#!/usr/bin/env python3
"""Validate gate-tier config and optional claim evidence."""

from __future__ import annotations

import argparse
import copy
import json
import re
import sys
import tempfile
import tomllib
from pathlib import Path
from typing import Any

SCRIPT_PATH = Path(__file__).resolve()
DEFAULT_CONFIG = None

EXPECTED_TIERS = ("smoke", "acceptance", "release")
EXPECTED_CLAIMS = ("smoke", "acceptance", "release")
GATE_ID_RE = re.compile(r"^[a-z0-9][a-z0-9_.-]*$")
EVIDENCE_KEYS = (
    "evidence",
    "evidence_refs",
    "artifacts",
    "artifact",
    "log_path",
    "log_ref",
    "report",
    "run_url",
    "url",
    "urls",
)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    if args.self_test:
        return self_test()

    data = load_toml(args.config)
    errors = validate_config(data)
    if args.claim:
        if not args.evidence:
            errors.append("--evidence is required when --claim is set")
        elif not errors:
            evidence = load_json(args.evidence)
            errors.extend(evaluate_claim(data, args.claim, evidence))

    if args.json:
        payload = {
            "status": "fail" if errors else "pass",
            "config": str(args.config),
            "claim": args.claim or "",
            "errors": errors,
        }
        print(json.dumps(payload, indent=2, sort_keys=True))
    elif errors:
        print("Gate-tier validation failed:", file=sys.stderr)
        for error in errors:
            print(f"  - {error}", file=sys.stderr)
    elif args.claim:
        print(f"OK  {args.claim} claim satisfied by {args.evidence}")
    else:
        print(f"OK  {display_path(args.config)}")

    return 1 if errors else 0


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--config", type=Path, default=default_config_path(), help="Gate-tier TOML config.")
    parser.add_argument("--claim", choices=EXPECTED_CLAIMS, help="Claim to validate against evidence.")
    parser.add_argument("--evidence", type=Path, help="Machine-readable evidence JSON.")
    parser.add_argument("--json", action="store_true", help="Emit a JSON validation result.")
    parser.add_argument("--self-test", action="store_true", help="Run validator self-tests.")
    return parser.parse_args(argv)


def load_toml(path: Path) -> dict[str, Any]:
    try:
        with path.open("rb") as f:
            data = tomllib.load(f)
    except OSError as exc:
        raise SystemExit(f"cannot read {path}: {exc}") from exc
    except tomllib.TOMLDecodeError as exc:
        raise SystemExit(f"invalid TOML in {path}: {exc}") from exc
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: TOML root must be a table")
    return data


def load_json(path: Path) -> dict[str, Any]:
    try:
        with path.open(encoding="utf-8") as f:
            data = json.load(f)
    except OSError as exc:
        raise SystemExit(f"cannot read {path}: {exc}") from exc
    except json.JSONDecodeError as exc:
        raise SystemExit(f"invalid JSON in {path}: {exc}") from exc
    if not isinstance(data, dict):
        raise SystemExit(f"{path}: JSON root must be an object")
    return data


def validate_config(data: dict[str, Any]) -> list[str]:
    errors: list[str] = []
    if data.get("schema_version") != 1:
        errors.append("schema_version must be 1")

    tiers = data.get("tiers")
    if not isinstance(tiers, dict):
        errors.append("[tiers] table is required")
        tiers = {}
    for tier_id in EXPECTED_TIERS:
        tier = tiers.get(tier_id)
        if not isinstance(tier, dict):
            errors.append(f"[tiers.{tier_id}] table is required")
            continue
        require_string(tier, "description", f"tiers.{tier_id}", errors)
        require_bool(tier, "evidence_required", f"tiers.{tier_id}", errors)

    claims = data.get("claims")
    if not isinstance(claims, dict):
        errors.append("[claims] table is required")
        claims = {}
    for claim_id in EXPECTED_CLAIMS:
        claim = claims.get(claim_id)
        if not isinstance(claim, dict):
            errors.append(f"[claims.{claim_id}] table is required")
            continue
        require_string(claim, "description", f"claims.{claim_id}", errors)
        required_tiers = require_string_list(claim, "required_tiers", f"claims.{claim_id}", errors)
        for tier_id in required_tiers:
            if tier_id not in EXPECTED_TIERS:
                errors.append(f"claims.{claim_id}.required_tiers references unknown tier {tier_id!r}")
    release = claims.get("release")
    if isinstance(release, dict) and release.get("missing_evidence") != "fail":
        errors.append('claims.release.missing_evidence must be "fail"')

    gates = data.get("gates")
    if not isinstance(gates, list) or not gates:
        errors.append("[[gates]] must contain at least one gate")
        gates = []

    seen_ids: set[str] = set()
    for index, gate in enumerate(gates, start=1):
        if not isinstance(gate, dict):
            errors.append(f"gates[{index}] must be a table")
            continue
        gate_id = require_string(gate, "id", f"gates[{index}]", errors)
        if gate_id:
            if not GATE_ID_RE.match(gate_id):
                errors.append(f"gates[{index}].id {gate_id!r} must match {GATE_ID_RE.pattern}")
            if gate_id in seen_ids:
                errors.append(f"duplicate gate id {gate_id!r}")
            seen_ids.add(gate_id)
        tier_id = require_string(gate, "tier", f"gates[{index}]", errors)
        if tier_id and tier_id not in EXPECTED_TIERS:
            errors.append(f"gate {gate_id or index} references unknown tier {tier_id!r}")
        require_string(gate, "description", f"gates[{index}]", errors)
        required_for = require_string_list(gate, "required_for", f"gates[{index}]", errors)
        for claim_id in required_for:
            if claim_id not in EXPECTED_CLAIMS:
                errors.append(f"gate {gate_id or index} required_for references unknown claim {claim_id!r}")
        if gate.get("command") is not None and not isinstance(gate.get("command"), str):
            errors.append(f"gate {gate_id or index} command must be a string when set")
        evidence_required = gate_evidence_required(gate, tiers)
        if gate.get("evidence_required") is not None and not isinstance(gate.get("evidence_required"), bool):
            errors.append(f"gate {gate_id or index} evidence_required must be a bool when set")
        if gate.get("optional") is not None and not isinstance(gate.get("optional"), bool):
            errors.append(f"gate {gate_id or index} optional must be a bool when set")
        if tier_id == "smoke" and "smoke" in required_for and not gate.get("command"):
            errors.append(f"smoke gate {gate_id or index} must declare command")
        if tier_id in {"acceptance", "release"} and not evidence_required:
            errors.append(f"{tier_id} gate {gate_id or index} must require evidence")

    gates_by_claim = gates_grouped_by_claim(gates)
    for claim_id in EXPECTED_CLAIMS:
        claim = claims.get(claim_id)
        if not isinstance(claim, dict):
            continue
        required_tiers = set(claim.get("required_tiers") or [])
        present_tiers = {gate.get("tier") for gate in gates_by_claim.get(claim_id, [])}
        missing = sorted(required_tiers - present_tiers)
        if missing:
            errors.append(f"claim {claim_id!r} has no required gate for tiers: {', '.join(missing)}")
    return errors


def require_string(data: dict[str, Any], key: str, owner: str, errors: list[str]) -> str:
    value = data.get(key)
    if not isinstance(value, str) or not value.strip():
        errors.append(f"{owner}.{key} must be a non-empty string")
        return ""
    return value.strip()


def require_bool(data: dict[str, Any], key: str, owner: str, errors: list[str]) -> bool:
    value = data.get(key)
    if not isinstance(value, bool):
        errors.append(f"{owner}.{key} must be a bool")
        return False
    return value


def require_string_list(data: dict[str, Any], key: str, owner: str, errors: list[str]) -> list[str]:
    value = data.get(key)
    if not isinstance(value, list) or not value:
        errors.append(f"{owner}.{key} must be a non-empty list of strings")
        return []
    out: list[str] = []
    for item in value:
        if not isinstance(item, str) or not item.strip():
            errors.append(f"{owner}.{key} must contain only non-empty strings")
            continue
        out.append(item.strip())
    return out


def gate_evidence_required(gate: dict[str, Any], tiers: dict[str, Any]) -> bool:
    if isinstance(gate.get("evidence_required"), bool):
        return bool(gate["evidence_required"])
    tier = tiers.get(gate.get("tier"))
    if isinstance(tier, dict) and isinstance(tier.get("evidence_required"), bool):
        return bool(tier["evidence_required"])
    return False


def gates_grouped_by_claim(gates: list[Any]) -> dict[str, list[dict[str, Any]]]:
    grouped: dict[str, list[dict[str, Any]]] = {}
    for gate in gates:
        if not isinstance(gate, dict):
            continue
        if gate.get("optional") is True:
            continue
        for claim_id in gate.get("required_for") or []:
            if isinstance(claim_id, str):
                grouped.setdefault(claim_id, []).append(gate)
    return grouped


def evaluate_claim(data: dict[str, Any], claim_id: str, evidence: dict[str, Any]) -> list[str]:
    errors = validate_config(data)
    if errors:
        return errors
    gates = data["gates"]
    tiers = data["tiers"]
    results = latest_gate_results(evidence)
    required = [gate for gate in gates if claim_id in gate.get("required_for", [])]
    out: list[str] = []
    for gate in required:
        gate_id = gate["id"]
        result = results.get(gate_id)
        if not result:
            if gate.get("optional") is True:
                continue
            out.append(f"{claim_id} claim missing gate result {gate_id!r}")
            continue
        status = str(result.get("status", "")).strip().lower()
        if status != "pass":
            out.append(f"{claim_id} claim gate {gate_id!r} status is {status or 'missing'}, want pass")
            continue
        if gate_evidence_required(gate, tiers) and not has_evidence(result):
            out.append(f"{claim_id} claim gate {gate_id!r} passed without evidence")
    return out


def latest_gate_results(evidence: dict[str, Any]) -> dict[str, dict[str, Any]]:
    gates = evidence.get("gates")
    if not isinstance(gates, list):
        return {}
    out: dict[str, dict[str, Any]] = {}
    for item in gates:
        if not isinstance(item, dict):
            continue
        gate_id = item.get("id") or item.get("name")
        if not isinstance(gate_id, str) or not gate_id.strip():
            continue
        out[gate_id.strip()] = item
    return out


def has_evidence(result: dict[str, Any]) -> bool:
    for key in EVIDENCE_KEYS:
        if non_empty(result.get(key)):
            return True
    return False


def non_empty(value: Any) -> bool:
    if value is None:
        return False
    if isinstance(value, str):
        return bool(value.strip())
    if isinstance(value, (list, tuple, set, dict)):
        return bool(value)
    return True


def display_path(path: Path) -> str:
    resolved = path.resolve()
    roots = [Path.cwd().resolve(), *resolved.parents]
    for root in roots:
        try:
            rel = resolved.relative_to(root)
        except ValueError:
            continue
        if rel.parts:
            return str(rel)
    return str(path)


def default_config_path() -> Path:
    global DEFAULT_CONFIG
    if DEFAULT_CONFIG is not None:
        return DEFAULT_CONFIG

    for ancestor in (SCRIPT_PATH.parent, *SCRIPT_PATH.parents):
        for candidate in (
            ancestor / "gates.toml",
            ancestor / ".agent_team" / "gates.toml",
            ancestor / "template" / "gates.toml",
        ):
            if candidate.is_file():
                DEFAULT_CONFIG = candidate
                return DEFAULT_CONFIG

    # Let load_toml produce the actionable error if the script is copied into a
    # nonstandard layout and no explicit --config is supplied.
    DEFAULT_CONFIG = Path(".agent_team") / "gates.toml"
    return DEFAULT_CONFIG


def self_test() -> int:
    data = load_toml(default_config_path())
    errors = validate_config(data)
    if errors:
        return fail_self_test("default config should validate", errors)

    smoke_evidence = {
        "schema_version": 1,
        "gates": [
            {"id": gate["id"], "status": "pass"}
            for gate in data["gates"]
            if gate["tier"] == "smoke"
        ],
    }
    errors = evaluate_claim(data, "smoke", smoke_evidence)
    if errors:
        return fail_self_test("smoke evidence should satisfy smoke claim", errors)

    errors = evaluate_claim(data, "release", smoke_evidence)
    if not errors:
        return fail_self_test("smoke evidence must not satisfy release claim", [])
    if not any("acceptance-evidence" in error for error in errors):
        return fail_self_test("release refusal should name missing acceptance evidence", errors)

    optional_missing_evidence = {"schema_version": 1, "gates": []}
    for gate in data["gates"]:
        if gate.get("optional") is True:
            continue
        result = {"id": gate["id"], "status": "pass"}
        if gate_evidence_required(gate, data["tiers"]):
            result["evidence"] = [f"test://evidence/{gate['id']}"]
        optional_missing_evidence["gates"].append(result)
    errors = evaluate_claim(data, "release", optional_missing_evidence)
    if errors:
        return fail_self_test("optional gates should not be required when absent", errors)

    full_evidence = {"schema_version": 1, "gates": []}
    for gate in data["gates"]:
        result = {"id": gate["id"], "status": "pass"}
        if gate_evidence_required(gate, data["tiers"]):
            result["evidence"] = [f"test://evidence/{gate['id']}"]
        full_evidence["gates"].append(result)
    errors = evaluate_claim(data, "release", full_evidence)
    if errors:
        return fail_self_test("full evidence should satisfy release claim", errors)

    broken = copy.deepcopy(data)
    broken["claims"]["release"]["missing_evidence"] = "pass"
    errors = validate_config(broken)
    if not any("missing_evidence" in error for error in errors):
        return fail_self_test("release missing_evidence must be fail", errors)

    with tempfile.TemporaryDirectory() as tmp:
        evidence_path = Path(tmp) / "smoke.json"
        evidence_path.write_text(json.dumps(smoke_evidence), encoding="utf-8")
        if not evidence_path.exists():
            return fail_self_test("self-test evidence fixture was not written", [])

    print("OK  gate-tier validator self-test")
    return 0


def fail_self_test(name: str, errors: list[str]) -> int:
    print(f"gate-tier validator self-test failed: {name}", file=sys.stderr)
    for error in errors:
        print(f"  - {error}", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

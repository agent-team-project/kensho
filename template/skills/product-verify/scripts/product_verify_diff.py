#!/usr/bin/env python3
"""Compare daemon UI data endpoints with equivalent agent-team CLI output."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


class ProductVerifyError(RuntimeError):
    pass


FieldSpec = tuple[str, tuple[str, ...], Any]


# Equivalence projections define the state both the daemon endpoint and CLI
# expose for the same underlying resource.
#
# Instances are limited to daemon metadata fields that /v1/instances and
# `agent-team ps --json` both expose for the same daemon row. `ps` also merges
# declared/status-only rows and CLI/job-store enrichment; those are not daemon
# metadata and must not create product-verifier bugs. Exclusions: `pr` is
# job-store enrichment, `branch`/`workspace` may be sourced or preferred from
# status files/topology instead of daemon metadata, and process/runtime details
# such as pid, runtime_binary, or resume_count are computed by the CLI.
INSTANCE_DAEMON_METADATA_FIELDS: tuple[FieldSpec, ...] = (
    ("instance", ("instance",), ""),
    ("agent", ("agent",), ""),
    ("job", ("job",), ""),
    ("status", ("status",), ""),
    ("runtime", ("runtime",), ""),
)


JOB_EQUIVALENCE_FIELDS: tuple[FieldSpec, ...] = (
    ("id", ("id", "ID"), ""),
    ("ticket", ("ticket", "Ticket"), ""),
    ("ticket_url", ("ticket_url", "TicketURL"), ""),
    ("target", ("target", "Target"), ""),
    ("implementation_agent", ("implementation_agent", "ImplementationAgent"), ""),
    ("instance", ("instance", "Instance"), ""),
    ("pipeline", ("pipeline", "Pipeline"), ""),
    ("status", ("status", "Status"), ""),
    ("held", ("held", "Held"), False),
    ("branch", ("branch", "Branch"), ""),
    ("pr", ("pr", "PR"), ""),
    ("last_event", ("last_event", "LastEvent"), ""),
    ("last_status", ("last_status", "LastStatus"), ""),
    ("created_at", ("created_at", "CreatedAt"), ""),
    ("updated_at", ("updated_at", "UpdatedAt"), ""),
)


TOPOLOGY_EQUIVALENCE_SECTIONS: dict[str, tuple[str, tuple[FieldSpec, ...]]] = {
    "instances": (
        "name",
        (
            ("name", ("name",), ""),
            ("agent", ("agent",), ""),
            ("ephemeral", ("ephemeral",), False),
            ("description", ("description",), ""),
            ("replicas", ("replicas",), 0),
            ("reap_worktree", ("reap_worktree",), ""),
            ("config", ("config",), {}),
            ("triggers", ("triggers",), []),
            ("running", ("running",), 0),
            ("queued", ("queued",), 0),
        ),
    ),
    "pipelines": (
        "name",
        (
            ("name", ("name",), ""),
            ("trigger", ("trigger",), {}),
            ("steps", ("steps",), []),
            ("auto_advance", ("auto_advance",), False),
            ("reap_worktree", ("reap_worktree",), ""),
            ("redispatch_on_reentry", ("redispatch_on_reentry",), False),
            ("merge", ("merge",), {}),
        ),
    ),
    "schedules": (
        "name",
        (
            ("name", ("name",), ""),
            ("every", ("every",), ""),
            ("run_on_start", ("run_on_start",), False),
            ("payload", ("payload",), {}),
        ),
    ),
    "teams": (
        "name",
        (
            ("name", ("name",), ""),
            ("description", ("description",), ""),
            ("instances", ("instances",), []),
            ("pipelines", ("pipelines",), []),
            ("schedules", ("schedules",), []),
            ("channels", ("channels",), []),
        ),
    ),
    "budgets": (
        "team",
        (
            ("team", ("team",), ""),
            ("tokens_per_day", ("tokens_per_day",), 0),
            ("jobs_in_flight", ("jobs_in_flight",), 0),
            ("allocation", ("allocation",), ""),
        ),
    ),
}


def stable_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), default=str)


def canonical(value: Any) -> Any:
    if isinstance(value, dict):
        return {str(key): canonical(value[key]) for key in sorted(value)}
    if isinstance(value, list):
        normalized = [canonical(item) for item in value]
        return sorted(normalized, key=stable_json)
    return value


def get_value(record: dict[str, Any], aliases: tuple[str, ...], default: Any) -> Any:
    for alias in aliases:
        if alias in record and record[alias] is not None:
            return record[alias]
    return default


def project_record(record: dict[str, Any], fields: tuple[FieldSpec, ...]) -> dict[str, Any]:
    return {
        name: canonical(get_value(record, aliases, default))
        for name, aliases, default in fields
    }


def normalize_record_list(
    records: Any,
    key_field: str,
    fields: tuple[FieldSpec, ...],
) -> dict[str, dict[str, Any]]:
    if records is None:
        records = []
    if not isinstance(records, list):
        raise ProductVerifyError(f"expected list for {key_field} records, got {type(records).__name__}")
    out: dict[str, dict[str, Any]] = {}
    for record in records:
        if not isinstance(record, dict):
            raise ProductVerifyError(f"expected object in {key_field} records, got {type(record).__name__}")
        projected = project_record(record, fields)
        key = str(projected.get(key_field, "")).strip()
        if not key:
            raise ProductVerifyError(f"{key_field} record is missing key field {key_field!r}: {record!r}")
        out[key] = projected
    return out


def normalize_instances(records: Any) -> dict[str, dict[str, Any]]:
    return normalize_record_list(records, "instance", INSTANCE_DAEMON_METADATA_FIELDS)


def normalize_jobs(records: Any) -> dict[str, dict[str, Any]]:
    return normalize_record_list(records, "id", JOB_EQUIVALENCE_FIELDS)


def normalize_topology(payload: Any) -> dict[str, Any]:
    if not isinstance(payload, dict):
        raise ProductVerifyError(f"expected topology object, got {type(payload).__name__}")
    out: dict[str, Any] = {}
    for section, (key_field, fields) in TOPOLOGY_EQUIVALENCE_SECTIONS.items():
        out[section] = normalize_record_list(payload.get(section, []), key_field, fields)
    out["budget_reminder_levels"] = canonical(payload.get("budget_reminder_levels", []))
    return out


def diff_record_maps(
    comparison: str,
    ui_records: dict[str, dict[str, Any]],
    cli_records: dict[str, dict[str, Any]],
) -> list[dict[str, Any]]:
    diffs: list[dict[str, Any]] = []
    ui_keys = set(ui_records)
    cli_keys = set(cli_records)
    for key in sorted(ui_keys - cli_keys):
        diffs.append({"type": "missing_in_cli", "comparison": comparison, "key": key, "ui": ui_records[key]})
    for key in sorted(cli_keys - ui_keys):
        diffs.append({"type": "missing_in_ui", "comparison": comparison, "key": key, "cli": cli_records[key]})
    for key in sorted(ui_keys & cli_keys):
        ui_record = ui_records[key]
        cli_record = cli_records[key]
        for field in sorted(set(ui_record) | set(cli_record)):
            ui_value = ui_record.get(field)
            cli_value = cli_record.get(field)
            if ui_value != cli_value:
                diffs.append(
                    {
                        "type": "field_mismatch",
                        "comparison": comparison,
                        "key": key,
                        "field": field,
                        "ui": ui_value,
                        "cli": cli_value,
                    }
                )
    return diffs


def compare_records(
    name: str,
    ui_records: Any,
    cli_records: Any,
    normalizer,
) -> dict[str, Any]:
    ui_normalized = normalizer(ui_records)
    cli_normalized = normalizer(cli_records)
    diffs = diff_record_maps(name, ui_normalized, cli_normalized)
    return {
        "name": name,
        "ok": not diffs,
        "ui_count": len(ui_normalized),
        "cli_count": len(cli_normalized),
        "diffs": diffs,
    }


def compare_instances(ui_records: Any, cli_records: Any) -> dict[str, Any]:
    ui_normalized = normalize_instances(ui_records)
    cli_normalized = normalize_instances(cli_records)
    shared_names = set(ui_normalized) & set(cli_normalized)
    diffs = diff_record_maps(
        "instances",
        {name: ui_normalized[name] for name in shared_names},
        {name: cli_normalized[name] for name in shared_names},
    )
    return {
        "name": "instances",
        "ok": not diffs,
        "ui_count": len(ui_normalized),
        "cli_count": len(cli_normalized),
        "diffs": diffs,
    }


def compare_topology(ui_payload: Any, cli_payload: Any) -> dict[str, Any]:
    ui_topology = normalize_topology(ui_payload)
    cli_topology = normalize_topology(cli_payload)
    diffs: list[dict[str, Any]] = []
    for section in TOPOLOGY_EQUIVALENCE_SECTIONS:
        diffs.extend(diff_record_maps(f"topology.{section}", ui_topology[section], cli_topology[section]))
    if ui_topology["budget_reminder_levels"] != cli_topology["budget_reminder_levels"]:
        diffs.append(
            {
                "type": "field_mismatch",
                "comparison": "topology",
                "key": "budget_reminder_levels",
                "field": "budget_reminder_levels",
                "ui": ui_topology["budget_reminder_levels"],
                "cli": cli_topology["budget_reminder_levels"],
            }
        )
    return {
        "name": "topology",
        "ok": not diffs,
        "ui_count": sum(len(ui_topology[section]) for section in TOPOLOGY_EQUIVALENCE_SECTIONS),
        "cli_count": sum(len(cli_topology[section]) for section in TOPOLOGY_EQUIVALENCE_SECTIONS),
        "diffs": diffs,
    }


def finding_for_diff(diff: dict[str, Any]) -> dict[str, str]:
    comparison = diff.get("comparison", "unknown")
    key = diff.get("key", "unknown")
    if diff.get("type") == "field_mismatch":
        field = diff.get("field", "unknown")
        summary = f"product-verifier: {comparison} differs from CLI for {key} field {field}"
    elif diff.get("type") == "missing_in_ui":
        summary = f"product-verifier: {comparison} is missing CLI record {key}"
    elif diff.get("type") == "missing_in_cli":
        summary = f"product-verifier: CLI is missing {comparison} record {key}"
    else:
        summary = f"product-verifier: {comparison} differs for {key}"
    return {
        "category": "bug",
        "summary": summary,
        "detail": stable_json(diff),
    }


def build_report(ui_data: dict[str, Any], cli_data: dict[str, Any], max_findings: int) -> dict[str, Any]:
    comparisons = [
        compare_instances(ui_data.get("instances", []), cli_data.get("instances", [])),
        compare_records("jobs", ui_data.get("jobs", []), cli_data.get("jobs", []), normalize_jobs),
        compare_topology(ui_data.get("topology", {}), cli_data.get("topology", {})),
    ]
    all_diffs = [diff for comparison in comparisons for diff in comparison["diffs"]]
    findings = [finding_for_diff(diff) for diff in all_diffs]
    capped = False
    if max_findings >= 0 and len(findings) > max_findings:
        findings = findings[:max_findings]
        capped = True
    return {
        "status": "mismatch" if all_diffs else "ok",
        "summary": {
            "comparisons": len(comparisons),
            "mismatches": len(all_diffs),
            "findings": len(findings),
            "capped": capped,
        },
        "comparisons": comparisons,
        "findings": findings,
    }


def resolve_team_dir(raw: str | None) -> Path:
    if raw:
        return Path(raw).expanduser().resolve()
    env_root = os.environ.get("AGENT_TEAM_ROOT")
    if env_root:
        return Path(env_root).expanduser().resolve()
    return (Path.cwd() / ".agent_team").resolve()


def resolve_repo(raw: str | None, team_dir: Path) -> Path:
    if raw:
        return Path(raw).expanduser().resolve()
    return team_dir.parent.resolve()


def resolve_daemon_url(team_dir: Path) -> str | None:
    env_url = os.environ.get("AGENT_TEAM_DAEMON_URL", "").strip()
    if env_url:
        return env_url.rstrip("/")
    addr_file = team_dir / "daemon" / "http.addr"
    if not addr_file.exists():
        return None
    addr = addr_file.read_text(encoding="utf-8").strip()
    if not addr:
        return None
    if addr.startswith("http://") or addr.startswith("https://"):
        return addr.rstrip("/")
    return f"http://{addr}".rstrip("/")


def read_operator_token(team_dir: Path) -> str:
    token_file = team_dir / "daemon" / "operator.token"
    try:
        token = token_file.read_text(encoding="utf-8").strip()
    except FileNotFoundError as exc:
        raise ProductVerifyError(f"operator token not found at {token_file}") from exc
    if not token:
        raise ProductVerifyError(f"operator token is empty at {token_file}")
    return token


def fetch_json(base_url: str, token: str, path: str) -> Any:
    url = f"{base_url}/{path.lstrip('/')}"
    request = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    try:
        with urllib.request.urlopen(request, timeout=10) as response:
            body = response.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        raise ProductVerifyError(f"GET {path}: HTTP {exc.code}: {exc.read().decode('utf-8', 'replace')}") from exc
    except urllib.error.URLError as exc:
        raise ProductVerifyError(f"GET {path}: {exc.reason}") from exc
    try:
        return json.loads(body)
    except json.JSONDecodeError as exc:
        raise ProductVerifyError(f"GET {path}: invalid JSON: {exc}") from exc


def fetch_ui_data(base_url: str, token: str) -> dict[str, Any]:
    return {
        "instances": fetch_json(base_url, token, "/v1/instances"),
        "jobs": fetch_json(base_url, token, "/v1/jobs"),
        "topology": fetch_json(base_url, token, "/v1/topology"),
    }


def run_json_command(args: list[str]) -> Any:
    try:
        result = subprocess.run(args, check=False, text=True, capture_output=True)
    except OSError as exc:
        raise ProductVerifyError(f"{args[0]} failed to start: {exc}") from exc
    if result.returncode != 0:
        stderr = result.stderr.strip()
        stdout = result.stdout.strip()
        detail = stderr or stdout or f"exit {result.returncode}"
        raise ProductVerifyError(f"{' '.join(args)} failed: {detail}")
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise ProductVerifyError(f"{' '.join(args)} produced invalid JSON: {exc}") from exc


def fetch_cli_data(agent_team_bin: str, repo: Path) -> dict[str, Any]:
    repo_arg = str(repo)
    return {
        "instances": run_json_command([agent_team_bin, "--repo", repo_arg, "ps", "--json"]),
        "jobs": run_json_command([agent_team_bin, "--repo", repo_arg, "job", "ls", "--json"]),
        "topology": run_json_command([agent_team_bin, "--repo", repo_arg, "topology", "show", "--json"]),
    }


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--team-dir", help="Path to .agent_team. Defaults to AGENT_TEAM_ROOT or ./.agent_team.")
    parser.add_argument("--repo", help="Repo root for CLI reads. Defaults to the parent of --team-dir.")
    parser.add_argument("--agent-team-bin", default=os.environ.get("AGENT_TEAM_BIN", "agent-team"))
    parser.add_argument("--max-findings", type=int, default=5, help="Maximum bug findings to include. Use -1 for unlimited.")
    parser.add_argument("--no-fail", action="store_true", help="Exit 0 even when mismatches are found.")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    team_dir = resolve_team_dir(args.team_dir)
    repo = resolve_repo(args.repo, team_dir)
    daemon_url = resolve_daemon_url(team_dir)
    if not daemon_url:
        print(
            json.dumps(
                {
                    "status": "skipped",
                    "reason": "daemon HTTP address is not configured",
                    "expected": str(team_dir / "daemon" / "http.addr"),
                },
                indent=2,
                sort_keys=True,
            )
        )
        return 0
    try:
        token = read_operator_token(team_dir)
        ui_data = fetch_ui_data(daemon_url, token)
        cli_data = fetch_cli_data(args.agent_team_bin, repo)
        report = build_report(ui_data, cli_data, args.max_findings)
    except ProductVerifyError as exc:
        print(json.dumps({"status": "error", "error": str(exc)}, indent=2, sort_keys=True))
        return 2
    print(json.dumps(report, indent=2, sort_keys=True))
    if report["status"] == "mismatch" and not args.no_fail:
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

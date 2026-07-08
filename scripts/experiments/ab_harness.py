#!/usr/bin/env python3
"""Dry-run and compare topology A/B experiments.

The harness is intentionally local and stdlib-only. It validates a canonical
backlog, checks that two experiment arms cover the same work, summarizes their
topology files, and emits a reproducible Markdown report. When result JSON is
available, it also computes difficulty-normalized comparison metrics.
"""

from __future__ import annotations

import argparse
import json
import math
import sys
import tomllib
from collections import Counter
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


DIFFICULTY_POINTS = {
    "xs": 1.0,
    "s": 2.0,
    "small": 2.0,
    "m": 3.0,
    "medium": 3.0,
    "l": 5.0,
    "large": 5.0,
    "xl": 8.0,
}


@dataclass(frozen=True)
class WorkItem:
    id: str
    title: str
    difficulty: float
    depends_on: tuple[str, ...] = ()


@dataclass(frozen=True)
class WorkSlice:
    id: str
    title: str
    item_ids: tuple[str, ...]
    difficulty: float
    depends_on: tuple[str, ...] = ()


@dataclass
class Backlog:
    name: str
    hypothesis: str
    items: dict[str, WorkItem]
    raw_arms: dict[str, Any] = field(default_factory=dict)

    @property
    def total_difficulty(self) -> float:
        return sum(item.difficulty for item in self.items.values())


@dataclass(frozen=True)
class TopologySummary:
    path: Path
    instance_count: int
    ephemeral_instances: int
    ephemeral_capacity: int
    worker_capacity: int
    reviewer_capacity: int
    verifier_capacity: int
    pipeline_steps: dict[str, tuple[str, ...]]
    verifier_before_review: bool


@dataclass
class Arm:
    label: str
    topology_path: Path
    result_path: Path | None
    slices: list[WorkSlice]
    topology: TopologySummary


@dataclass
class ArmMetrics:
    label: str
    source: str
    expected_slices: int
    expected_slice_ids: tuple[str, ...] = ()
    merged_slices: int | None = None
    wall_clock_seconds: float | None = None
    effective_concurrency: float | None = None
    tokens_consumed: float | None = None
    bounce_rounds: float | None = None
    review_rounds: float | None = None
    human_interventions: float | None = None
    escaped_defects: float | None = None
    failed_required_gates: float | None = None
    quality_pass: bool | None = None
    quality_notes: list[str] = field(default_factory=list)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    try:
        backlog = load_backlog(args.backlog)
        baseline_slices = slices_for_arm(backlog, args.baseline_arm)
        candidate_slices = slices_for_arm(backlog, args.candidate_arm)
        baseline = Arm(
            label=args.baseline_label,
            topology_path=args.baseline_topology,
            result_path=args.baseline_results,
            slices=baseline_slices,
            topology=load_topology_summary(args.baseline_topology),
        )
        candidate = Arm(
            label=args.candidate_label,
            topology_path=args.candidate_topology,
            result_path=args.candidate_results,
            slices=candidate_slices,
            topology=load_topology_summary(args.candidate_topology),
        )
        ensure_same_work(backlog, baseline, candidate)
        baseline_metrics = load_metrics(baseline, backlog.total_difficulty)
        candidate_metrics = load_metrics(candidate, backlog.total_difficulty)
        generated_at = datetime.now(timezone.utc)
        report = render_report(
            backlog=backlog,
            baseline=baseline,
            candidate=candidate,
            baseline_metrics=baseline_metrics,
            candidate_metrics=candidate_metrics,
            generated_at=generated_at,
        )
        write_text(args.output, report)
        if args.summary_json:
            write_text(
                args.summary_json,
                json.dumps(
                    summary_json(backlog, baseline, candidate, baseline_metrics, candidate_metrics, generated_at),
                    indent=2,
                    sort_keys=True,
                )
                + "\n",
            )
    except HarnessError as exc:
        print(f"ab_harness.py: {exc}", file=sys.stderr)
        return 2
    return 0


def parse_args(argv: list[str] | None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Build a dry-run or result comparison report for a topology A/B experiment."
    )
    parser.add_argument("--backlog", required=True, type=Path, help="Canonical backlog JSON.")
    parser.add_argument("--baseline-topology", required=True, type=Path, help="Baseline instances.toml.")
    parser.add_argument("--candidate-topology", required=True, type=Path, help="Candidate instances.toml.")
    parser.add_argument("--baseline-arm", default="baseline", help="Backlog arm key for baseline slicing.")
    parser.add_argument(
        "--candidate-arm",
        default="slim-verifier-first",
        help="Backlog arm key for candidate slicing.",
    )
    parser.add_argument("--baseline-label", default="baseline", help="Display label for baseline.")
    parser.add_argument(
        "--candidate-label",
        default="slim-verifier-first",
        help="Display label for candidate.",
    )
    parser.add_argument("--baseline-results", type=Path, help="Optional result JSON for baseline.")
    parser.add_argument("--candidate-results", type=Path, help="Optional result JSON for candidate.")
    parser.add_argument("--output", type=Path, help="Write Markdown report here; defaults to stdout.")
    parser.add_argument("--summary-json", type=Path, help="Optional machine-readable summary output.")
    return parser.parse_args(argv)


class HarnessError(Exception):
    """User-facing harness input error."""


def load_backlog(path: Path) -> Backlog:
    raw = load_json(path)
    if isinstance(raw, list):
        items_raw = raw
        name = path.stem
        hypothesis = ""
        raw_arms: dict[str, Any] = {}
    elif isinstance(raw, dict):
        items_raw = raw.get("items") or raw.get("work_items") or raw.get("backlog")
        name = str(raw.get("name") or path.stem)
        hypothesis = str(raw.get("hypothesis") or "")
        raw_arms = raw.get("arms") or {}
    else:
        raise HarnessError(f"{path}: backlog must be a JSON object or array")
    if not isinstance(items_raw, list) or not items_raw:
        raise HarnessError(f"{path}: backlog must contain a non-empty items/work_items array")
    if not isinstance(raw_arms, dict):
        raise HarnessError(f"{path}: arms must be an object when present")

    items: dict[str, WorkItem] = {}
    for index, item_raw in enumerate(items_raw, start=1):
        if not isinstance(item_raw, dict):
            raise HarnessError(f"{path}: item {index} must be an object")
        item_id = clean_id(item_raw.get("id"))
        if not item_id:
            raise HarnessError(f"{path}: item {index} is missing id")
        if item_id in items:
            raise HarnessError(f"{path}: duplicate item id {item_id!r}")
        depends_on = tuple(clean_id(dep) for dep in as_list(item_raw.get("depends_on") or item_raw.get("dependencies") or item_raw.get("after")))
        if "" in depends_on:
            raise HarnessError(f"{path}: item {item_id!r} has an empty dependency")
        items[item_id] = WorkItem(
            id=item_id,
            title=str(item_raw.get("title") or item_raw.get("name") or item_id),
            difficulty=difficulty_value(item_raw, f"{path}: item {item_id!r}"),
            depends_on=depends_on,
        )
    for item in items.values():
        for dep in item.depends_on:
            if dep not in items:
                raise HarnessError(f"{path}: item {item.id!r} depends on unknown item {dep!r}")
    dependency_waves(
        [
            WorkSlice(
                id=item.id,
                title=item.title,
                item_ids=(item.id,),
                difficulty=item.difficulty,
                depends_on=item.depends_on,
            )
            for item in items.values()
        ],
        label=f"{path}: backlog items",
    )
    return Backlog(name=name, hypothesis=hypothesis, items=items, raw_arms=raw_arms)


def slices_for_arm(backlog: Backlog, arm_label: str) -> list[WorkSlice]:
    arm_raw = backlog.raw_arms.get(arm_label, {})
    if arm_raw is None:
        arm_raw = {}
    if not isinstance(arm_raw, dict):
        raise HarnessError(f"backlog arm {arm_label!r} must be an object")
    slice_defs = arm_raw.get("slices") or arm_raw.get("work_slices")
    if slice_defs is None:
        slices = [
            WorkSlice(
                id=item.id,
                title=item.title,
                item_ids=(item.id,),
                difficulty=item.difficulty,
                depends_on=item.depends_on,
            )
            for item in backlog.items.values()
        ]
        return ordered_by_waves(slices, label=f"arm {arm_label!r}")
    if not isinstance(slice_defs, list) or not slice_defs:
        raise HarnessError(f"backlog arm {arm_label!r}: slices must be a non-empty array")

    declared: list[dict[str, Any]] = []
    item_to_slice: dict[str, str] = {}
    slice_ids: set[str] = set()
    for index, raw_slice in enumerate(slice_defs, start=1):
        if not isinstance(raw_slice, dict):
            raise HarnessError(f"backlog arm {arm_label!r}: slice {index} must be an object")
        item_ids = tuple(clean_id(item) for item in as_list(raw_slice.get("items") or raw_slice.get("work_items") or raw_slice.get("item")))
        if not item_ids:
            raise HarnessError(f"backlog arm {arm_label!r}: slice {index} has no items")
        if "" in item_ids:
            raise HarnessError(f"backlog arm {arm_label!r}: slice {index} has an empty item id")
        for item_id in item_ids:
            if item_id not in backlog.items:
                raise HarnessError(f"backlog arm {arm_label!r}: slice {index} references unknown item {item_id!r}")
            if item_id in item_to_slice:
                raise HarnessError(
                    f"backlog arm {arm_label!r}: item {item_id!r} appears in both "
                    f"{item_to_slice[item_id]!r} and slice {index}"
                )
        slice_id = clean_id(raw_slice.get("id")) or (item_ids[0] if len(item_ids) == 1 else f"{arm_label}-{index}")
        if slice_id in slice_ids:
            raise HarnessError(f"backlog arm {arm_label!r}: duplicate slice id {slice_id!r}")
        slice_ids.add(slice_id)
        for item_id in item_ids:
            item_to_slice[item_id] = slice_id
        declared.append({"id": slice_id, "raw": raw_slice, "items": item_ids})

    missing = sorted(set(backlog.items) - set(item_to_slice))
    if missing:
        raise HarnessError(f"backlog arm {arm_label!r}: slices do not cover items: {', '.join(missing)}")

    slices: list[WorkSlice] = []
    for declared_slice in declared:
        raw_slice = declared_slice["raw"]
        slice_id = declared_slice["id"]
        item_ids = declared_slice["items"]
        deps: set[str] = set()
        for item_id in item_ids:
            for dep_item in backlog.items[item_id].depends_on:
                dep_slice = item_to_slice[dep_item]
                if dep_slice != slice_id:
                    deps.add(dep_slice)
        for raw_dep in as_list(raw_slice.get("depends_on") or raw_slice.get("dependencies") or raw_slice.get("after")):
            dep = clean_id(raw_dep)
            if dep in backlog.items:
                dep = item_to_slice[dep]
            if dep and dep != slice_id:
                deps.add(dep)
        unknown_deps = sorted(dep for dep in deps if dep not in slice_ids)
        if unknown_deps:
            raise HarnessError(
                f"backlog arm {arm_label!r}: slice {slice_id!r} depends on unknown slices: "
                + ", ".join(unknown_deps)
            )
        difficulty = sum(backlog.items[item_id].difficulty for item_id in item_ids)
        title = str(raw_slice.get("title") or raw_slice.get("name") or slice_id)
        slices.append(
            WorkSlice(
                id=slice_id,
                title=title,
                item_ids=item_ids,
                difficulty=difficulty,
                depends_on=tuple(sorted(deps)),
            )
        )
    return ordered_by_waves(slices, label=f"arm {arm_label!r}")


def load_topology_summary(path: Path) -> TopologySummary:
    if not path.exists():
        raise HarnessError(f"{path}: topology file not found")
    try:
        with path.open("rb") as f:
            data = tomllib.load(f)
    except tomllib.TOMLDecodeError as exc:
        raise HarnessError(f"{path}: invalid TOML: {exc}") from exc
    instances = data.get("instances") or {}
    if not isinstance(instances, dict):
        raise HarnessError(f"{path}: [instances] must be a TOML table")
    instance_count = len(instances)
    ephemeral_instances = 0
    ephemeral_capacity = 0
    worker_capacity = 0
    reviewer_capacity = 0
    verifier_capacity = 0
    for name, raw in instances.items():
        if not isinstance(raw, dict):
            continue
        ephemeral = bool(raw.get("ephemeral"))
        replicas = int(raw.get("replicas") or (1 if ephemeral else 0))
        agent = str(raw.get("agent") or "")
        description = str(raw.get("description") or "")
        haystack = " ".join([str(name), agent, description]).lower()
        if ephemeral:
            ephemeral_instances += 1
            ephemeral_capacity += replicas
        if "verifier" in haystack or "verify" in haystack:
            verifier_capacity += replicas
        elif "reviewer" in haystack or agent == "reviewer":
            reviewer_capacity += replicas
        elif "worker" in haystack or agent == "worker":
            worker_capacity += replicas

    pipeline_steps: dict[str, tuple[str, ...]] = {}
    verifier_before_review = False
    pipelines = data.get("pipelines") or {}
    if isinstance(pipelines, dict):
        for pipeline_name, raw_pipeline in pipelines.items():
            if not isinstance(raw_pipeline, dict):
                continue
            steps = raw_pipeline.get("steps") or []
            step_labels: list[str] = []
            verifier_index: int | None = None
            review_index: int | None = None
            if isinstance(steps, list):
                for index, step in enumerate(steps):
                    if not isinstance(step, dict):
                        continue
                    step_id = str(step.get("id") or f"step-{index + 1}")
                    target = str(step.get("target") or "")
                    step_labels.append(f"{step_id}->{target}" if target else step_id)
                    haystack = f"{step_id} {target} {step.get('description', '')}".lower()
                    if verifier_index is None and ("verifier" in haystack or "verify" in haystack):
                        verifier_index = index
                    if review_index is None and ("review" in haystack or "reviewer" in haystack):
                        review_index = index
            pipeline_steps[str(pipeline_name)] = tuple(step_labels)
            if verifier_index is not None and review_index is not None and verifier_index < review_index:
                verifier_before_review = True
    return TopologySummary(
        path=path,
        instance_count=instance_count,
        ephemeral_instances=ephemeral_instances,
        ephemeral_capacity=ephemeral_capacity,
        worker_capacity=worker_capacity,
        reviewer_capacity=reviewer_capacity,
        verifier_capacity=verifier_capacity,
        pipeline_steps=pipeline_steps,
        verifier_before_review=verifier_before_review,
    )


def ensure_same_work(backlog: Backlog, baseline: Arm, candidate: Arm) -> None:
    expected = set(backlog.items)
    for arm in (baseline, candidate):
        covered = {item_id for work_slice in arm.slices for item_id in work_slice.item_ids}
        if covered != expected:
            missing = sorted(expected - covered)
            extra = sorted(covered - expected)
            parts = []
            if missing:
                parts.append("missing " + ", ".join(missing))
            if extra:
                parts.append("extra " + ", ".join(extra))
            raise HarnessError(f"{arm.label}: does not cover the same canonical work ({'; '.join(parts)})")


def load_metrics(arm: Arm, total_difficulty: float) -> ArmMetrics | None:
    if arm.result_path is None:
        return None
    raw = load_json(arm.result_path)
    metrics = metrics_from_result(
        raw,
        arm.label,
        arm.result_path,
        tuple(work_slice.id for work_slice in arm.slices),
    )
    if total_difficulty <= 0:
        metrics.quality_notes.append("total difficulty is zero; normalized metrics are unavailable")
    return metrics


def metrics_from_result(raw: Any, label: str, path: Path, expected_slice_ids: tuple[str, ...]) -> ArmMetrics:
    if isinstance(raw, dict) and "summary" in raw and isinstance(raw["summary"], dict):
        return metrics_from_summary(raw["summary"], label, path, expected_slice_ids)
    records = result_records(raw)
    if records is not None:
        return metrics_from_records(records, label, path, expected_slice_ids)
    if isinstance(raw, dict):
        return metrics_from_summary(
            raw.get("metrics") if isinstance(raw.get("metrics"), dict) else raw,
            label,
            path,
            expected_slice_ids,
        )
    raise HarnessError(f"{path}: result JSON must be an object, object with records/jobs/slices, or an array")


def result_records(raw: Any) -> list[dict[str, Any]] | None:
    if isinstance(raw, list):
        if all(isinstance(item, dict) for item in raw):
            return raw
        return None
    if not isinstance(raw, dict):
        return None
    for key in ("records", "jobs", "slices", "work_units"):
        value = raw.get(key)
        if isinstance(value, list) and all(isinstance(item, dict) for item in value):
            return value
    return None


def metrics_from_summary(raw: dict[str, Any], label: str, path: Path, expected_slice_ids: tuple[str, ...]) -> ArmMetrics:
    completed_slice_ids = completed_slice_ids_from_summary(raw)
    metrics = ArmMetrics(
        label=label,
        source=str(path),
        expected_slices=len(expected_slice_ids),
        expected_slice_ids=expected_slice_ids,
    )
    metrics.merged_slices = int_or_none(pick_number(raw, "merged_slices", "merged", "done", "completed_slices"))
    if completed_slice_ids is not None:
        metrics.merged_slices = len(set(completed_slice_ids) & set(expected_slice_ids))
    metrics.wall_clock_seconds = duration_seconds(raw, "wall_clock")
    metrics.effective_concurrency = pick_number(raw, "effective_concurrency")
    metrics.tokens_consumed = pick_number(raw, "tokens_consumed", "tokens", "total_tokens")
    metrics.bounce_rounds = pick_number(raw, "bounce_rounds", "bounces", "bounce_count")
    metrics.review_rounds = pick_number(raw, "review_rounds")
    metrics.human_interventions = pick_number(raw, "human_interventions", "manual_interventions", "manual_approvals")
    metrics.escaped_defects = pick_number(raw, "escaped_defects")
    metrics.failed_required_gates = pick_number(raw, "failed_required_gates", "failed_quality_checks")
    attach_quality(metrics, raw)
    attach_slice_coverage(metrics, completed_slice_ids)
    return metrics


def metrics_from_records(records: list[dict[str, Any]], label: str, path: Path, expected_slice_ids: tuple[str, ...]) -> ArmMetrics:
    metrics = ArmMetrics(
        label=label,
        source=str(path),
        expected_slices=len(expected_slice_ids),
        expected_slice_ids=expected_slice_ids,
    )
    starts: list[datetime] = []
    ends: list[datetime] = []
    work_seconds = 0.0
    tokens = 0.0
    bounces = 0.0
    reviews = 0.0
    human = 0.0
    escaped = 0.0
    failed_required = 0.0
    completed_slice_ids: list[str] = []
    completed_records_missing_id = 0
    quality_payloads: list[dict[str, Any]] = []
    for record in records:
        status = str(record.get("status") or "").lower()
        has_pr = bool(record.get("pr") or record.get("pr_url"))
        if status in {"done", "merged", "passed", "complete", "completed"} or has_pr:
            slice_id = record_slice_id(record)
            if slice_id:
                completed_slice_ids.append(slice_id)
            else:
                completed_records_missing_id += 1
        for key in ("created_at", "started_at", "dispatched_at", "start"):
            ts = parse_timestamp(record.get(key))
            if ts is not None:
                starts.append(ts)
                break
        for key in ("merged_at", "finalized_at", "finished_at", "completed_at", "end"):
            ts = parse_timestamp(record.get(key))
            if ts is not None:
                ends.append(ts)
                break
        tokens += pick_number(record, "tokens_consumed", "tokens", "total_tokens") or usage_tokens(record)
        bounces += pick_number(record, "bounce_rounds", "bounce_count", "bounces") or 0
        reviews += pick_number(record, "review_rounds") or 0
        human += pick_number(record, "human_interventions", "manual_interventions", "manual_approvals") or 0
        escaped += pick_number(record, "escaped_defects") or backlink_count(record)
        failed_required += pick_number(record, "failed_required_gates", "failed_quality_checks") or 0
        quality = record.get("quality")
        if isinstance(quality, dict):
            quality_payloads.append(quality)
        for unit in record.get("work_units") or []:
            if not isinstance(unit, dict):
                continue
            start = parse_timestamp(unit.get("started_at") or unit.get("start"))
            end = parse_timestamp(unit.get("finished_at") or unit.get("ended_at") or unit.get("end"))
            if start and end and end > start:
                work_seconds += (end - start).total_seconds()
    metrics.merged_slices = len(set(completed_slice_ids) & set(expected_slice_ids))
    metrics.tokens_consumed = tokens
    metrics.bounce_rounds = bounces
    metrics.review_rounds = reviews or None
    metrics.human_interventions = human
    metrics.escaped_defects = escaped
    metrics.failed_required_gates = failed_required
    if starts and ends and max(ends) > min(starts):
        metrics.wall_clock_seconds = (max(ends) - min(starts)).total_seconds()
    if metrics.wall_clock_seconds and work_seconds > 0:
        metrics.effective_concurrency = round(work_seconds / metrics.wall_clock_seconds, 2)
    attach_quality(metrics, {"quality": combine_quality(quality_payloads)})
    attach_slice_coverage(metrics, completed_slice_ids, missing_id_records=completed_records_missing_id)
    return metrics


def attach_quality(metrics: ArmMetrics, raw: dict[str, Any]) -> None:
    quality = raw.get("quality") if isinstance(raw.get("quality"), dict) else raw
    status = str(quality.get("status") or quality.get("quality_status") or "").lower()
    required_gates_passed = quality.get("required_gates_passed")
    if status in {"pass", "passed", "ok", "green"}:
        metrics.quality_pass = True
    elif status in {"fail", "failed", "red"}:
        metrics.quality_pass = False
        metrics.quality_notes.append("quality status is fail")
    if required_gates_passed is True and metrics.quality_pass is None:
        metrics.quality_pass = True
    if required_gates_passed is False:
        metrics.quality_pass = False
        metrics.quality_notes.append("required gates did not pass")
    if metrics.escaped_defects and metrics.escaped_defects > 0:
        metrics.quality_pass = False
        metrics.quality_notes.append(f"escaped defects: {format_number(metrics.escaped_defects)}")
    if metrics.failed_required_gates and metrics.failed_required_gates > 0:
        metrics.quality_pass = False
        metrics.quality_notes.append(f"failed required gates: {format_number(metrics.failed_required_gates)}")
    if metrics.merged_slices is not None and metrics.merged_slices < metrics.expected_slices:
        metrics.quality_pass = False
        metrics.quality_notes.append(f"merged {metrics.merged_slices}/{metrics.expected_slices} planned slices")
    if metrics.quality_pass is None:
        metrics.quality_notes.append("quality floor unknown; provide quality.status or required_gates_passed")


def attach_slice_coverage(
    metrics: ArmMetrics,
    completed_slice_ids: list[str] | None,
    *,
    missing_id_records: int = 0,
) -> None:
    expected = set(metrics.expected_slice_ids)
    if not expected:
        return
    if completed_slice_ids is None:
        metrics.quality_pass = False
        metrics.quality_notes.append("completed slice ids missing; cannot prove planned slice coverage")
        return

    counts = Counter(completed_slice_ids)
    missing_id_records += counts.pop("", 0)
    completed = set(counts)
    missing = sorted(expected - completed, key=str.lower)
    unknown = sorted(completed - expected, key=str.lower)
    duplicated = sorted((slice_id for slice_id, count in counts.items() if count > 1), key=str.lower)

    if missing_id_records:
        metrics.quality_pass = False
        metrics.quality_notes.append(f"completed records without slice id: {missing_id_records}")
    if missing:
        metrics.quality_pass = False
        metrics.quality_notes.append("missing completed slices: " + ", ".join(missing))
    if duplicated:
        metrics.quality_pass = False
        metrics.quality_notes.append("duplicate completed slices: " + ", ".join(duplicated))
    if unknown:
        metrics.quality_pass = False
        metrics.quality_notes.append("unknown completed slices: " + ", ".join(unknown))


def combine_quality(payloads: list[dict[str, Any]]) -> dict[str, Any]:
    if not payloads:
        return {}
    required = [payload.get("required_gates_passed") for payload in payloads if "required_gates_passed" in payload]
    statuses = [str(payload.get("status") or "").lower() for payload in payloads]
    return {
        "required_gates_passed": all(required) if required else None,
        "status": "fail" if any(status in {"fail", "failed", "red"} for status in statuses) else "",
    }


def render_report(
    *,
    backlog: Backlog,
    baseline: Arm,
    candidate: Arm,
    baseline_metrics: ArmMetrics | None,
    candidate_metrics: ArmMetrics | None,
    generated_at: datetime,
) -> str:
    lines: list[str] = []
    lines.append(f"# A/B Experiment Harness Report: {backlog.name}")
    lines.append("")
    lines.append(f"- Generated: {generated_at.isoformat(timespec='seconds')}")
    lines.append(f"- Canonical work items: {len(backlog.items)}")
    lines.append(f"- Difficulty points: {format_number(backlog.total_difficulty)}")
    lines.append(f"- Same canonical work: yes")
    if backlog.hypothesis:
        lines.append(f"- Hypothesis: {backlog.hypothesis}")
    lines.append("")
    lines.append("## Inputs")
    lines.append("")
    lines.append("| Arm | Topology | Planned slices | Result source |")
    lines.append("| --- | --- | ---: | --- |")
    for arm, metrics in ((baseline, baseline_metrics), (candidate, candidate_metrics)):
        source = metrics.source if metrics else "dry-run only"
        lines.append(f"| {arm.label} | `{arm.topology_path}` | {len(arm.slices)} | {source} |")
    lines.append("")
    lines.append("## Topology Summary")
    lines.append("")
    lines.append(
        "| Arm | Instances | Ephemeral instances | Ephemeral capacity | Workers | Reviewers | Verifiers | Verifier before review |"
    )
    lines.append("| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |")
    for arm in (baseline, candidate):
        t = arm.topology
        lines.append(
            f"| {arm.label} | {t.instance_count} | {t.ephemeral_instances} | {t.ephemeral_capacity} | "
            f"{t.worker_capacity} | {t.reviewer_capacity} | {t.verifier_capacity} | "
            f"{yes_no(t.verifier_before_review)} |"
        )
    lines.append("")
    lines.append("### Pipeline Steps")
    lines.append("")
    for arm in (baseline, candidate):
        lines.append(f"- {arm.label}:")
        if arm.topology.pipeline_steps:
            for name, steps in sorted(arm.topology.pipeline_steps.items()):
                rendered_steps = " -> ".join(steps) if steps else "(no steps)"
                lines.append(f"  - `{name}`: {rendered_steps}")
        else:
            lines.append("  - no pipelines declared")
    lines.append("")
    lines.append("## Backlog Slicing")
    lines.append("")
    lines.extend(render_waves(baseline.label, baseline.slices))
    lines.append("")
    lines.extend(render_waves(candidate.label, candidate.slices))
    lines.append("")
    lines.append("## Dry-Run Runbook")
    lines.append("")
    lines.append("1. Freeze the backlog JSON and both topology files in the PR or experiment artifact.")
    lines.append("2. Create isolated, clean repos or job stores for each arm; do not reuse daemon state between arms.")
    lines.append("3. Install the arm topology, then dispatch the planned slices wave by wave. Slices in the same wave may run concurrently.")
    lines.append("4. Capture every job outcome, gate result, usage record, review bounce, manual approval, and merge timestamp.")
    lines.append("5. Export per-arm result JSON and rerun this harness with `--baseline-results` and `--candidate-results`.")
    lines.append("6. Treat speed/cost wins as invalid unless the delivery-quality floor passes.")
    lines.append("")
    lines.append("## Result Comparison")
    lines.append("")
    if baseline_metrics is None or candidate_metrics is None:
        lines.append("No complete pair of result files was supplied, so this is a dry-run report.")
        lines.append("The table below is emitted after both arm result files are available.")
    else:
        lines.extend(render_metrics(backlog.total_difficulty, baseline_metrics, candidate_metrics))
    lines.append("")
    lines.append("## Quality Firewall")
    lines.append("")
    lines.append(
        "These metrics are for observers: org-review, humans, and experiment reports. "
        "Do not put per-arm scores into worker or reviewer prompts. Turn findings into reviewed "
        "changes to topology, slicing policy, prompts, or gates instead."
    )
    lines.append("")
    return "\n".join(lines)


def render_waves(label: str, slices: list[WorkSlice]) -> list[str]:
    lines = [f"### {label}", "", "| Wave | Slices | Difficulty |", "| ---: | --- | ---: |"]
    for index, wave in enumerate(dependency_waves(slices, label=label), start=1):
        ids = ", ".join(f"`{work_slice.id}`" for work_slice in wave)
        difficulty = sum(work_slice.difficulty for work_slice in wave)
        lines.append(f"| {index} | {ids} | {format_number(difficulty)} |")
    return lines


def render_metrics(total_difficulty: float, baseline: ArmMetrics, candidate: ArmMetrics) -> list[str]:
    rows = [
        "| Metric | Baseline | Candidate | Candidate delta |",
        "| --- | ---: | ---: | ---: |",
    ]
    metric_defs = [
        ("Quality floor", quality_label, None),
        ("Effective concurrency", lambda m: m.effective_concurrency, None),
        ("Wall-clock minutes / difficulty point", lambda m: normalized(m.wall_clock_seconds, total_difficulty, scale=60), "lower"),
        ("Tokens / merged slice", tokens_per_merged_slice, "lower"),
        ("Tokens / difficulty point", lambda m: normalized(m.tokens_consumed, total_difficulty), "lower"),
        ("Bounce rounds / difficulty point", lambda m: normalized(m.bounce_rounds, total_difficulty), "lower"),
        ("Human interventions / difficulty point", lambda m: normalized(m.human_interventions, total_difficulty), "lower"),
    ]
    for name, getter, _direction in metric_defs:
        base_value = getter(baseline)
        candidate_value = getter(candidate)
        delta = numeric_delta(base_value, candidate_value)
        rows.append(f"| {name} | {format_cell(base_value)} | {format_cell(candidate_value)} | {format_cell(delta, signed=True)} |")
    rows.append("")
    rows.append("### Interpretation")
    rows.append("")
    if candidate.quality_pass is False:
        rows.append("- Candidate cannot be considered a win because its quality floor failed.")
    elif baseline.quality_pass is False and candidate.quality_pass is True:
        rows.append("- Candidate is the only arm with a passing quality floor; inspect speed and cost as secondary evidence.")
    else:
        rows.append("- Compare normalized speed and cost only after confirming both quality floors are acceptable.")
    for metrics in (baseline, candidate):
        if metrics.quality_notes:
            rows.append(f"- {metrics.label} quality notes: {'; '.join(metrics.quality_notes)}")
    return rows


def summary_json(
    backlog: Backlog,
    baseline: Arm,
    candidate: Arm,
    baseline_metrics: ArmMetrics | None,
    candidate_metrics: ArmMetrics | None,
    generated_at: datetime,
) -> dict[str, Any]:
    return {
        "generated_at": generated_at.isoformat(timespec="seconds"),
        "backlog": {
            "name": backlog.name,
            "items": len(backlog.items),
            "difficulty_points": backlog.total_difficulty,
        },
        "arms": {
            baseline.label: arm_json(baseline, baseline_metrics, backlog.total_difficulty),
            candidate.label: arm_json(candidate, candidate_metrics, backlog.total_difficulty),
        },
    }


def arm_json(arm: Arm, metrics: ArmMetrics | None, total_difficulty: float) -> dict[str, Any]:
    out: dict[str, Any] = {
        "topology": {
            "path": str(arm.topology_path),
            "instance_count": arm.topology.instance_count,
            "ephemeral_capacity": arm.topology.ephemeral_capacity,
            "worker_capacity": arm.topology.worker_capacity,
            "reviewer_capacity": arm.topology.reviewer_capacity,
            "verifier_capacity": arm.topology.verifier_capacity,
            "verifier_before_review": arm.topology.verifier_before_review,
        },
        "planned_slices": len(arm.slices),
        "planned_waves": [[work_slice.id for work_slice in wave] for wave in dependency_waves(arm.slices, label=arm.label)],
    }
    if metrics:
        out["metrics"] = {
            "quality_pass": metrics.quality_pass,
            "effective_concurrency": metrics.effective_concurrency,
            "wall_clock_minutes_per_difficulty": normalized(metrics.wall_clock_seconds, total_difficulty, scale=60),
            "tokens_per_merged_slice": tokens_per_merged_slice(metrics),
            "tokens_per_difficulty": normalized(metrics.tokens_consumed, total_difficulty),
            "bounce_rounds_per_difficulty": normalized(metrics.bounce_rounds, total_difficulty),
            "human_interventions_per_difficulty": normalized(metrics.human_interventions, total_difficulty),
        }
    return out


def dependency_waves(slices: list[WorkSlice], *, label: str) -> list[list[WorkSlice]]:
    by_id = {work_slice.id: work_slice for work_slice in slices}
    if len(by_id) != len(slices):
        raise HarnessError(f"{label}: duplicate slice ids")
    remaining = set(by_id)
    done: set[str] = set()
    waves: list[list[WorkSlice]] = []
    while remaining:
        ready = sorted(
            [slice_id for slice_id in remaining if set(by_id[slice_id].depends_on) <= done],
            key=str.lower,
        )
        if not ready:
            blocked = ", ".join(sorted(remaining))
            raise HarnessError(f"{label}: dependency cycle or unknown dependency among {blocked}")
        waves.append([by_id[slice_id] for slice_id in ready])
        done.update(ready)
        remaining.difference_update(ready)
    return waves


def ordered_by_waves(slices: list[WorkSlice], *, label: str) -> list[WorkSlice]:
    return [work_slice for wave in dependency_waves(slices, label=label) for work_slice in wave]


def load_json(path: Path) -> Any:
    if not path.exists():
        raise HarnessError(f"{path}: file not found")
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError as exc:
        raise HarnessError(f"{path}: invalid JSON: {exc}") from exc


def write_text(path: Path | None, text: str) -> None:
    if path is None:
        print(text, end="")
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def clean_id(value: Any) -> str:
    return str(value or "").strip()


def as_list(value: Any) -> list[Any]:
    if value is None:
        return []
    if isinstance(value, list):
        return value
    return [value]


def difficulty_value(raw: dict[str, Any], context: str) -> float:
    value = raw.get("difficulty_points", raw.get("difficulty", raw.get("points", 1)))
    if isinstance(value, str):
        key = value.strip().lower()
        if key in DIFFICULTY_POINTS:
            return DIFFICULTY_POINTS[key]
        try:
            return float(key)
        except ValueError as exc:
            raise HarnessError(f"{context}: unsupported difficulty {value!r}") from exc
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        if value <= 0:
            raise HarnessError(f"{context}: difficulty must be positive")
        return float(value)
    raise HarnessError(f"{context}: difficulty must be a number or size string")


def pick_number(raw: dict[str, Any], *keys: str) -> float | None:
    for key in keys:
        value = raw.get(key)
        if isinstance(value, (int, float)) and not isinstance(value, bool):
            return float(value)
        if isinstance(value, str) and value.strip():
            try:
                return float(value.replace("_", ""))
            except ValueError:
                continue
    return None


def int_or_none(value: float | None) -> int | None:
    if value is None:
        return None
    return int(value)


def completed_slice_ids_from_summary(raw: dict[str, Any]) -> list[str] | None:
    for key in ("completed_slice_ids", "merged_slice_ids"):
        value = raw.get(key)
        if isinstance(value, list):
            return clean_ids(value)
    for key in ("completed_slices", "merged_slices"):
        value = raw.get(key)
        if isinstance(value, list):
            return clean_ids(value)
    return None


def clean_ids(values: list[Any]) -> list[str]:
    return [clean_id(value) for value in values]


def record_slice_id(record: dict[str, Any]) -> str:
    for key in ("id", "slice_id", "work_slice_id"):
        value = clean_id(record.get(key))
        if value:
            return value
    return ""


def duration_seconds(raw: dict[str, Any], prefix: str) -> float | None:
    if (value := pick_number(raw, f"{prefix}_seconds", f"{prefix}_sec")) is not None:
        return value
    if (value := pick_number(raw, f"{prefix}_minutes", f"{prefix}_min")) is not None:
        return value * 60
    if (value := pick_number(raw, f"{prefix}_ms", f"{prefix}_milliseconds")) is not None:
        return value / 1000
    return None


def usage_tokens(record: dict[str, Any]) -> float:
    usage = record.get("usage")
    if not isinstance(usage, dict):
        return 0.0
    return (pick_number(usage, "input_tokens") or 0) + (pick_number(usage, "output_tokens") or 0)


def backlink_count(record: dict[str, Any]) -> float:
    backlinks = record.get("post_merge_defect_backlinks")
    if isinstance(backlinks, list):
        return float(len(backlinks))
    return 0.0


def parse_timestamp(value: Any) -> datetime | None:
    if not isinstance(value, str) or not value.strip():
        return None
    text = value.strip()
    if text.endswith("Z"):
        text = text[:-1] + "+00:00"
    try:
        ts = datetime.fromisoformat(text)
    except ValueError:
        return None
    if ts.tzinfo is None:
        return ts.replace(tzinfo=timezone.utc)
    return ts.astimezone(timezone.utc)


def normalized(value: float | None, total_difficulty: float, *, scale: float = 1) -> float | None:
    if value is None or total_difficulty <= 0:
        return None
    return round((value / scale) / total_difficulty, 2)


def tokens_per_merged_slice(metrics: ArmMetrics) -> float | None:
    if metrics.tokens_consumed is None or not metrics.merged_slices:
        return None
    return round(metrics.tokens_consumed / metrics.merged_slices, 2)


def quality_label(metrics: ArmMetrics) -> str:
    if metrics.quality_pass is True:
        return "PASS"
    if metrics.quality_pass is False:
        return "FAIL"
    return "UNKNOWN"


def numeric_delta(base_value: Any, candidate_value: Any) -> float | None:
    if isinstance(base_value, (int, float)) and isinstance(candidate_value, (int, float)):
        if math.isnan(base_value) or math.isnan(candidate_value):
            return None
        return round(float(candidate_value) - float(base_value), 2)
    return None


def format_cell(value: Any, *, signed: bool = False) -> str:
    if value is None:
        return "n/a"
    if isinstance(value, str):
        return value
    if isinstance(value, bool):
        return yes_no(value)
    if isinstance(value, (int, float)):
        if signed and value > 0:
            return f"+{format_number(value)}"
        return format_number(value)
    return str(value)


def format_number(value: float | int) -> str:
    if isinstance(value, float) and value.is_integer():
        value = int(value)
    if isinstance(value, int):
        return str(value)
    return f"{value:.2f}".rstrip("0").rstrip(".")


def yes_no(value: bool) -> str:
    return "yes" if value else "no"


if __name__ == "__main__":
    sys.exit(main())

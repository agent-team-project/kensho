#!/usr/bin/env python3
"""Concurrency-safe Discord webhook delivery with a rolling 24-hour ceiling."""

from __future__ import annotations

import argparse
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import tomllib
from typing import Callable, TextIO
import urllib.error
import urllib.parse
import urllib.request
import uuid


SCHEMA = "agent-team.discord-delivery.v1"
WINDOW = timedelta(hours=24)
MAX_CONTENT_LENGTH = 2_000
KIND_PRIORITY = {"release": 100, "manual": 50, "digest": 10}
EXIT_DELIVERED = 0
EXIT_FAILED = 1
EXIT_UNAVAILABLE = 2
EXIT_DEFERRED = 3


@dataclass(frozen=True)
class DeliveryRequest:
    delivery_id: str
    kind: str
    content: str


@dataclass(frozen=True)
class PostResult:
    success: bool
    status: int | None = None
    message_id: str = ""
    detail: str = ""
    definitive: bool = True
    completed_at: datetime | None = None


class BookkeepingInterrupted(RuntimeError):
    """Test hook: the HTTP receipt landed but the aggregate state did not."""


class DeliveryGate:
    def __init__(self, state_dir: Path, window: timedelta = WINDOW) -> None:
        self.state_dir = state_dir
        self.window = window
        self.state_path = state_dir / "state.json"
        self.pending_path = state_dir / "pending.json"
        self.pending_markdown_path = state_dir / "pending.md"
        self.notification_path = state_dir / "supervisor-notifications.jsonl"
        self.attempts_dir = state_dir / "attempts"
        self.lock_path = state_dir / "delivery.lock"
        self.state_dir.mkdir(parents=True, exist_ok=True)
        self.attempts_dir.mkdir(parents=True, exist_ok=True)
        try:
            self.state_dir.chmod(0o700)
            self.attempts_dir.chmod(0o700)
        except OSError:
            pass

    def deliver(
        self,
        request: DeliveryRequest,
        sender: Callable[[str], PostResult],
        *,
        now: datetime | None = None,
        interrupt_after_receipt: bool = False,
    ) -> dict[str, object]:
        current = normalize_time(now or datetime.now(timezone.utc))
        with self._locked():
            state = self._load_state()
            pending = self._load_pending()
            self._recover(state, pending, current)
            self._drop_delivered_pending(state, pending)

            existing = state["deliveries"].get(request.delivery_id, {})
            if existing.get("status") == "delivered":
                self._save(state, pending)
                return self._result(
                    "duplicate",
                    request.delivery_id,
                    current,
                    state,
                    included_delivery_ids=[request.delivery_id],
                )

            self._queue_request(state, pending, request, current)
            self._checkpoint_queued_request(state, pending)
            eligible_at = self._eligible_at(state)
            if eligible_at is not None and current < eligible_at:
                self._record_notification(
                    request,
                    current,
                    "rolling 24-hour ceiling",
                    eligible_at=eligible_at,
                )
                self._save(state, pending)
                return self._result("deferred", request.delivery_id, current, state)

            selected, content = self._select_pending(state, pending)
            if not selected:
                self._record_notification(request, current, "pending content exceeds Discord's 2000-character limit")
                self._save(state, pending)
                return self._result("failed", request.delivery_id, current, state, error="content_too_long")

            attempt_id = uuid.uuid4().hex
            included_ids = [str(item["delivery_id"]) for item in selected]
            attempt = {
                "schema": SCHEMA,
                "attempt_id": attempt_id,
                "stage": "reserved",
                "delivery_id": included_ids[0],
                "included_delivery_ids": included_ids,
                "kind": str(selected[0]["kind"]),
                "attempted_at": format_time(current),
                "content_sha256": content_hash(content),
            }
            self._write_attempt(attempt)
            for delivery_id in included_ids:
                state["deliveries"][delivery_id]["status"] = "attempting"
                state["deliveries"][delivery_id]["attempt_id"] = attempt_id
                state["deliveries"][delivery_id]["updated_at"] = format_time(current)
            self._save(state, pending)

            try:
                post = sender(content)
            except Exception as exc:  # noqa: BLE001 - delivery errors become durable state
                post = PostResult(False, detail=f"sender exception: {type(exc).__name__}", definitive=False)

            completed = normalize_time(post.completed_at or current)

            if post.success:
                attempt.update(
                    stage="succeeded",
                    completed_at=format_time(completed),
                    http_status=post.status,
                    message_id=post.message_id,
                )
                # This durable receipt is the first local action after a confirmed 2xx.
                # Recovery applies it if the process dies before aggregate bookkeeping.
                self._write_attempt(attempt)
                if interrupt_after_receipt:
                    raise BookkeepingInterrupted(attempt_id)
                self._apply_success(state, pending, attempt)
                self._save(state, pending)
                return self._result(
                    "delivered",
                    request.delivery_id,
                    completed,
                    state,
                    included_delivery_ids=included_ids,
                    http_status=post.status,
                )

            if post.definitive:
                attempt.update(
                    stage="failed",
                    completed_at=format_time(completed),
                    http_status=post.status,
                    detail=post.detail,
                )
                self._write_attempt(attempt)
                for delivery_id in included_ids:
                    record = state["deliveries"][delivery_id]
                    record["status"] = "pending"
                    record["last_failure_at"] = format_time(completed)
                    record["last_failure"] = post.detail or f"HTTP {post.status}"
                    record["updated_at"] = format_time(completed)
                self._record_notification(request, completed, "Discord delivery failed and remains retryable")
                self._save(state, pending)
                return self._result(
                    "failed",
                    request.delivery_id,
                    completed,
                    state,
                    error=post.detail or f"HTTP {post.status}",
                    http_status=post.status,
                )

            # A lost/unknown response cannot be retried immediately without risking a
            # second successful post. Preserve catch-up state, but hold the allowance.
            hold_until = completed + self.window
            attempt.update(
                stage="uncertain",
                completed_at=format_time(completed),
                detail=post.detail,
                hold_until=format_time(hold_until),
            )
            self._write_attempt(attempt)
            self._set_uncertain_hold(state, attempt)
            for delivery_id in included_ids:
                record = state["deliveries"][delivery_id]
                record["status"] = "pending"
                record["updated_at"] = format_time(completed)
            self._record_notification(
                request,
                completed,
                "Discord response was ambiguous; strict ceiling held conservatively",
                eligible_at=hold_until,
            )
            self._save(state, pending)
            return self._result("uncertain", request.delivery_id, completed, state, error=post.detail)

    def defer_unavailable(
        self,
        request: DeliveryRequest,
        reason: str,
        *,
        now: datetime | None = None,
    ) -> dict[str, object]:
        current = normalize_time(now or datetime.now(timezone.utc))
        with self._locked():
            state = self._load_state()
            pending = self._load_pending()
            self._recover(state, pending, current)
            existing = state["deliveries"].get(request.delivery_id, {})
            if existing.get("status") == "delivered":
                self._save(state, pending)
                return self._result("duplicate", request.delivery_id, current, state)
            self._queue_request(state, pending, request, current)
            self._checkpoint_queued_request(state, pending)
            self._record_notification(request, current, reason)
            self._save(state, pending)
            return self._result("unavailable", request.delivery_id, current, state, error=reason)

    def status(self, *, now: datetime | None = None) -> dict[str, object]:
        current = normalize_time(now or datetime.now(timezone.utc))
        with self._locked():
            state = self._load_state()
            pending = self._load_pending()
            self._recover(state, pending, current)
            self._drop_delivered_pending(state, pending)
            self._save(state, pending)
            eligible_at = self._eligible_at(state)
            return {
                "schema": SCHEMA,
                "last_success": state.get("last_success"),
                "eligible": eligible_at is None or current >= eligible_at,
                "eligible_at": format_time(eligible_at) if eligible_at else None,
                "pending": [
                    {
                        "delivery_id": item["delivery_id"],
                        "kind": item["kind"],
                        "created_at": item["created_at"],
                    }
                    for item in self._sorted_pending(pending)
                ],
            }

    def _locked(self):
        return FileLock(self.lock_path)

    def _load_state(self) -> dict[str, object]:
        value = load_json(self.state_path, {})
        if not isinstance(value, dict) or value.get("schema") not in (None, SCHEMA):
            raise RuntimeError(f"unsupported Discord delivery state in {self.state_path}")
        value.setdefault("schema", SCHEMA)
        value.setdefault("window_seconds", int(self.window.total_seconds()))
        value.setdefault("last_success", None)
        value.setdefault("uncertain_hold", None)
        value.setdefault("deliveries", {})
        if not isinstance(value["deliveries"], dict):
            raise RuntimeError(f"invalid deliveries ledger in {self.state_path}")
        return value

    def _load_pending(self) -> dict[str, object]:
        value = load_json(self.pending_path, {})
        if not isinstance(value, dict) or value.get("schema") not in (None, SCHEMA):
            raise RuntimeError(f"unsupported Discord pending queue in {self.pending_path}")
        value.setdefault("schema", SCHEMA)
        value.setdefault("items", [])
        if not isinstance(value["items"], list):
            raise RuntimeError(f"invalid pending queue in {self.pending_path}")
        return value

    def _save(self, state: dict[str, object], pending: dict[str, object]) -> None:
        # State first: a crash can leave delivered content in pending, but selection
        # filters it through the authoritative ledger and cannot repost it.
        atomic_write_json(self.state_path, state)
        atomic_write_json(self.pending_path, pending)
        self._write_pending_markdown(pending)

    def _checkpoint_queued_request(
        self,
        state: dict[str, object],
        pending: dict[str, object],
    ) -> None:
        # New content cannot be reconstructed from the aggregate ledger, so make
        # the pending body durable before publishing a notice or reservation.
        # Recovery can rebuild a ledger record if the process dies between files.
        atomic_write_json(self.pending_path, pending)
        atomic_write_json(self.state_path, state)
        self._write_pending_markdown(pending)

    def _queue_request(
        self,
        state: dict[str, object],
        pending: dict[str, object],
        request: DeliveryRequest,
        now: datetime,
    ) -> None:
        timestamp = format_time(now)
        items = pending["items"]
        assert isinstance(items, list)
        current = next((item for item in items if item.get("delivery_id") == request.delivery_id), None)
        if current is None:
            current = {
                "delivery_id": request.delivery_id,
                "kind": request.kind,
                "priority": KIND_PRIORITY[request.kind],
                "content": request.content.strip(),
                "created_at": timestamp,
                "updated_at": timestamp,
            }
            items.append(current)
        else:
            current["kind"] = request.kind
            current["priority"] = max(int(current.get("priority", 0)), KIND_PRIORITY[request.kind])
            existing_content = str(current.get("content", "")).strip()
            proposed_content = request.content.strip()
            if (
                proposed_content
                and len(existing_content) > MAX_CONTENT_LENGTH
                and len(proposed_content) <= MAX_CONTENT_LENGTH
            ):
                # A retry with the same stable ID may correct an invalid body.
                # Keeping the oversized version would otherwise poison the queue.
                current["content"] = proposed_content
            else:
                current["content"] = merge_text(existing_content, proposed_content)
            current["updated_at"] = timestamp

        deliveries = state["deliveries"]
        assert isinstance(deliveries, dict)
        record = deliveries.setdefault(
            request.delivery_id,
            {
                "delivery_id": request.delivery_id,
                "first_seen_at": timestamp,
            },
        )
        record.update(
            kind=request.kind,
            priority=KIND_PRIORITY[request.kind],
            status="pending",
            content_sha256=content_hash(str(current["content"])),
            updated_at=timestamp,
        )

    def _select_pending(
        self,
        state: dict[str, object],
        pending: dict[str, object],
    ) -> tuple[list[dict[str, object]], str]:
        candidates = [
            item
            for item in self._sorted_pending(pending)
            if state["deliveries"].get(item.get("delivery_id"), {}).get("status") != "delivered"
        ]
        if not candidates:
            return [], ""

        selected: list[dict[str, object]] = []
        combined = ""
        for item in candidates:
            content = str(item.get("content", "")).strip()
            if not content or len(content) > MAX_CONTENT_LENGTH:
                continue
            proposed = merge_text(combined, content)
            if len(proposed) <= MAX_CONTENT_LENGTH:
                selected.append(item)
                combined = proposed
        return selected, combined

    def _sorted_pending(self, pending: dict[str, object]) -> list[dict[str, object]]:
        items = pending["items"]
        assert isinstance(items, list)
        return sorted(
            (item for item in items if isinstance(item, dict)),
            key=lambda item: (
                -int(item.get("priority", 0)),
                str(item.get("created_at", "")),
                str(item.get("delivery_id", "")),
            ),
        )

    def _recover(
        self,
        state: dict[str, object],
        pending: dict[str, object],
        now: datetime,
    ) -> None:
        changed = self._restore_pending_deliveries(state, pending)
        for path in sorted(self.attempts_dir.glob("*.json")):
            attempt = load_json(path, {})
            if not isinstance(attempt, dict) or attempt.get("schema") != SCHEMA:
                continue
            stage = attempt.get("stage")
            if stage == "succeeded":
                self._apply_success(state, pending, attempt)
                changed = True
            elif stage == "reserved":
                attempt["stage"] = "uncertain"
                attempt["completed_at"] = format_time(now)
                attempt["detail"] = "process ended without a definitive HTTP receipt"
                # Start the conservative window at recovery, which cannot be
                # earlier than an unrecorded success from the orphaned process.
                attempt["hold_until"] = format_time(now + self.window)
                self._write_attempt(attempt)
                self._set_uncertain_hold(state, attempt)
                changed = True
            elif stage == "uncertain":
                self._set_uncertain_hold(state, attempt)
                changed = True
        if changed:
            self._drop_delivered_pending(state, pending)

    def _restore_pending_deliveries(
        self,
        state: dict[str, object],
        pending: dict[str, object],
    ) -> bool:
        deliveries = state["deliveries"]
        items = pending["items"]
        assert isinstance(deliveries, dict)
        assert isinstance(items, list)
        changed = False
        for item in items:
            if not isinstance(item, dict):
                continue
            delivery_id = str(item.get("delivery_id", "")).strip()
            if not delivery_id or delivery_id in deliveries:
                continue
            created_at = str(item.get("created_at", item.get("updated_at", "")))
            content = str(item.get("content", ""))
            deliveries[delivery_id] = {
                "delivery_id": delivery_id,
                "first_seen_at": created_at,
                "kind": str(item.get("kind", "digest")),
                "priority": int(item.get("priority", 0)),
                "status": "pending",
                "content_sha256": content_hash(content),
                "updated_at": str(item.get("updated_at", created_at)),
            }
            changed = True
        return changed

    def _apply_success(
        self,
        state: dict[str, object],
        pending: dict[str, object],
        attempt: dict[str, object],
    ) -> None:
        completed_at = str(attempt.get("completed_at") or attempt["attempted_at"])
        included = [str(value) for value in attempt.get("included_delivery_ids", [])]
        deliveries = state["deliveries"]
        assert isinstance(deliveries, dict)
        for delivery_id in included:
            record = deliveries.setdefault(delivery_id, {"delivery_id": delivery_id})
            record.update(
                status="delivered",
                delivered_at=completed_at,
                attempt_id=attempt["attempt_id"],
                updated_at=completed_at,
            )

        previous = state.get("last_success")
        if not isinstance(previous, dict) or parse_time(completed_at) >= parse_time(str(previous["timestamp"])):
            state["last_success"] = {
                "delivery_id": attempt["delivery_id"],
                "included_delivery_ids": included,
                "attempt_id": attempt["attempt_id"],
                "timestamp": completed_at,
                "content_sha256": attempt["content_sha256"],
                "message_id": attempt.get("message_id", ""),
            }
        self._drop_delivered_pending(state, pending)

    def _drop_delivered_pending(
        self,
        state: dict[str, object],
        pending: dict[str, object],
    ) -> None:
        deliveries = state["deliveries"]
        items = pending["items"]
        assert isinstance(deliveries, dict)
        assert isinstance(items, list)
        pending["items"] = [
            item
            for item in items
            if deliveries.get(item.get("delivery_id"), {}).get("status") != "delivered"
        ]

    def _set_uncertain_hold(self, state: dict[str, object], attempt: dict[str, object]) -> None:
        hold_until = str(attempt["hold_until"])
        current = state.get("uncertain_hold")
        if not isinstance(current, dict) or parse_time(hold_until) >= parse_time(str(current["until"])):
            state["uncertain_hold"] = {
                "delivery_id": attempt["delivery_id"],
                "attempt_id": attempt["attempt_id"],
                "until": hold_until,
                "reason": attempt.get("detail", "ambiguous delivery attempt"),
            }

    def _eligible_at(self, state: dict[str, object]) -> datetime | None:
        candidates: list[datetime] = []
        success = state.get("last_success")
        if isinstance(success, dict):
            candidates.append(parse_time(str(success["timestamp"])) + self.window)
        hold = state.get("uncertain_hold")
        if isinstance(hold, dict):
            candidates.append(parse_time(str(hold["until"])))
        return max(candidates) if candidates else None

    def _write_attempt(self, attempt: dict[str, object]) -> None:
        atomic_write_json(self.attempts_dir / f"{attempt['attempt_id']}.json", attempt)

    def _record_notification(
        self,
        request: DeliveryRequest,
        now: datetime,
        reason: str,
        *,
        eligible_at: datetime | None = None,
    ) -> None:
        entry = {
            "schema": SCHEMA,
            "timestamp": format_time(now),
            "delivery_id": request.delivery_id,
            "kind": request.kind,
            "reason": reason,
            "eligible_at": format_time(eligible_at) if eligible_at else None,
        }
        append_json_line(self.notification_path, entry)

    def _write_pending_markdown(self, pending: dict[str, object]) -> None:
        items = self._sorted_pending(pending)
        if not items:
            try:
                self.pending_markdown_path.unlink()
            except FileNotFoundError:
                pass
            return
        chunks = ["# Pending Discord material\n"]
        for item in items:
            chunks.append(
                f"## {item['kind']}: {item['delivery_id']}\n\n"
                f"Queued: {item['created_at']}\n\n{str(item['content']).strip()}\n"
            )
        atomic_write_text(self.pending_markdown_path, "\n".join(chunks))

    def _result(
        self,
        outcome: str,
        delivery_id: str,
        now: datetime,
        state: dict[str, object],
        **extra: object,
    ) -> dict[str, object]:
        eligible_at = self._eligible_at(state)
        result: dict[str, object] = {
            "schema": SCHEMA,
            "outcome": outcome,
            "delivery_id": delivery_id,
            "timestamp": format_time(now),
            "last_success": state.get("last_success"),
            "eligible_at": format_time(eligible_at) if eligible_at else None,
        }
        result.update(extra)
        return result


class FileLock:
    def __init__(self, path: Path) -> None:
        self.path = path
        self.handle: TextIO | None = None

    def __enter__(self) -> "FileLock":
        self.handle = self.path.open("a+", encoding="utf-8")
        fcntl.flock(self.handle.fileno(), fcntl.LOCK_EX)
        return self

    def __exit__(self, exc_type, exc, traceback) -> None:
        assert self.handle is not None
        fcntl.flock(self.handle.fileno(), fcntl.LOCK_UN)
        self.handle.close()


def post_webhook(webhook: str, content: str, *, test_time: datetime | None = None) -> PostResult:
    url = with_wait_query(webhook)
    payload = json.dumps({"content": content}).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=payload,
        headers={"Content-Type": "application/json", "User-Agent": "agent-team-discord-delivery/1"},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:  # noqa: S310 - configured webhook only
            body = response.read(65_536)
            status = response.status
    except urllib.error.HTTPError as exc:
        # Discord returned a definitive non-2xx response, so retry is safe.
        return PostResult(
            False,
            status=exc.code,
            detail=f"Discord webhook returned HTTP {exc.code}",
            completed_at=test_time or datetime.now(timezone.utc),
        )
    except (urllib.error.URLError, TimeoutError, OSError) as exc:
        # The request may have reached Discord even though its response did not.
        return PostResult(
            False,
            detail=f"Discord webhook response unavailable: {type(exc).__name__}",
            definitive=False,
            completed_at=test_time or datetime.now(timezone.utc),
        )

    if not 200 <= status < 300:
        return PostResult(
            False,
            status=status,
            detail=f"Discord webhook returned HTTP {status}",
            completed_at=test_time or datetime.now(timezone.utc),
        )
    message_id = ""
    if body:
        try:
            parsed = json.loads(body)
            if isinstance(parsed, dict):
                message_id = str(parsed.get("id", ""))
        except json.JSONDecodeError:
            pass
    return PostResult(
        True,
        status=status,
        message_id=message_id,
        completed_at=test_time or datetime.now(timezone.utc),
    )


def with_wait_query(url: str) -> str:
    parsed = urllib.parse.urlsplit(url)
    query = urllib.parse.parse_qsl(parsed.query, keep_blank_values=True)
    if not any(key == "wait" for key, _ in query):
        query.append(("wait", "true"))
    return urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))


def resolve_team_root() -> Path:
    configured = os.environ.get("AGENT_TEAM_ROOT", "").strip()
    if configured:
        return Path(configured).expanduser().resolve()
    try:
        output = subprocess.check_output(
            ["git", "worktree", "list", "--porcelain"],
            text=True,
            stderr=subprocess.DEVNULL,
        )
        first = next(line.removeprefix("worktree ") for line in output.splitlines() if line.startswith("worktree "))
        root = Path(first) / ".agent_team"
        if root.is_dir():
            return root.resolve()
    except (OSError, subprocess.SubprocessError, StopIteration):
        pass
    local = Path(".agent_team")
    if local.is_dir():
        return local.resolve()
    raise RuntimeError("AGENT_TEAM_ROOT is unset and no .agent_team directory was found")


def resolve_webhook(team_root: Path, env_file: str) -> str:
    key = "AGENT_TEAM_DISCORD_WEBHOOK"
    config_path = team_root / "config.toml"
    if config_path.is_file():
        try:
            config = tomllib.loads(config_path.read_text(encoding="utf-8"))
            configured = str(config.get("comms", {}).get("discord_webhook_env", "")).strip()
            if configured:
                key = configured
        except (OSError, tomllib.TOMLDecodeError):
            pass

    candidates = [Path(env_file)] if env_file else [Path.cwd() / ".env", team_root.parent / ".env"]
    for candidate in candidates:
        value = read_env_value(candidate, key)
        if value:
            return value
    raise LookupError(f"no Discord webhook found for {key} in .env")


def read_env_value(path: Path, name: str) -> str:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return ""
    for raw in lines:
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if line.startswith("export "):
            line = line[len("export ") :].lstrip()
        key, separator, value = line.partition("=")
        if not separator or key.strip() != name:
            continue
        value = value.strip()
        if len(value) >= 2 and value[0] == value[-1] and value[0] in ("'", '"'):
            value = value[1:-1]
        return value.strip()
    return ""


def notify_supervisor(result: dict[str, object]) -> None:
    if result.get("outcome") not in {"deferred", "failed", "uncertain", "unavailable"}:
        return
    if os.environ.get("AGENT_TEAM_COMMS_TESTING") == "1":
        return
    inbox = shutil.which("inbox")
    if not inbox:
        print("discord-webhook.sh: queued material; inbox command unavailable for supervisor notice", file=sys.stderr)
        return
    target = os.environ.get("AGENT_TEAM_SUPERVISOR", "manager")
    eligible = f"; next eligible {result['eligible_at']}" if result.get("eligible_at") else ""
    message = f"Discord {result['outcome']} for {result['delivery_id']}{eligible}; material is queued durably."
    try:
        completed = subprocess.run(
            [inbox, "send", target, message],
            text=True,
            capture_output=True,
            timeout=5,
            check=False,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        print(f"discord-webhook.sh: supervisor notification failed: {type(exc).__name__}", file=sys.stderr)
        return
    if completed.returncode != 0:
        print("discord-webhook.sh: supervisor notification failed; durable local notice retained", file=sys.stderr)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="discord-webhook.sh",
        description="Post through the shared rolling-24-hour Discord delivery gate.",
    )
    content = parser.add_mutually_exclusive_group()
    content.add_argument("--content")
    content.add_argument("--content-file")
    parser.add_argument("--delivery-id", help="Stable logical delivery ID; defaults to a content digest")
    parser.add_argument("--kind", choices=sorted(KIND_PRIORITY), default="manual")
    parser.add_argument("--env-file", default="")
    parser.add_argument("--status", action="store_true", help="Print canonical last-success and pending state")
    args = parser.parse_args(argv)
    if not args.status and args.content is None and args.content_file is None:
        parser.error("one of --content or --content-file is required")
    if args.status and (args.content is not None or args.content_file is not None):
        parser.error("--status cannot be combined with content")
    return args


def load_content(args: argparse.Namespace) -> str:
    if args.content_file:
        try:
            content = Path(args.content_file).read_text(encoding="utf-8")
        except OSError as exc:
            raise RuntimeError(f"content file unavailable: {exc.filename}") from exc
    else:
        content = args.content or ""
    content = content.strip()
    if not content:
        raise RuntimeError("content is empty")
    return content


def requested_time() -> datetime:
    injected = os.environ.get("AGENT_TEAM_COMMS_TEST_NOW", "")
    if not injected:
        return datetime.now(timezone.utc)
    if os.environ.get("AGENT_TEAM_COMMS_TESTING") != "1":
        raise RuntimeError("AGENT_TEAM_COMMS_TEST_NOW is restricted to test mode")
    return parse_time(injected)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv or sys.argv[1:])
    try:
        team_root = resolve_team_root()
        gate = DeliveryGate(team_root / "state" / "comms" / "discord-delivery")
        now = requested_time()
        if args.status:
            print(json.dumps(gate.status(now=now), sort_keys=True))
            return 0

        content = load_content(args)
        delivery_id = args.delivery_id or f"content-{content_hash(args.kind + chr(0) + content)[:32]}"
        validate_delivery_id(delivery_id)
        request = DeliveryRequest(delivery_id, args.kind, content)
        try:
            webhook = resolve_webhook(team_root, args.env_file)
        except LookupError as exc:
            result = gate.defer_unavailable(request, str(exc), now=now)
            print(json.dumps(result, sort_keys=True))
            notify_supervisor(result)
            return EXIT_DELIVERED if result["outcome"] == "duplicate" else EXIT_UNAVAILABLE

        interrupt = (
            os.environ.get("AGENT_TEAM_COMMS_TESTING") == "1"
            and os.environ.get("AGENT_TEAM_COMMS_TEST_INTERRUPT_AFTER_RECEIPT") == "1"
        )
        try:
            test_time = now if os.environ.get("AGENT_TEAM_COMMS_TESTING") == "1" else None
            result = gate.deliver(
                request,
                lambda body: post_webhook(webhook, body, test_time=test_time),
                now=now,
                interrupt_after_receipt=interrupt,
            )
        except BookkeepingInterrupted:
            return 91
        print(json.dumps(result, sort_keys=True))
        notify_supervisor(result)
        if result["outcome"] in {"delivered", "duplicate"}:
            return EXIT_DELIVERED
        if result["outcome"] == "deferred":
            return EXIT_DEFERRED
        return EXIT_FAILED
    except (RuntimeError, ValueError) as exc:
        print(f"discord-webhook.sh: {exc}", file=sys.stderr)
        return EXIT_FAILED


def validate_delivery_id(value: str) -> None:
    if not value.strip() or len(value) > 200 or any(character in value for character in "\r\n"):
        raise ValueError("delivery ID must be 1-200 characters without newlines")


def normalize_time(value: datetime) -> datetime:
    if value.tzinfo is None:
        raise ValueError("timestamps must include a timezone")
    return value.astimezone(timezone.utc)


def format_time(value: datetime | None) -> str:
    if value is None:
        return ""
    return normalize_time(value).isoformat().replace("+00:00", "Z")


def parse_time(value: str) -> datetime:
    return normalize_time(datetime.fromisoformat(value.replace("Z", "+00:00")))


def content_hash(content: str) -> str:
    return hashlib.sha256(content.encode("utf-8")).hexdigest()


def merge_text(existing: str, proposed: str) -> str:
    existing = existing.strip()
    proposed = proposed.strip()
    if not existing:
        return proposed
    if not proposed or proposed == existing:
        return existing
    existing_units = content_units(existing)
    proposed_units = content_units(proposed)
    if contains_unit_sequence(existing_units, proposed_units):
        return existing
    if contains_unit_sequence(proposed_units, existing_units):
        return proposed
    return f"{existing}\n\n{proposed}"


def content_units(content: str) -> tuple[str, ...]:
    return tuple(line.strip() for line in content.splitlines() if line.strip())


def contains_unit_sequence(container: tuple[str, ...], candidate: tuple[str, ...]) -> bool:
    if not candidate:
        return True
    width = len(candidate)
    return any(container[start : start + width] == candidate for start in range(len(container) - width + 1))


def load_json(path: Path, default: object) -> object:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        return default
    except (OSError, json.JSONDecodeError) as exc:
        raise RuntimeError(f"cannot read durable Discord state {path}: {exc}") from exc


def atomic_write_json(path: Path, value: object) -> None:
    atomic_write_text(path, json.dumps(value, indent=2, sort_keys=True) + "\n")


def atomic_write_text(path: Path, value: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            handle.write(value)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, path)
        sync_directory(path.parent)
    except Exception:
        try:
            os.close(descriptor)
        except OSError:
            pass
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise


def append_json_line(path: Path, value: object) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        try:
            os.chmod(path, 0o600)
        except OSError:
            pass
        handle.write(json.dumps(value, sort_keys=True) + "\n")
        handle.flush()
        os.fsync(handle.fileno())


def sync_directory(path: Path) -> None:
    try:
        descriptor = os.open(path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(descriptor)
    except OSError:
        pass
    finally:
        os.close(descriptor)


if __name__ == "__main__":
    sys.exit(main())

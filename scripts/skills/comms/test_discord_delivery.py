#!/usr/bin/env python3
"""Deterministic tests for the shared Discord delivery ceiling."""

from __future__ import annotations

from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time
import unittest
from unittest import mock


sys.dont_write_bytecode = True
REPO_ROOT = Path(__file__).resolve().parents[3]
SCRIPT = REPO_ROOT / "template" / "skills" / "comms" / "scripts" / "discord_delivery.py"
WRAPPER = SCRIPT.with_name("discord-webhook.sh")
SPEC = importlib.util.spec_from_file_location("discord_delivery", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
discord_delivery = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = discord_delivery
SPEC.loader.exec_module(discord_delivery)

T0 = datetime(2026, 7, 10, 12, 0, tzinfo=timezone.utc)


class RecordingSender:
    def __init__(self, *results: object) -> None:
        self.results = list(results)
        self.bodies: list[str] = []

    def __call__(self, content: str):
        self.bodies.append(content)
        if self.results:
            return self.results.pop(0)
        return discord_delivery.PostResult(True, status=200, message_id=f"message-{len(self.bodies)}")


class DiscordDeliveryTests(unittest.TestCase):
    def test_scheduled_then_release_back_to_back_posts_once(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            scheduled = gate.deliver(request("scheduled-1", "digest", "scheduled material"), sender, now=T0)
            release = gate.deliver(request("release-1", "release", "release material"), sender, now=T0)

            self.assertEqual(scheduled["outcome"], "delivered")
            self.assertEqual(release["outcome"], "deferred")
            self.assertEqual(sender.bodies, ["scheduled material"])
            self.assertEqual([item["delivery_id"] for item in gate.status(now=T0)["pending"]], ["release-1"])

    def test_release_then_scheduled_back_to_back_posts_once(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            release = gate.deliver(request("release-1", "release", "release material"), sender, now=T0)
            scheduled = gate.deliver(request("scheduled-1", "digest", "scheduled material"), sender, now=T0)

            self.assertEqual(release["outcome"], "delivered")
            self.assertEqual(scheduled["outcome"], "deferred")
            self.assertEqual(sender.bodies, ["release material"])
            self.assertEqual([item["delivery_id"] for item in gate.status(now=T0)["pending"]], ["scheduled-1"])

    def test_parallel_processes_share_one_lock_and_one_allowance(self) -> None:
        with tempfile.TemporaryDirectory() as tmp, RecordingWebhook() as webhook:
            team_root = Path(tmp) / ".agent_team"
            team_root.mkdir()
            env_file = Path(tmp) / "delivery.env"
            env_file.write_text(f"AGENT_TEAM_DISCORD_WEBHOOK={webhook.url}\n", encoding="utf-8")
            env = os.environ.copy()
            env.update(
                AGENT_TEAM_ROOT=str(team_root),
                AGENT_TEAM_COMMS_TESTING="1",
                AGENT_TEAM_COMMS_TEST_NOW="2026-07-10T12:00:00Z",
                PYTHONDONTWRITEBYTECODE="1",
            )
            processes = [
                subprocess.Popen(
                    [
                        sys.executable,
                        str(SCRIPT),
                        "--content",
                        f"parallel material {index}",
                        "--delivery-id",
                        f"parallel-{index}",
                        "--kind",
                        "manual",
                        "--env-file",
                        str(env_file),
                    ],
                    cwd=REPO_ROOT,
                    env=env,
                    text=True,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                )
                for index in range(2)
            ]
            completed = [process.communicate(timeout=10) + (process.returncode,) for process in processes]

            self.assertEqual(sorted(item[2] for item in completed), [0, discord_delivery.EXIT_DEFERRED])
            self.assertEqual(webhook.requests, 1)
            outcomes = sorted(json.loads(item[0])["outcome"] for item in completed)
            self.assertEqual(outcomes, ["deferred", "delivered"])

    def test_shell_wrapper_queues_and_notifies_when_webhook_is_unavailable(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            team_root = Path(tmp) / ".agent_team"
            team_root.mkdir()
            env = os.environ.copy()
            env.update(
                AGENT_TEAM_ROOT=str(team_root),
                AGENT_TEAM_COMMS_TESTING="1",
                AGENT_TEAM_COMMS_TEST_NOW="2026-07-10T12:00:00Z",
                PYTHONDONTWRITEBYTECODE="1",
            )

            completed = subprocess.run(
                [
                    str(WRAPPER),
                    "--content",
                    "durable pending material",
                    "--delivery-id",
                    "missing-webhook",
                    "--kind",
                    "manual",
                    "--env-file",
                    str(Path(tmp) / "absent.env"),
                ],
                cwd=REPO_ROOT,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(completed.returncode, discord_delivery.EXIT_UNAVAILABLE)
            self.assertEqual(json.loads(completed.stdout)["outcome"], "unavailable")
            state_dir = team_root / "state" / "comms" / "discord-delivery"
            pending = json.loads((state_dir / "pending.json").read_text(encoding="utf-8"))
            self.assertEqual(pending["items"][0]["delivery_id"], "missing-webhook")
            notices = (state_dir / "supervisor-notifications.jsonl").read_text(encoding="utf-8")
            self.assertIn("missing-webhook", notices)

    def test_shell_wrapper_duplicate_succeeds_when_webhook_is_unavailable(self) -> None:
        with tempfile.TemporaryDirectory() as tmp, RecordingWebhook() as webhook:
            team_root = Path(tmp) / ".agent_team"
            team_root.mkdir()
            env_file = Path(tmp) / "delivery.env"
            env_file.write_text(f"AGENT_TEAM_DISCORD_WEBHOOK={webhook.url}\n", encoding="utf-8")
            env = os.environ.copy()
            env.update(
                AGENT_TEAM_ROOT=str(team_root),
                AGENT_TEAM_COMMS_TESTING="1",
                AGENT_TEAM_COMMS_TEST_NOW="2026-07-10T12:00:00Z",
                PYTHONDONTWRITEBYTECODE="1",
            )
            command = [
                str(WRAPPER),
                "--content",
                "already delivered",
                "--delivery-id",
                "stable-cli-id",
                "--kind",
                "manual",
            ]

            first = subprocess.run(
                [*command, "--env-file", str(env_file)],
                cwd=REPO_ROOT,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )
            duplicate = subprocess.run(
                [*command, "--env-file", str(Path(tmp) / "absent.env")],
                cwd=REPO_ROOT,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(first.returncode, discord_delivery.EXIT_DELIVERED)
            self.assertEqual(duplicate.returncode, discord_delivery.EXIT_DELIVERED)
            self.assertEqual(json.loads(duplicate.stdout)["outcome"], "duplicate")
            self.assertEqual(webhook.requests, 1)

    def test_new_gate_instance_observes_durable_success(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_dir = Path(tmp) / "discord-delivery"
            sender = RecordingSender()
            first = discord_delivery.DeliveryGate(state_dir)
            self.assertEqual(first.deliver(request("first", "digest", "one"), sender, now=T0)["outcome"], "delivered")

            restarted = discord_delivery.DeliveryGate(state_dir)
            result = restarted.deliver(request("second", "digest", "two"), sender, now=T0 + timedelta(hours=1))

            self.assertEqual(result["outcome"], "deferred")
            self.assertEqual(len(sender.bodies), 1)
            self.assertEqual(restarted.status(now=T0)["last_success"]["delivery_id"], "first")

    def test_success_receipt_recovers_after_bookkeeping_interruption(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_dir = Path(tmp) / "discord-delivery"
            sender = RecordingSender()
            gate = discord_delivery.DeliveryGate(state_dir)

            with self.assertRaises(discord_delivery.BookkeepingInterrupted):
                gate.deliver(
                    request("receipt-1", "manual", "confirmed post"),
                    sender,
                    now=T0,
                    interrupt_after_receipt=True,
                )

            restarted = discord_delivery.DeliveryGate(state_dir)
            status = restarted.status(now=T0)
            retry = restarted.deliver(request("receipt-1", "manual", "confirmed post"), sender, now=T0)

            self.assertEqual(status["last_success"]["delivery_id"], "receipt-1")
            self.assertEqual(retry["outcome"], "duplicate")
            self.assertEqual(sender.bodies, ["confirmed post"])

    def test_deferred_body_survives_interruption_after_supervisor_notice(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_dir = Path(tmp) / "discord-delivery"
            sender = RecordingSender()
            gate = discord_delivery.DeliveryGate(state_dir)
            gate.deliver(request("seed", "digest", "seed"), sender, now=T0)

            with mock.patch.object(gate, "_save", side_effect=RuntimeError("interrupted after notice")):
                with self.assertRaisesRegex(RuntimeError, "interrupted after notice"):
                    gate.deliver(
                        request("deferred-release", "release", "durable release"),
                        sender,
                        now=T0 + timedelta(hours=1),
                    )

            notices = (state_dir / "supervisor-notifications.jsonl").read_text(encoding="utf-8")
            pending = discord_delivery.DeliveryGate(state_dir).status(now=T0 + timedelta(hours=1))
            self.assertIn("deferred-release", notices)
            self.assertEqual([item["delivery_id"] for item in pending["pending"]], ["deferred-release"])
            self.assertEqual(sender.bodies, ["seed"])

    def test_eligible_body_survives_interruption_after_reservation(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_dir = Path(tmp) / "discord-delivery"
            sender = RecordingSender()
            gate = discord_delivery.DeliveryGate(state_dir)

            with mock.patch.object(gate, "_save", side_effect=RuntimeError("interrupted after reservation")):
                with self.assertRaisesRegex(RuntimeError, "interrupted after reservation"):
                    gate.deliver(request("reserved-release", "release", "durable release"), sender, now=T0)

            attempts = list((state_dir / "attempts").glob("*.json"))
            pending = discord_delivery.DeliveryGate(state_dir).status(now=T0)
            self.assertEqual(len(attempts), 1)
            self.assertEqual([item["delivery_id"] for item in pending["pending"]], ["reserved-release"])
            self.assertEqual(sender.bodies, [])

    def test_pending_body_repairs_ledger_after_checkpoint_interruption(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            state_dir = Path(tmp) / "discord-delivery"
            gate = discord_delivery.DeliveryGate(state_dir)
            original_write = discord_delivery.atomic_write_json

            def interrupt_before_state(path: Path, value: object) -> None:
                if path == gate.state_path:
                    raise RuntimeError("interrupted between pending and state")
                original_write(path, value)

            with mock.patch.object(discord_delivery, "atomic_write_json", side_effect=interrupt_before_state):
                with self.assertRaisesRegex(RuntimeError, "interrupted between pending and state"):
                    gate.deliver(request("checkpointed", "manual", "durable body"), RecordingSender(), now=T0)

            restarted = discord_delivery.DeliveryGate(state_dir)
            status = restarted.status(now=T0)
            ledger = json.loads((state_dir / "state.json").read_text(encoding="utf-8"))
            self.assertEqual([item["delivery_id"] for item in status["pending"]], ["checkpointed"])
            self.assertEqual(ledger["deliveries"]["checkpointed"]["status"], "pending")

    def test_definitive_failed_delivery_is_immediately_retryable(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender(
                discord_delivery.PostResult(False, status=500, detail="HTTP 500"),
                discord_delivery.PostResult(True, status=200, message_id="retry-success"),
            )
            first = gate.deliver(request("retry-1", "digest", "retry me"), sender, now=T0)
            status_after_failure = gate.status(now=T0)
            second = gate.deliver(request("retry-1", "digest", "retry me"), sender, now=T0)

            self.assertEqual(first["outcome"], "failed")
            self.assertIsNone(status_after_failure["last_success"])
            self.assertTrue(status_after_failure["eligible"])
            self.assertEqual(second["outcome"], "delivered")
            self.assertEqual(len(sender.bodies), 2)

    def test_duplicate_delivery_id_never_posts_again(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            gate.deliver(request("stable-id", "release", "release"), sender, now=T0)
            duplicate = gate.deliver(
                request("stable-id", "release", "release"),
                sender,
                now=T0 + timedelta(hours=25),
            )

            self.assertEqual(duplicate["outcome"], "duplicate")
            self.assertEqual(sender.bodies, ["release"])

    def test_deferred_release_is_prioritized_and_merged_at_next_eligibility(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            gate.deliver(request("seed", "digest", "seed post"), sender, now=T0)
            gate.deliver(
                request("release-next", "release", "priority release"),
                sender,
                now=T0 + timedelta(seconds=1),
            )
            result = gate.deliver(
                request("digest-next", "digest", "catch-up digest"),
                sender,
                now=T0 + timedelta(hours=24),
            )

            self.assertEqual(result["outcome"], "delivered")
            self.assertEqual(result["included_delivery_ids"], ["release-next", "digest-next"])
            self.assertEqual(sender.bodies[1], "priority release\n\ncatch-up digest")
            self.assertEqual(gate.status(now=T0 + timedelta(hours=24))["pending"], [])

    def test_new_catch_up_digest_coalesces_overlapping_deferred_content(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            gate.deliver(request("seed", "digest", "seed post"), sender, now=T0)
            gate.deliver(
                request("digest-deferred", "digest", "A"),
                sender,
                now=T0 + timedelta(seconds=1),
            )
            result = gate.deliver(
                request("digest-catch-up", "digest", "A\nB"),
                sender,
                now=T0 + timedelta(hours=24),
            )

            self.assertEqual(result["outcome"], "delivered")
            self.assertEqual(result["included_delivery_ids"], ["digest-deferred", "digest-catch-up"])
            self.assertEqual(sender.bodies[1], "A\nB")
            self.assertEqual(gate.status(now=T0 + timedelta(hours=24))["pending"], [])

    def test_catch_up_overlap_does_not_collapse_prefix_collision(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            gate.deliver(request("seed", "digest", "seed post"), sender, now=T0)
            gate.deliver(
                request("release-12", "release", "Fix #12"),
                sender,
                now=T0 + timedelta(seconds=1),
            )
            result = gate.deliver(
                request("digest-123", "digest", "Fix #123 and add docs"),
                sender,
                now=T0 + timedelta(hours=24),
            )

            self.assertEqual(result["outcome"], "delivered")
            self.assertEqual(result["included_delivery_ids"], ["release-12", "digest-123"])
            self.assertEqual(sender.bodies[1], "Fix #12\n\nFix #123 and add docs")
            self.assertEqual(gate.status(now=T0 + timedelta(hours=24))["pending"], [])

    def test_oversized_pending_content_can_be_corrected_or_skipped(self) -> None:
        with self.gate() as gate:
            sender = RecordingSender()
            oversized = "x" * (discord_delivery.MAX_CONTENT_LENGTH + 1)

            rejected = gate.deliver(request("oversized", "release", oversized), sender, now=T0)
            corrected = gate.deliver(request("oversized", "release", "fixed"), sender, now=T0)
            poison = gate.deliver(
                request("still-oversized", "release", oversized),
                sender,
                now=T0 + timedelta(hours=25),
            )
            unrelated = gate.deliver(
                request("unrelated-digest", "digest", "digest material"),
                sender,
                now=T0 + timedelta(hours=25),
            )

            self.assertEqual(rejected["outcome"], "failed")
            self.assertEqual(corrected["outcome"], "delivered")
            self.assertEqual(poison["outcome"], "failed")
            self.assertEqual(unrelated["outcome"], "delivered")
            self.assertEqual(sender.bodies, ["fixed", "digest material"])
            self.assertEqual(
                [item["delivery_id"] for item in gate.status(now=T0 + timedelta(hours=25))["pending"]],
                ["still-oversized"],
            )

    def test_eligibility_immediately_before_at_and_after_24_hours(self) -> None:
        cases = [
            (timedelta(hours=24) - timedelta(seconds=1), "deferred", 1),
            (timedelta(hours=24), "delivered", 2),
            (timedelta(hours=24) + timedelta(seconds=1), "delivered", 2),
        ]
        for offset, expected, calls in cases:
            with self.subTest(offset=offset), self.gate() as gate:
                sender = RecordingSender()
                gate.deliver(request("seed", "digest", "seed"), sender, now=T0)
                result = gate.deliver(request("boundary", "digest", "boundary"), sender, now=T0 + offset)
                self.assertEqual(result["outcome"], expected)
                self.assertEqual(len(sender.bodies), calls)

    def test_subsecond_success_keeps_window_closed_for_full_24_hours(self) -> None:
        success_at = T0.replace(microsecond=900_000)
        cases = [
            (timedelta(hours=24) - timedelta(microseconds=800_000), "deferred", 1),
            (timedelta(hours=24) - timedelta(microseconds=1), "deferred", 1),
            (timedelta(hours=24), "delivered", 2),
        ]
        for offset, expected, calls in cases:
            with self.subTest(offset=offset), self.gate() as gate:
                sender = RecordingSender()
                seed = gate.deliver(request("seed", "digest", "seed"), sender, now=success_at)
                result = gate.deliver(request("boundary", "digest", "boundary"), sender, now=success_at + offset)

                self.assertEqual(seed["last_success"]["timestamp"], "2026-07-10T12:00:00.900000Z")
                self.assertEqual(result["outcome"], expected)
                self.assertEqual(len(sender.bodies), calls)

    def gate(self):
        return TemporaryGate()


class TemporaryGate:
    def __enter__(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.gate = discord_delivery.DeliveryGate(Path(self.temporary.name) / "discord-delivery")
        return self.gate

    def __exit__(self, exc_type, exc, traceback) -> None:
        self.temporary.cleanup()


class RecordingWebhook:
    def __enter__(self):
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler contract
                length = int(self.headers.get("Content-Length", "0"))
                self.rfile.read(length)
                time.sleep(0.1)
                with owner.lock:
                    owner.requests += 1
                    message_id = owner.requests
                body = json.dumps({"id": f"message-{message_id}"}).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, _format: str, *args: object) -> None:
                return

        self.requests = 0
        self.lock = threading.Lock()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        host, port = self.server.server_address
        self.url = f"http://{host}:{port}/api/webhooks/test/runtime-token"
        return self

    def __exit__(self, exc_type, exc, traceback) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)


def request(delivery_id: str, kind: str, content: str):
    return discord_delivery.DeliveryRequest(delivery_id, kind, content)


if __name__ == "__main__":
    unittest.main()

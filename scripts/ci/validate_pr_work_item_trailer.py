#!/usr/bin/env python3
"""Validate that pull request bodies carry a work-item trailer."""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

CLOSING_TRAILER_RE = re.compile(
    r"^\s*(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+"
    r"#\d+(?:[,\s]+#\d+)*\s*\.?\s*$",
    re.IGNORECASE,
)
ADVANCES_TRAILER_RE = re.compile(
    r"^\s*advances\s+#\d+(?:[,\s]+#\d+)*\s*\.?\s*$",
    re.IGNORECASE,
)

FAILURE_MESSAGE = """PR body is missing a standalone work-item trailer.

Add one of:
  Closes #123
  Fixes #123
  Resolves #123
  Advances #216

Use a closing keyword for implementation work that fully resolves a
non-epic issue. Use `Advances #<epic>` for design/slice PRs or epic work.
"""


def has_work_item_trailer(body: str) -> bool:
    for line in body.splitlines():
        if CLOSING_TRAILER_RE.match(line) or ADVANCES_TRAILER_RE.match(line):
            return True
    return False


def body_from_event(path: Path) -> tuple[str, bool]:
    with path.open("r", encoding="utf-8") as f:
        event = json.load(f)

    pull_request = event.get("pull_request")
    if not isinstance(pull_request, dict):
        return "", True

    body = pull_request.get("body")
    return body if isinstance(body, str) else "", False


def read_body(args: argparse.Namespace, parser: argparse.ArgumentParser) -> tuple[str, bool]:
    sources = [
        args.body is not None,
        args.body_file is not None,
        args.event_path is not None,
    ]
    if sum(sources) > 1:
        parser.error("pass only one of --body, --body-file, or --event-path")
    if args.body is not None:
        return args.body, False
    if args.body_file is not None:
        return args.body_file.read_text(encoding="utf-8"), False
    if args.event_path is not None:
        return body_from_event(args.event_path)
    parser.error("pass --body, --body-file, --event-path, or --self-test")
    raise AssertionError("unreachable")


def run_self_test() -> bool:
    valid = [
        "## Summary\n\n- ship it\n\nCloses #123\n",
        "Fixes #123.",
        "resolved #1, #2",
        "Advances #216",
        "## Footer\n\nADVANCES #216\n",
    ]
    invalid = [
        "",
        "This fixes #123 in passing, but is not a trailer.",
        "Contributes to #123",
        "Closes https://github.com/agent-team-project/kensho/issues/216",
        "Closes the issue",
    ]

    failures: list[str] = []
    for body in valid:
        if not has_work_item_trailer(body):
            failures.append(f"expected valid body to pass: {body!r}")
    for body in invalid:
        if has_work_item_trailer(body):
            failures.append(f"expected invalid body to fail: {body!r}")

    if failures:
        print("PR work-item trailer self-test failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return False
    print("OK  PR work-item trailer self-test")
    return True


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--body", help="PR body text to validate")
    parser.add_argument("--body-file", type=Path, help="file containing PR body text")
    parser.add_argument(
        "--event-path",
        type=Path,
        help="GitHub Actions event JSON path; non-PR events are skipped",
    )
    parser.add_argument("--self-test", action="store_true", help="run built-in examples")
    args = parser.parse_args()

    ok = True
    if args.self_test:
        ok = run_self_test()

    if args.body is None and args.body_file is None and args.event_path is None:
        return 0 if ok else 1

    body, skipped = read_body(args, parser)
    if skipped:
        print("Skipping PR work-item trailer validation for non-pull_request event")
        return 0 if ok else 1
    if has_work_item_trailer(body):
        print("OK  PR body has a work-item trailer")
        return 0 if ok else 1

    print(FAILURE_MESSAGE, file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())

#!/usr/bin/env python3
"""Check rendered docs HTML for leaked mustache markers outside examples."""

from __future__ import annotations

import argparse
import re
import sys
from html.parser import HTMLParser
from pathlib import Path


IGNORED_ELEMENTS = {"code", "pre", "kbd", "samp", "script", "style"}
MARKER = "{{"
SNIPPET_RADIUS = 60


class DocsMarkerParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self._ignored_depth = 0
        self.findings: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        del attrs
        if tag.lower() in IGNORED_ELEMENTS:
            self._ignored_depth += 1

    def handle_endtag(self, tag: str) -> None:
        if tag.lower() in IGNORED_ELEMENTS and self._ignored_depth > 0:
            self._ignored_depth -= 1

    def handle_data(self, data: str) -> None:
        if self._ignored_depth > 0 or MARKER not in data:
            return
        self.findings.append(snippet(data))


def snippet(text: str) -> str:
    collapsed = re.sub(r"\s+", " ", text).strip()
    marker_index = collapsed.find(MARKER)
    if marker_index < 0:
        return collapsed[: SNIPPET_RADIUS * 2]

    start = max(0, marker_index - SNIPPET_RADIUS)
    end = min(len(collapsed), marker_index + len(MARKER) + SNIPPET_RADIUS)
    context = collapsed[start:end].strip()
    if start > 0:
        context = "..." + context
    if end < len(collapsed):
        context += "..."
    return context


def find_markers(path: Path) -> list[str]:
    parser = DocsMarkerParser()
    parser.feed(path.read_text(encoding="utf-8", errors="replace"))
    parser.close()
    return parser.findings


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Detect rendered docs mustache markers outside code/example blocks.",
    )
    parser.add_argument("url", help="URL represented by the fetched HTML file")
    parser.add_argument("html_file", type=Path, help="Fetched rendered HTML file")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    findings = find_markers(args.html_file)
    if not findings:
        return 0

    suffix = ""
    if len(findings) > 1:
        suffix = f" (+{len(findings) - 1} more)"
    print(
        "docs page exposes literal '{{' marker outside code/example block: "
        f"{args.url} (context: {findings[0]}{suffix})",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    raise SystemExit(main())

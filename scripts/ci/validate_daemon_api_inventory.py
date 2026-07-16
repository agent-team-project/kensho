#!/usr/bin/env python3
"""Validate the documented daemon API inventory against live route registrations."""

from __future__ import annotations

import argparse
import re
import sys
from collections import Counter
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_HTTP_SOURCE = Path("internal/daemon/http.go")
DEFAULT_DOCUMENTATION = Path("documentation/orchestrator.md")
INVENTORY_START = "<!-- daemon-api-inventory:start -->"
INVENTORY_END = "<!-- daemon-api-inventory:end -->"
DOCUMENTED_ROUTE_RE = re.compile(r"/v1/[A-Za-z0-9_.~{}/-]+")
METHODS = frozenset({"DELETE", "GET", "PATCH", "POST", "PUT"})
GO_SIMPLE_ESCAPES = {
    "a": "\a",
    "b": "\b",
    "f": "\f",
    "n": "\n",
    "r": "\r",
    "t": "\t",
    "v": "\v",
    "\\": "\\",
    '"': '"',
    "'": "'",
}
GO_HEX_ESCAPE_WIDTHS = {"x": 2, "u": 4, "U": 8}


def normalize_route(path: str) -> str:
    """Map registrations and documented dynamic paths to one route family."""
    path = re.split(r"[?\[]", path.strip(), maxsplit=1)[0].rstrip("/")
    segments = path.split("/")
    for index, segment in enumerate(segments):
        if segment.startswith("{") and segment.endswith("}"):
            segments = segments[:index]
            break
    return "/".join(segments).rstrip("/")


def scan_go_interpreted_string(source: str, start: int) -> tuple[str, int]:
    """Decode one Go interpreted string and return its value and end offset."""
    value: list[str] = []
    index = start + 1
    while index < len(source):
        character = source[index]
        if character == '"':
            return "".join(value), index + 1
        if character in "\r\n":
            raise ValueError(f"unterminated interpreted string at offset {start}")
        if character != "\\":
            value.append(character)
            index += 1
            continue

        index += 1
        if index >= len(source):
            raise ValueError(f"unterminated escape at offset {start}")
        escape = source[index]
        if escape in GO_SIMPLE_ESCAPES:
            value.append(GO_SIMPLE_ESCAPES[escape])
            index += 1
            continue
        if escape in "01234567":
            digits = source[index : index + 3]
            if len(digits) != 3 or any(digit not in "01234567" for digit in digits):
                raise ValueError(f"invalid Go octal escape at offset {index - 1}")
            codepoint = int(digits, 8)
            if codepoint > 0xFF:
                raise ValueError(f"Go octal escape exceeds one byte at offset {index - 1}")
            value.append(chr(codepoint))
            index += 3
            continue
        if escape in GO_HEX_ESCAPE_WIDTHS:
            width = GO_HEX_ESCAPE_WIDTHS[escape]
            digits = source[index + 1 : index + 1 + width]
            if len(digits) != width or any(digit not in "0123456789abcdefABCDEF" for digit in digits):
                raise ValueError(f"invalid Go hexadecimal escape at offset {index - 1}")
            codepoint = int(digits, 16)
            if codepoint > 0x10FFFF or 0xD800 <= codepoint <= 0xDFFF:
                raise ValueError(f"invalid Go Unicode escape at offset {index - 1}")
            value.append(chr(codepoint))
            index += width + 1
            continue
        raise ValueError(f"invalid Go escape \\{escape} at offset {index - 1}")
    raise ValueError(f"unterminated interpreted string at offset {start}")


def skip_go_rune(source: str, start: int) -> int:
    """Skip a rune literal so comment and string delimiters inside it stay inert."""
    index = start + 1
    while index < len(source):
        character = source[index]
        if character == "'":
            return index + 1
        if character in "\r\n":
            raise ValueError(f"unterminated rune literal at offset {start}")
        index += 2 if character == "\\" else 1
    raise ValueError(f"unterminated rune literal at offset {start}")


def scan_go_tokens(source: str) -> list[tuple[str, str]]:
    """Return the Go tokens needed to identify active mux registrations."""
    tokens: list[tuple[str, str]] = []
    index = 0
    while index < len(source):
        character = source[index]
        if character.isspace():
            index += 1
            continue
        if source.startswith("//", index):
            newline = source.find("\n", index + 2)
            index = len(source) if newline == -1 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            if end == -1:
                raise ValueError(f"unterminated block comment at offset {index}")
            index = end + 2
            continue
        if character == '"':
            value, index = scan_go_interpreted_string(source, index)
            tokens.append(("string", value))
            continue
        if character == "`":
            end = source.find("`", index + 1)
            if end == -1:
                raise ValueError(f"unterminated raw string at offset {index}")
            tokens.append(("string", source[index + 1 : end].replace("\r", "")))
            index = end + 1
            continue
        if character == "'":
            index = skip_go_rune(source, index)
            continue
        if character == "_" or character.isalpha():
            end = index + 1
            while end < len(source) and (source[end] == "_" or source[end].isalnum()):
                end += 1
            tokens.append(("identifier", source[index:end]))
            index = end
            continue
        tokens.append((character, character))
        index += 1
    return tokens


def extract_registered_routes(source: str) -> list[str]:
    tokens = scan_go_tokens(source)
    routes: list[str] = []
    for index in range(len(tokens) - 5):
        if (
            tokens[index] == ("identifier", "mux")
            and tokens[index + 1] == (".", ".")
            and tokens[index + 2] in {
                ("identifier", "Handle"),
                ("identifier", "HandleFunc"),
            }
            and tokens[index + 3] == ("(", "(")
            and tokens[index + 4][0] == "string"
            and tokens[index + 4][1].startswith("/v1/")
            and tokens[index + 5] == (",", ",")
        ):
            routes.append(tokens[index + 4][1])
    return routes


def inventory_block(documentation: str) -> tuple[str, list[str]]:
    failures: list[str] = []
    if documentation.count(INVENTORY_START) != 1:
        failures.append(f"expected exactly one {INVENTORY_START} marker")
    if documentation.count(INVENTORY_END) != 1:
        failures.append(f"expected exactly one {INVENTORY_END} marker")
    if failures:
        return "", failures
    start = documentation.index(INVENTORY_START) + len(INVENTORY_START)
    try:
        end = documentation.index(INVENTORY_END, start)
    except ValueError:
        return "", [f"{INVENTORY_END} marker must follow {INVENTORY_START}"]
    return documentation[start:end], failures


def extract_documented_routes(block: str) -> list[str]:
    return DOCUMENTED_ROUTE_RE.findall(block)


def validate_inventory_rows(block: str, documented: list[str]) -> list[str]:
    failures: list[str] = []
    table_routes: list[str] = []
    for line_number, line in enumerate(block.splitlines(), start=1):
        routes = DOCUMENTED_ROUTE_RE.findall(line)
        if not routes or not line.lstrip().startswith("|"):
            continue
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if len(cells) != 4:
            failures.append(
                f"inventory row {line_number} must have method, path, purpose, and authority columns"
            )
            continue
        methods = [method.strip(" `") for method in cells[0].split(",")]
        if not methods or any(method not in METHODS for method in methods):
            failures.append(f"inventory row {line_number} has invalid methods: {cells[0]}")
        path_routes = DOCUMENTED_ROUTE_RE.findall(cells[1])
        if len(path_routes) != 1 or routes != path_routes:
            failures.append(
                f"inventory row {line_number} must put exactly one primary /v1 route family in the path column"
            )
        else:
            table_routes.append(path_routes[0])
        if not cells[2]:
            failures.append(f"inventory row {line_number} is missing a purpose")
        if not cells[3]:
            failures.append(f"inventory row {line_number} is missing authority semantics")

    if Counter(table_routes) != Counter(documented):
        failures.append(
            "every /v1 path between the inventory markers must appear once in a four-column table row"
        )
    return failures


def counted_routes(routes: list[str]) -> Counter[str]:
    return Counter(normalize_route(route) for route in routes)


def describe_counts(counts: Counter[str]) -> str:
    return ", ".join(
        route if count == 1 else f"{route} ({count} occurrences)"
        for route, count in sorted(counts.items())
    )


def validate_text(source: str, documentation: str) -> list[str]:
    failures: list[str] = []
    try:
        registered = extract_registered_routes(source)
    except ValueError as error:
        failures.append(f"could not lex daemon HTTP source: {error}")
        registered = []
    if not registered:
        failures.append("no literal /v1 route registrations found in daemon HTTP source")

    block, marker_failures = inventory_block(documentation)
    failures.extend(marker_failures)
    if marker_failures:
        return failures

    documented = extract_documented_routes(block)
    failures.extend(validate_inventory_rows(block, documented))
    registered_counts = counted_routes(registered)
    documented_counts = counted_routes(documented)
    missing = registered_counts - documented_counts
    stale = documented_counts - registered_counts
    if missing:
        failures.append("documented inventory omits: " + describe_counts(missing))
    if stale:
        failures.append("documented inventory has no live registration for: " + describe_counts(stale))
    return failures


def fixture_documentation(rows: list[tuple[str, str, str, str]], outside: str = "") -> str:
    rendered = [
        INVENTORY_START,
        "| Method(s) | Path | Purpose | Authority |",
        "|---|---|---|---|",
    ]
    rendered.extend(f"| {method} | `{path}` | {purpose} | {authority} |" for method, path, purpose, authority in rows)
    rendered.append(INVENTORY_END)
    rendered.append(outside)
    return "\n".join(rendered)


def run_self_test() -> list[str]:
    lexical_source = "\n".join(
        [
            'mux.HandleFunc("/v1/func-interpreted", handler)',
            'mux.Handle("/v1/handle-interpreted", handler)',
            "mux.HandleFunc(`/v1/func-raw`, handler)",
            "mux.Handle(`/v1/handle-raw`, handler)",
            '// mux.HandleFunc("/v1/line-comment-only", handler)',
            '/* mux.Handle("/v1/block-comment-only", handler) */',
            '_ = `mux.HandleFunc("/v1/raw-string-lookalike", handler)`',
            'mux.HandleFunc("/v1/composed-" + suffix, handler)',
        ]
    )
    expected_lexical_routes = [
        "/v1/func-interpreted",
        "/v1/handle-interpreted",
        "/v1/func-raw",
        "/v1/handle-raw",
    ]
    failures: list[str] = []
    if (got := extract_registered_routes(lexical_source)) != expected_lexical_routes:
        failures.append(
            "Go lexical extraction did not isolate active Handle calls: "
            f"expected {expected_lexical_routes}, got {got}"
        )

    source = "\n".join(
        [
            'mux.HandleFunc("/v1/status", handler)',
            'mux.HandleFunc("/v1/logs/", handler)',
            'mux.HandleFunc("/v1/queue", handler)',
            'mux.HandleFunc("/v1/queue/", handler)',
        ]
    )
    rows = [
        ("GET", "/v1/status", "Read status.", "Operator or instance."),
        ("GET", "/v1/logs/{instance}", "Read logs.", "Operator or instance."),
        ("GET", "/v1/queue", "List queue items.", "Operator or instance."),
        ("GET, POST", "/v1/queue/{id}/{verb}", "Read or mutate one item.", "Grant required for writes."),
    ]
    if got := validate_text(source, fixture_documentation(rows)):
        failures.append(f"valid dynamic and trailing-slash fixture failed: {got}")

    raw_route = source + "\nmux.HandleFunc(`/v1/raw-new`, handler)"
    got = validate_text(raw_route, fixture_documentation(rows))
    expected = "documented inventory omits: /v1/raw-new"
    if expected not in got:
        failures.append(f"raw-string registration mutant did not fail with {expected!r}: {got}")

    for label, commented_route in (
        ("line", '// mux.HandleFunc("/v1/comment-only", handler)'),
        ("block", '/* mux.Handle("/v1/comment-only", handler) */'),
    ):
        got = validate_text(source + "\n" + commented_route, fixture_documentation(rows))
        if got:
            failures.append(f"{label}-commented route was treated as a registration: {got}")

    missing_row = rows[:-1]
    got = validate_text(source, fixture_documentation(missing_row))
    expected = "documented inventory omits: /v1/queue"
    if expected not in got:
        failures.append(f"missing-family mutant did not fail with {expected!r}: {got}")

    counterfeit = fixture_documentation(
        [row for row in rows if row[1] != "/v1/logs/{instance}"],
        outside="The prose elsewhere mentions `/v1/logs/{instance}`.",
    )
    got = validate_text(source, counterfeit)
    expected = "documented inventory omits: /v1/logs"
    if expected not in got:
        failures.append(f"out-of-inventory counterfeit did not fail with {expected!r}: {got}")

    stale_rows = rows + [
        ("GET", "/v1/imaginary", "Not live.", "Operator or instance."),
    ]
    got = validate_text(source, fixture_documentation(stale_rows))
    expected = "documented inventory has no live registration for: /v1/imaginary"
    if expected not in got:
        failures.append(f"stale-documentation mutant did not fail with {expected!r}: {got}")
    return failures


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=REPO_ROOT)
    parser.add_argument("--http-source", type=Path, default=DEFAULT_HTTP_SOURCE)
    parser.add_argument("--documentation", type=Path, default=DEFAULT_DOCUMENTATION)
    parser.add_argument("--self-test", action="store_true")
    return parser.parse_args()


def resolve(repo_root: Path, path: Path) -> Path:
    return path if path.is_absolute() else repo_root / path


def main() -> int:
    args = parse_args()
    if args.self_test:
        failures = run_self_test()
        if failures:
            print("daemon API inventory validator self-test failed:", file=sys.stderr)
            for failure in failures:
                print(f"  - {failure}", file=sys.stderr)
            return 1
        print("OK  daemon API inventory validator rejects lexical lookalikes, missing, and stale route families")
        return 0

    repo_root = args.repo_root.resolve()
    source_path = resolve(repo_root, args.http_source)
    documentation_path = resolve(repo_root, args.documentation)
    try:
        source = source_path.read_text(encoding="utf-8")
        documentation = documentation_path.read_text(encoding="utf-8")
    except OSError as error:
        print(f"FAIL daemon API inventory: {error}", file=sys.stderr)
        return 1

    failures = validate_text(source, documentation)
    if failures:
        print("daemon API inventory validation failed:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    count = len(extract_registered_routes(source))
    print(f"OK  daemon API inventory covers {count} registered route families")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

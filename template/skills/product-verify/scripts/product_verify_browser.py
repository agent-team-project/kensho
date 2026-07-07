#!/usr/bin/env python3
"""Drive the daemon UI in headless Chromium and report browser-observed bugs."""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from product_verify_diff import ProductVerifyError
from product_verify_diff import read_operator_token
from product_verify_diff import resolve_daemon_url
from product_verify_diff import resolve_team_dir
from product_verify_diff import stable_json


DEFAULT_TIMEOUT_MS = 15_000

METRIC_IDS = (
    "instanceCount",
    "runningCount",
    "jobCount",
    "activeJobCount",
    "pipelineCount",
    "budgetTeamCount",
    "teamCount",
)

PANEL_BODY_IDS = {
    "instances": "instancesBody",
    "jobs": "jobsBody",
    "pipelines": "pipelinesBody",
    "budgets": "budgetsBody",
    "teams": "teamsBody",
}

ERROR_MARKERS = (
    "unavailable",
    "unauthorized",
    "forbidden",
    "network request failed",
    "request failed",
    "render failed",
    "unable to load",
)


def attr_value(obj: Any, name: str, default: Any = "") -> Any:
    value = getattr(obj, name, default)
    if callable(value):
        return value()
    return value


def truncate(value: Any, limit: int = 300) -> str:
    text = str(value).strip()
    if len(text) <= limit:
        return text
    return text[: limit - 1] + "..."


def load_playwright() -> tuple[Any, type[Exception], type[Exception]] | tuple[None, None, None]:
    try:
        from playwright.sync_api import Error as PlaywrightError
        from playwright.sync_api import TimeoutError as PlaywrightTimeoutError
        from playwright.sync_api import sync_playwright
    except ImportError:
        return None, None, None
    return sync_playwright, PlaywrightError, PlaywrightTimeoutError


def default_screenshot_dir(team_dir: Path) -> Path:
    state_dir = os.environ.get("AGENT_TEAM_STATE_DIR", "").strip()
    if state_dir:
        return Path(state_dir).expanduser().resolve() / "product-verify" / "screenshots"
    return team_dir / "state" / "product-verifier" / "product-verify" / "screenshots"


def sanitize_filename(value: str) -> str:
    cleaned = re.sub(r"[^a-zA-Z0-9._-]+", "-", value.strip().lower()).strip(".-")
    return cleaned or "broken-state"


def screenshot_path(screenshot_dir: Path, reason: str) -> Path:
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    return screenshot_dir / f"{stamp}-{sanitize_filename(reason)}.png"


def capture_screenshot(page: Any, screenshot_dir: Path, reason: str) -> str | None:
    try:
        screenshot_dir.mkdir(parents=True, exist_ok=True)
        path = screenshot_path(screenshot_dir, reason)
        page.screenshot(path=str(path), full_page=True)
        return str(path)
    except Exception:
        return None


def skip_report(reason: str, **extra: Any) -> dict[str, Any]:
    report = {"status": "skipped", "reason": reason}
    report.update({key: value for key, value in extra.items() if value})
    return report


def is_relevant_browser_url(base_url: str, raw_url: str) -> bool:
    parsed_base = urlparse(base_url)
    parsed_url = urlparse(raw_url)
    if parsed_url.scheme != parsed_base.scheme or parsed_url.netloc != parsed_base.netloc:
        return False
    if parsed_url.path == "/favicon.ico":
        return False
    return parsed_url.path == "/ui" or parsed_url.path.startswith("/ui/") or parsed_url.path.startswith("/v1/")


def response_status(response: Any) -> int:
    status = attr_value(response, "status", 0)
    try:
        return int(status)
    except (TypeError, ValueError):
        return 0


def console_error_entry(message: Any) -> dict[str, Any] | None:
    if attr_value(message, "type") != "error":
        return None
    entry = {
        "type": "console_error",
        "text": truncate(attr_value(message, "text")),
    }
    location = attr_value(message, "location", None)
    if isinstance(location, dict):
        entry["location"] = location
    return entry


def failed_request_entry(base_url: str, request: Any) -> dict[str, Any] | None:
    raw_url = str(attr_value(request, "url"))
    if not is_relevant_browser_url(base_url, raw_url):
        return None
    failure = attr_value(request, "failure", None)
    return {
        "type": "request_failed",
        "url": raw_url,
        "method": attr_value(request, "method", ""),
        "error": truncate(failure or "request failed"),
    }


def failed_response_entry(base_url: str, response: Any) -> dict[str, Any] | None:
    raw_url = str(attr_value(response, "url"))
    status = response_status(response)
    if status < 400 or not is_relevant_browser_url(base_url, raw_url):
        return None
    return {
        "type": "http_error",
        "url": raw_url,
        "status": status,
        "status_text": truncate(attr_value(response, "status_text", "")),
    }


def page_error_entry(error: Any) -> dict[str, Any]:
    return {
        "type": "page_error",
        "text": truncate(error),
    }


def dom_snapshot(page: Any) -> dict[str, Any]:
    return page.evaluate(
        """
() => {
  const text = (selector) => document.querySelector(selector)?.textContent?.trim() || "";
  const inputValue = (selector) => document.querySelector(selector)?.value || "";
  const rowTexts = (selector) => Array.from(document.querySelectorAll(`${selector} tr`))
    .map((row) => row.textContent.trim().replace(/\\s+/g, " "))
    .filter(Boolean);
  const metrics = {};
  for (const id of %s) {
    metrics[id] = text(`#${id}`);
  }
  const panels = {};
  for (const [name, id] of Object.entries(%s)) {
    const rows = rowTexts(`#${id}`);
    panels[name] = {
      id,
      rows: rows.length,
      text: rows.join(" | "),
    };
  }
  return {
    title: document.title,
    h1: text("h1"),
    tokenInputPresent: Boolean(document.querySelector("#tokenInput")),
    tokenInputFilled: Boolean(inputValue("#tokenInput")),
    connectionText: text("#connectionText"),
    notice: text("#notice"),
    refreshState: text("#refreshState"),
    metrics,
    panels,
  };
}
"""
        % (json.dumps(METRIC_IDS), json.dumps(PANEL_BODY_IDS))
    )


def ok_check(name: str, detail: str | dict[str, Any] = "") -> dict[str, Any]:
    return {"name": name, "ok": True, "detail": detail}


def failed_check(name: str, detail: str | dict[str, Any]) -> dict[str, Any]:
    return {"name": name, "ok": False, "detail": detail}


def checks_for_dom_snapshot(snapshot: dict[str, Any]) -> list[dict[str, Any]]:
    checks: list[dict[str, Any]] = []
    if snapshot.get("h1") == "Daemon Dashboard":
        checks.append(ok_check("dashboard_heading"))
    else:
        checks.append(failed_check("dashboard_heading", {"h1": snapshot.get("h1", "")}))

    if snapshot.get("tokenInputPresent") and snapshot.get("tokenInputFilled"):
        checks.append(ok_check("token_flow"))
    else:
        checks.append(
            failed_check(
                "token_flow",
                {
                    "tokenInputPresent": snapshot.get("tokenInputPresent", False),
                    "tokenInputFilled": snapshot.get("tokenInputFilled", False),
                },
            )
        )

    if snapshot.get("connectionText") == "Connected":
        checks.append(ok_check("connection_state"))
    else:
        checks.append(failed_check("connection_state", {"connectionText": snapshot.get("connectionText", "")}))

    notice = str(snapshot.get("notice", ""))
    if "loaded" in notice.lower():
        checks.append(ok_check("notice"))
    else:
        checks.append(failed_check("notice", {"notice": notice}))

    metrics = snapshot.get("metrics", {})
    for metric_id in METRIC_IDS:
        value = str(metrics.get(metric_id, "")).strip()
        if re.fullmatch(r"\d+", value):
            checks.append(ok_check(f"metric.{metric_id}", value))
        else:
            checks.append(failed_check(f"metric.{metric_id}", {"value": value}))

    panels = snapshot.get("panels", {})
    for panel_name in PANEL_BODY_IDS:
        panel = panels.get(panel_name, {})
        rows = int(panel.get("rows", 0) or 0)
        text = str(panel.get("text", ""))
        lower_text = text.lower()
        if rows <= 0:
            checks.append(failed_check(f"panel.{panel_name}", {"rows": rows, "text": text}))
            continue
        marker = next((item for item in ERROR_MARKERS if item in lower_text), "")
        if marker:
            checks.append(failed_check(f"panel.{panel_name}", {"marker": marker, "text": text}))
            continue
        checks.append(ok_check(f"panel.{panel_name}", {"rows": rows}))

    if snapshot.get("refreshState") == "Every 15s":
        checks.append(ok_check("auto_refresh_toggle"))
    else:
        checks.append(failed_check("auto_refresh_toggle", {"refreshState": snapshot.get("refreshState", "")}))

    return checks


def finding(summary: str, detail: Any, screenshot: str | None = None) -> dict[str, str]:
    out = {
        "category": "bug",
        "summary": summary,
        "detail": stable_json(detail),
    }
    if screenshot:
        out["screenshot"] = screenshot
    return out


def finding_for_check(check: dict[str, Any], screenshot: str | None) -> dict[str, str]:
    return finding(
        f"product-verifier browser: DOM check {check.get('name', 'unknown')} failed",
        check,
        screenshot,
    )


def finding_for_browser_error(error: dict[str, Any], screenshot: str | None) -> dict[str, str]:
    if error.get("type") == "console_error":
        summary = f"product-verifier browser: console error in daemon UI: {truncate(error.get('text', ''))}"
    elif error.get("type") == "page_error":
        summary = f"product-verifier browser: page error in daemon UI: {truncate(error.get('text', ''))}"
    elif error.get("type") == "http_error":
        summary = f"product-verifier browser: daemon UI request returned HTTP {error.get('status')} for {error.get('url')}"
    elif error.get("type") == "request_failed":
        summary = f"product-verifier browser: daemon UI request failed for {error.get('url')}"
    else:
        summary = "product-verifier browser: daemon UI browser error"
    return finding(summary, error, screenshot)


def build_browser_report(
    checks: list[dict[str, Any]],
    browser_errors: list[dict[str, Any]],
    screenshot: str | None,
    max_findings: int,
) -> dict[str, Any]:
    failed_checks = [check for check in checks if not check.get("ok")]
    findings = [finding_for_check(check, screenshot) for check in failed_checks]
    findings.extend(finding_for_browser_error(error, screenshot) for error in browser_errors)

    capped = False
    if max_findings >= 0 and len(findings) > max_findings:
        findings = findings[:max_findings]
        capped = True

    return {
        "status": "mismatch" if failed_checks or browser_errors else "ok",
        "summary": {
            "checks": len(checks),
            "failed_checks": len(failed_checks),
            "browser_errors": len(browser_errors),
            "findings": len(findings),
            "capped": capped,
            "screenshot": screenshot or "",
        },
        "checks": checks,
        "browser_errors": browser_errors,
        "findings": findings,
    }


def wait_for_dashboard_settled(page: Any, timeout_ms: int) -> None:
    page.wait_for_function(
        """
() => {
  const connection = document.querySelector("#connectionText")?.textContent || "";
  const notice = document.querySelector("#notice")?.textContent || "";
  return connection !== "Checking connection" && !notice.includes("Loading daemon data");
}
""",
        timeout=timeout_ms,
    )


def exercise_dashboard(page: Any, base_url: str, token: str, timeout_ms: int) -> dict[str, Any]:
    ui_url = f"{base_url.rstrip('/')}/ui"
    page.add_init_script(
        f"""window.sessionStorage.setItem("agent-team.daemonToken", {json.dumps(token)});""",
    )
    page.goto(ui_url, wait_until="domcontentloaded", timeout=timeout_ms)
    page.locator("#tokenInput").fill(token, timeout=timeout_ms)
    page.locator("#saveToken").click(timeout=timeout_ms)
    wait_for_dashboard_settled(page, timeout_ms)
    page.locator("#refresh").click(timeout=timeout_ms)
    wait_for_dashboard_settled(page, timeout_ms)

    auto_refresh = page.locator("#autoRefresh")
    if auto_refresh.is_checked(timeout=timeout_ms):
        auto_refresh.click(timeout=timeout_ms)
        page.wait_for_function(
            """() => document.querySelector("#refreshState")?.textContent === "Manual" """,
            timeout=timeout_ms,
        )
    auto_refresh.click(timeout=timeout_ms)
    page.wait_for_function(
        """() => document.querySelector("#refreshState")?.textContent === "Every 15s" """,
        timeout=timeout_ms,
    )
    try:
        page.wait_for_load_state("networkidle", timeout=timeout_ms)
    except Exception:
        pass
    return dom_snapshot(page)


def run_browser_check(
    base_url: str,
    token: str,
    screenshot_dir: Path,
    max_findings: int,
    timeout_ms: int,
) -> dict[str, Any]:
    sync_playwright, playwright_error, playwright_timeout_error = load_playwright()
    if sync_playwright is None:
        return skip_report("Python Playwright is not installed in this runtime")

    browser = None
    context = None
    page = None
    browser_errors: list[dict[str, Any]] = []
    checks: list[dict[str, Any]] = []
    unexpected_error: Exception | None = None
    with sync_playwright() as playwright:
        try:
            browser = playwright.chromium.launch(headless=True)
        except Exception as exc:
            return skip_report("Playwright Chromium is not available in this runtime", detail=truncate(exc))

        try:
            context = browser.new_context(viewport={"width": 1440, "height": 1000})
            page = context.new_page()
            page.on(
                "console",
                lambda message: (
                    browser_errors.append(entry)
                    if (entry := console_error_entry(message)) is not None
                    else None
                ),
            )
            page.on("pageerror", lambda error: browser_errors.append(page_error_entry(error)))
            page.on(
                "requestfailed",
                lambda request: (
                    browser_errors.append(entry)
                    if (entry := failed_request_entry(base_url, request)) is not None
                    else None
                ),
            )
            page.on(
                "response",
                lambda response: (
                    browser_errors.append(entry)
                    if (entry := failed_response_entry(base_url, response)) is not None
                    else None
                ),
            )
            snapshot = exercise_dashboard(page, base_url, token, timeout_ms)
            checks = checks_for_dom_snapshot(snapshot)
        except (playwright_timeout_error, playwright_error) as exc:
            checks.append(failed_check("browser_journey", {"error": truncate(exc)}))
        except Exception as exc:
            unexpected_error = exc
        finally:
            broken = any(not check.get("ok") for check in checks) or bool(browser_errors)
            screenshot = capture_screenshot(page, screenshot_dir, "broken-state") if broken and page else None
            if context:
                context.close()
            if browser:
                browser.close()

    if unexpected_error is not None:
        return {"status": "error", "error": f"browser verifier failed: {truncate(unexpected_error)}"}
    return build_browser_report(checks, browser_errors, screenshot, max_findings)


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--team-dir", help="Path to .agent_team. Defaults to AGENT_TEAM_ROOT or ./.agent_team.")
    parser.add_argument("--daemon-url", help="Override daemon base URL. Defaults to AGENT_TEAM_DAEMON_URL or daemon/http.addr.")
    parser.add_argument("--screenshot-dir", help="Directory for broken-state screenshots.")
    parser.add_argument("--max-findings", type=int, default=5, help="Maximum bug findings to include. Use -1 for unlimited.")
    parser.add_argument("--timeout-ms", type=int, default=DEFAULT_TIMEOUT_MS, help="Per-step Playwright timeout.")
    parser.add_argument("--no-fail", action="store_true", help="Exit 0 even when browser findings are found.")
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    team_dir = resolve_team_dir(args.team_dir)
    daemon_url = args.daemon_url.rstrip("/") if args.daemon_url else resolve_daemon_url(team_dir)
    if not daemon_url:
        print(
            json.dumps(
                skip_report(
                    "daemon HTTP address is not configured",
                    expected=str(team_dir / "daemon" / "http.addr"),
                ),
                indent=2,
                sort_keys=True,
            )
        )
        return 0

    try:
        token = read_operator_token(team_dir)
    except ProductVerifyError as exc:
        print(json.dumps({"status": "error", "error": str(exc)}, indent=2, sort_keys=True))
        return 2

    screenshot_dir = (
        Path(args.screenshot_dir).expanduser().resolve()
        if args.screenshot_dir
        else default_screenshot_dir(team_dir)
    )
    report = run_browser_check(daemon_url, token, screenshot_dir, args.max_findings, args.timeout_ms)
    print(json.dumps(report, indent=2, sort_keys=True))
    if report["status"] == "mismatch" and not args.no_fail:
        return 1
    if report["status"] == "error":
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

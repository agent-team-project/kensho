# Product-Verifier Loop

The product-verifier loop is a scheduled synthetic user for agent-team. It
dogfoods the shipped product surface once per day and files local feedback for
the existing feedback-triage loop to cluster and route.

v2 is browser-driven when the verifier runs on a browser-capable runtime. It
uses Playwright headless Chromium to load `/ui`, enter the operator token from
`.agent_team/daemon/operator.token`, click Connect/Refresh/auto-refresh, assert
that dashboard metrics and panels render, and capture console errors, page
errors, failed network requests, HTTP failures, and screenshots for broken
states. It does not require an external browser service; the runtime image
should provide Python Playwright plus its local Chromium install
(`python3 -m playwright install chromium`). If the daemon HTTP listener,
Playwright, or Chromium is unavailable, the browser pass skips cleanly.

The original v1 data diff remains part of the loop. It fetches daemon UI data
endpoints (`/v1/instances`, `/v1/jobs`, and `/v1/topology`) with the operator
token, reads the same state through the CLI with the global repo selector
(`agent-team --repo <repo> ps --json`, `agent-team --repo <repo> job ls
--json`, and `agent-team --repo <repo> topology show --json`), and diffs
explicit equivalence projections. Any mismatch is filed as `agent-team feedback
submit --category bug`.

For instance rows, the projection is intentionally limited to daemon/CLI shared
daemon metadata: `instance`, `agent`, `status`, `runtime`, and `job`. The
comparison only considers instance names present in both `/v1/instances` and
`agent-team ps --json`. `ps` may include declared/status-only rows that the
daemon endpoint does not list, and may prefer status-file or topology values for
fields such as `branch` and `workspace`; those are intentionally excluded along
with CLI-only enrichment such as PR links, process IDs, runtime binaries, and
resume counters.

The mechanical helper is shipped with the bundled skill at
`.agent_team/skills/product-verify/scripts/product_verify_diff.py`.
The browser helper is shipped next to it at
`.agent_team/skills/product-verify/scripts/product_verify_browser.py`.

The loop also performs a short subjective pass over operator clarity: empty and
error states, token-flow clarity, legibility of statuses and budgets, and
missing read-only operator affordances. Those findings are filed as
`--category friction` or `--category idea`.

Guardrails:

- The loop is read-only against daemon/product state.
- Intended writes are to the local feedback store and broken-state screenshots
  under the verifier state directory.
- If the daemon has no loopback HTTP address configured, the endpoint diff
  skips cleanly instead of treating that as a product bug.
- If the runtime is not browser-capable, the browser pass skips cleanly instead
  of treating missing Playwright/Chromium as a product bug.
- Findings are capped per run and deduplicated by the feedback fingerprint.

The shipped schedule is `product-verify` every 24 hours, handled by the
ephemeral `product-verifier` manager instance using the `product-verify` skill.

Retirement condition: once real user issues, discussions, and feedback provide
enough organic product signal, this synthetic loop should be removed or reduced.

Browser findings include the screenshot path in the feedback body because the
current feedback CLI stores text-only items.

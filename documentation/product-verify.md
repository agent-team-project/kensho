# Product-Verifier Loop

The product-verifier loop is a scheduled synthetic user for agent-team. It
dogfoods the shipped product surface once per day and files local feedback for
the existing feedback-triage loop to cluster and route.

v1 is intentionally headless. It fetches daemon UI data endpoints
(`/v1/instances`, `/v1/jobs`, and `/v1/topology`) with the operator token from
`.agent_team/daemon/operator.token`, reads the same state through the CLI, and
diffs explicit equivalence projections. Any mismatch is filed as `agent-team
feedback submit --category bug`.

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

The loop also performs a short subjective pass over operator clarity: empty and
error states, token-flow clarity, legibility of statuses and budgets, and
missing read-only operator affordances. Those findings are filed as
`--category friction` or `--category idea`.

Guardrails:

- The loop is read-only against daemon/product state.
- The only write is to the local feedback store.
- If the daemon has no loopback HTTP address configured, the endpoint diff
  skips cleanly instead of treating that as a product bug.
- Findings are capped per run and deduplicated by the feedback fingerprint.

The shipped schedule is `product-verify` every 24 hours, handled by the
ephemeral `product-verifier` manager instance using the `product-verify` skill.

Retirement condition: once real user issues, discussions, and feedback provide
enough organic product signal, this synthetic loop should be removed or reduced.

Follow-up v2: run the same journey in a real browser with Playwright or another
browser-capable runtime so the verifier can click through the UI rather than
only inspecting served assets and JSON data.

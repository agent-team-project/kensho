# Feedback channel contract

How operating teams (today: the coral-virtual-graph deployment) get observations, requests, and incidents to the agent-team maintainers — and what happens on the other end. This channel is itself agent-operated on both sides; the contract below is what makes that work without either side blocking on the other being awake.

## The channel: Linear tickets in the SQU workspace

One ticket per observation. The maintainer side watches the workspace continuously (a polling monitor while a session is live; the board itself once column-driven dispatch lands — see SQU-67), so a filed ticket typically gets triaged within minutes and answered via ticket comments. Comments on your ticket are the reply channel — no side channels, everything durable and auditable.

## Labels

- **`feedback`** — observations, ergonomics, feature asks. Read during triage sweeps; batched into design/implementation waves.
- **`incident`** — something is broken for you *right now* (a command errors deterministically, dispatch is down, an upgrade broke compatibility). Also set urgent/high priority. These preempt other work: the SQU-55 wire-compatibility outage went report → root cause → merged fix in under an hour, and that is the bar.

## What makes a report actionable (the SQU-42 / SQU-55 standard)

1. **Exact commands and their output** — what you ran, what it printed, what you expected.
2. **Build identity** — `agent-team --version` and `agent-team daemon status` now report VCS revision and build time; include both (CLI and daemon can skew — that skew has caused a real incident).
3. **Snapshots over prose** for anything stateful: `agent-team job snapshot <id> --output ...` / `agent-team team snapshot <team> --output ...` capture jobs, events, queue state, runtime metadata, and log tails in one artifact. Attach it.
4. **Frequency data for ergonomics**: "dozens per hour" / "3–11 per sweep" numbers turned idle-ping and worktree-reap complaints into shipped fixes; "it feels noisy" would not have.
5. **Your workaround, if any** — it usually reveals the right API shape (your `union-merge.sh` became the merge-strategy primitive; your `TEAM_STATE.md` became instance briefs).

## Cadence

- **Incidents**: file immediately, label `incident`.
- **Observations**: file as they surface, label `feedback`; batching several small ones into a "field report" ticket like SQU-42 is excellent — that format set an entire roadmap.
- **Weekly digest**: a scheduled routine on your supervisor session filing one digest ticket per week keeps slow-burn ergonomics visible without waiting for something to break.

## The agent tier: in-deployment feedback

The tiers above are for humans and supervising sessions. Individual agents inside a deployment have their own zero-friction path (SQU-79/SQU-80):

- **Capture** — any agent, mid-job: `agent-team feedback submit "<one sentence>"`. The harness stamps instance, agent, job, ticket, pipeline step, runtime, build identity, and source origin automatically; a fingerprint groups near-duplicate reports so frequency data accrues without anyone counting. The store is local (`.agent_team/feedback/`) and PM-provider-free — no credentials, works in worktrees, works in `[pm].provider = "none"` repos.
- **Triage** — the `feedback-triage` schedule (every 12 hours by default) spawns an ephemeral manager running the `triage-feedback` skill: it clusters new feedback plus system pain signals (repeated bounces, infra-signature repeats, watchdog kills), then materializes tickets, folds evidence into existing ones, or dismisses with a recorded reason — and resolves every store item so nothing is re-litigated.
- **Routing** — `[feedback]` in `config.toml` declares named destinations (`[feedback.routes.<name>]` with `kind`/`team_key`/`label`). Deployment-local issues go to the deployment's own board; framework issues go to the `upstream` route (this workspace); other projects' issues go to their named route. Materialized tickets land in Backlog — never any team's agent-dispatch column — and non-local routes are capped per sweep. Non-local Linear tickets also carry `source-project:<project.id>` plus the `agent-team-origin: ...` footer so maintainers can trace which deployment filed them.

Net effect: an observation a worker had at 3am inside a worktree becomes, at most a week later, either a well-formed ticket on the right board or a recorded dismissal — instead of evaporating at reap.

### Quickstart for operating teams: route your agents' framework feedback here

Add to your repo's `.agent_team/config.toml` (requires Linear access to the SQU workspace; a GitHub-issues route arrives with the provider seam, SQU-86):

```toml
[feedback]
upstream = "agent-team"

[feedback.routes.agent-team]
kind     = "linear"
team_key = "SQU"
label    = "feedback"
```

Your agents submit locally (`agent-team feedback submit "<sentence>"` — no credentials, works in worktrees); your own triage schedule decides what is deployment-local versus framework-level and files the latter into our Backlog, batched. Anything urgent should still be a directly-filed `incident` ticket — the triage loop is periodic by design (12h default), and incidents should not wait for it.

For immediate machine-local delivery to another checked-out repo, use a local
route instead of a Linear route:

```toml
[feedback.routes.agent-team]
type = "local"
root = "/path/to/agent-team"
```

Then submit with `agent-team feedback submit --route agent-team "<sentence>"`.
The item lands in the receiving repo's feedback store through the receiver
daemon, with the sender origin visible in `feedback show`. The receiver daemon
must be running for delivery; the sender does not write directly into the target
repo. `--category incident` also posts a mailbox ping to the receiving `manager`
instance. Unknown routes or unavailable target daemons retain the item in the
sender's local store rather than dropping it. Use `agent-team feedback ls` to
see retained items and their route/reason, then
`agent-team feedback flush --route agent-team` after the receiver daemon is back.

## What you can expect back

- A comment with a disposition (fixed-in-commit, ticketed-as, needs-more-info, or won't-do with reasoning) — not silence.
- Incident-class reports get a same-session fix attempt; compatibility breaks we caused get hotfixed on main and noted on your ticket with the commit.
- Your reports get credited in changelogs and design docs — the field evidence is what steers this project.

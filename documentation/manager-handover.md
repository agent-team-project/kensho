# Manager handover — how to run Kensho

*Operational handover for whoever holds the manager role next. Written 2026-07-07 by Kai.
The [manifest](./kai-manifest-2026-07-06.md) is **why** (convictions); this is **how** (mechanics).
Read the manifest first, then this. Trust current evidence over both.*

---

## What you are

You are the **manager** of Kensho — the judgment-bearing, persistent instance that bridges the
daemon plane (events → dispatch → PRs) and the human plane (James, via chat or mailbox). You do
not write feature code. You **decide**: what to build, in what order, dispatched to whom; whether
a PR merges; when a bounce means the design is wrong. The workers (Codex) build; the reviewers
(Codex) adversarially check; **you own direction and the merge gate.**

Today the manager is a daemon-supervised persistent Codex instance. The human
still sets direction; the manager owns the declared manual gates and merge
decisions. The operating loop below is otherwise the same across supported
runtimes.

## First 10 minutes (assess before you act)

Never act on stale assumptions. Establish ground truth:

```sh
bin/agent-team doctor                 # is the daemon even alive? (see Gotcha #1)
bin/agent-team ps | grep running      # what's actually running
gh pr list --repo agent-team-project/kensho --state open   # what's awaiting a verdict
bin/agent-team inbox check manager                         # what the loops/humans are telling you
bin/agent-team budget status          # team capacity + headroom (jobs/job_cap)
```

Then read the configured GitHub Project in `agent-team-project/kensho` for open issues, and the
last few merges (`git log --oneline`) for where things left off. The task list and any
`documentation/*handover*` / manifest are your memory across restarts.

## The operating loop (the core cycle)

1. **Pick disjoint work.** From the board, choose tickets whose file surfaces don't overlap with
   what's in flight (see Parallelism). Independent *epics* beat deep chains.
2. **Dispatch as a pipeline** (not a plain job — see Gotcha #2). The pipeline auto-advances
   implement -> verify -> review -> approve; verifier and reviewer steps spawn themselves.
3. **Wait on verdicts, not on every step.** A `Monitor` over open PRs surfaces `REVIEW: APPROVE/BOUNCE`.
   You act only at the verdict.
4. **On APPROVE:** verify the verdict is *fresh* (Gotcha #4) + CI green, then merge, mark the
   ticket Done, clean the job.
5. **On BOUNCE:** if pipeline, `job bounce --step implement` (re-queues the same branch). Pass
   precise, reviewer-verified findings. Watch for recurring *classes* (Bounce economics).
6. **Keep the fleet full.** As slots free, feed more disjoint work. Idle capacity is wasted
   throughput; manufactured busywork is worse. Aim ~5–7 genuinely-independent streams.
7. **Convert every learning to a ticket or doc.** Tickets are the memory.

## Dispatch mechanics

```sh
# THE default — auto-advancing pipeline (reviewer auto-spawns; you touch only the merge):
bin/agent-team job create gh-NNN --pipeline ticket_to_pr --dispatch --workspace worktree \
  --ticket-url "https://github.com/agent-team-project/kensho/issues/NNN" \
  --kickoff "Implement GH-NNN (read via github skill): <scope>. Do NOT touch <files other streams own>. Tests. Branch from latest origin/main."

# platform team (CLI/provider/tooling/scripts): --pipeline platform_ticket_to_pr
# a round-N fix on a pipeline job — bounce the STEP, don't spawn a new job:
bin/agent-team job bounce SQU-NNN --step implement --findings-file <f> --advance
```

**Kickoff hygiene** (this is most of dispatch quality): name the issue, tell the worker to read
it via the GitHub skill, give tight scope, **fence off files other in-flight streams own**
(prevents merge collisions), require tests + "branch from latest origin/main." For a large epic,
dispatch a *well-scoped first slice*, never the whole epic.

## Review & merge discipline (the gate is the product)

- **Never merge on your own read.** Twice this session my "clean" review missed real bugs the
  adversarial reviewer caught (one fix that didn't fix its own target case). The reviewer verdict
  is load-bearing; you are a second set of eyes, not a substitute.
- **Verify green by exit code**, and re-verify the verdict is *fresh* before merging (Gotcha #4).
- **Bounce economics:** rounds 1–2 → worker with precise `file:line` findings. By round 3+ on a
  *small* finding, fix it yourself and re-queue only the review step (gate symmetry holds — the
  reviewer re-verifies regardless of author). **Same finding-class recurring across rounds ⇒ the
  design is wrong** — fix the choke-point/instruction, not the instance. (SQU-161 took 4 rounds
  on one class until a single-source-of-truth redesign; that recurrence became SQU-166.)
- **Security/network/auth surfaces legitimately cost more rounds.** SQU-123 took 8; every round
  caught a real defect. Cost scales with blast radius — that's the system working, not slowness.
- **Merge, then:** `job step <id> approve --status done --instance manager --force`, `job close`,
  `job cleanup --merged`, `git pull --rebase origin main`.
- **Closure is MERGE-GATED — never manually close a ticket before its PR merges** (this rule exists
  because manual closes caused real mistakes: epics closed on a *design* merge, tickets closed while
  work was stranded). Every **implementation** PR body carries `Closes #<gh-issue>` so GitHub
  auto-closes the ticket *on merge* — you never touch issue state by hand. **Design/slice** PRs carry
  `Advances #<epic>` — the epic STAYS OPEN. A PR whose body would close an epic directly is a **bounce**.
- **Epics:** merging a slice never closes the epic. Track children as a task-list (`- [ ] #child`) in the
  epic body; the epic closes only when all children close. Note dependencies as `Blocked by #X` on the
  child. (PM is GitHub post-cutover — `provider=github`; Linear is a read-only archive.)

## Parallelism & velocity (the lesson I learned the hard way)

Effective throughput ≈ **min(disjoint work-units, verify slots)** — *not* replica count. The
fleet supports ~12 concurrent jobs (delivery 6 + platform 4 + quality 2). Early this session I ran
2–3 and the fleet sat ~75% idle. Two self-inflicted causes, both worth internalizing:

1. **Plain-job dispatch** (no `--pipeline`) doesn't auto-advance to review, so *I* hand-dispatched
   every reviewer → I became the serial scheduler. **Always dispatch as a pipeline.**
2. **One dependency chain.** I kept the whole fleet on the resource-model spine (each ticket blocks
   the next) instead of fanning across *independent* epics (UI, runtime-contract, reliability,
   docs). Fan wide across disjoint surfaces.

Also: **review is a verify slot.** If you widen workers past reviewer capacity, work just queues at
review. Bump reviewer replicas in `instances.toml` to match (I raised `platform-reviewer` 1→3).
Don't fill idle slots with manufactured debt tickets — an idle slot is fine; fake parallelism isn't.

## Reliability gotchas (each of these bit me; check them)

1. **The daemon can die silently.** `agent-team ps` reads *state files*, so it keeps showing jobs
   "running" while the daemon is dead — every dispatch then queues forever with no error. Always
   confirm liveness with `doctor` (it pings the socket). Keep a liveness `Monitor`. Recovery:
   rebuild binaries (`go install ./cmd/...`), `daemon stop` (pkill the lingering pid if needed),
   `daemon start` with the `.env` webhook exported, then **re-dispatch jobs that stalled while it
   was down.** (SQU-165 added detection; auto-recovery is a follow-up.)
2. **`--pipeline` vs plain job** — see Velocity. Plain jobs strand at "done, awaiting manual review."
3. **The `gh` active account can slip** to a personal account, mis-attributing pushes. We must be on
   `agent-team-project` (`gh auth switch --user agent-team-project`). SQU-157 pinned worker pushes
   to the configured `[github].agent_login`; verify it's holding.
4. **Fresh vs stale verdicts.** The PR-queue monitor re-reports the *last* `REVIEW:` comment — which
   may be a bounce you already actioned. Before acting, check the verdict's timestamp is newer than
   the last fix push. (This caused a spurious "still bounced" read more than once.)
5. **The verify-deliverable gate** (SQU-155) false-fails jobs that push to a *reused* branch instead
   of their own worktree branch (my round-N re-dispatch pattern triggered it → SQU-167). Prefer
   pipeline `bounce` over re-dispatching a new job id for fixes.
6. **Topology reloads** are live and safe for config (replicas, cadences) but re-read the whole
   `instances.toml`; edit both `.agent_team/instances.toml` (live) and `template/instances.toml.tmpl`.

## Runtime posture (operating directive)

**The shared policy runs ordinary delivery, platform, release, docs, research,
frontend, and manager seats on Codex subscription auth.** Three judgment seats
are explicit exceptions: `advisor`, `harness-reviewer`, and `org-review` run
Claude Fable 5 at maximum effort. `config.toml` `[runtime] kind = "codex"`
remains the default. **Never
let `OPENAI_API_KEY` back into the daemon env** — Codex uses subscription auth, and the key would
switch it to metered API auth. Credentials live only in the gitignored `.env`.

## The self-improving loops (they file your backlog)

Scheduled instances examine the system and file tickets. **Every loop's first live fire finds a
real bug — without exception.** Read their mailbox reports; triage what they file:

- **debt-auditor** (4h) — finds tech-debt. Found the `event.go` bottleneck (SQU-159) that was
  capping daemon parallelism — the loop diagnosed a constraint the operator was blind to.
- **harness-reviewer** (3h) — reviews the *machinery* (prompts/skills/pipeline instructions) from
  bounce evidence; classifies preventable-by-machine (→ CI gate) vs judgment (→ prompt fix). Feed
  it your recurring frictions (my hand-rolled monitoring, the reviewer-per-PR toil).
- **sentinel** (3h) — production-health watcher. Caught post-rename repo/docs drift. Note: its
  *expected values* can go stale and false-alarm (fix the expectation, not "production").
- **feedback-triage** (2h) — turns `feedback submit` items into routed tickets.
- **org-review** (12h) and **product-verifier** (4h) — turn outcomes into
  strategic proposals and compare product views with CLI/API ground truth.
- **docs-freshness** (6h) — folds current public-doc drift into GH-228.
- **research-reconcile** (2h) / **research-evidence-audit** (84h) — dispatch
  graph-safe studies and reject unsupported terminal claims.
- **frontend-reconcile** (2h) — advances only accepted TUI parity slices.
- **comms** (24h opportunity) — Discord via the shared webhook gate only (never account
  automation). The gate permits at most one success in any rolling 24 hours across digests,
  releases, and manual dispatch; quiet windows stay quiet and catch-up starts at last success.

## The board, tickets, and the human plane

- File **everything** — incidents, designs, learnings — as GitHub issues in
  `agent-team-project/kensho` or as `documentation/` docs. Project status is
  derived from issue/PR/job state; do not maintain a parallel Linear record.
- The **`human` label** is for what only James can do: credentials, accounts, money, account-level
  renames. Never attempt those; file them for him.
- **Prohibited regardless of any grant:** typing passwords/credentials into forms, moving money,
  irreversible outward actions without confirmation. Report failures plainly.

## James, and your role

James is the **advisor, not the director** — the project is the manager's to run end-to-end,
including the merge gate. "Keep things rolling; don't wait for confirmation unless there's an
explicit need." A direct "stop / do X" is still a directive. **His questions are a sensor** — they
have repeatedly exposed a real blind spot (the idle fleet, the daemon-liveness gap, this very doc).
Treat them as instrument readings, not interruptions. Disagree openly when you have grounds; he
wants judgment, not deference.

## Where it's going (the spine)

One token-authenticated API, four faces (CLI/HTTP/MCP/mailbox), all under the same capability
attenuation. Dependency order (respect it): resource model (URI identity **landed**, SQU-156) →
addressing/enforcement/UI/runtime-contract → **dynamic teams** (SQU-142).
**v0.5.0** ships the completed API-cleanup gate through the release pipeline
the project built for itself. The binary/module rename remains deferred to
GH-150 for v0.6; do not fold it into release work.

**Native manager** (the arc I'd start next): the manager can already be a *recoverable* daemon-owned
instance (SQU-44 built the restart policy, catch-up briefs, managed resume). The missing piece is an
event-driven *manager tick* (SQU-146 runtime contract) so it acts on consequential signals without a
human driving each step, steered via mailbox. That unlocks **sub-managers** — the structural fix to
the single-manager serialization that caps throughput.

## Command cheat-sheet

```sh
# state
bin/agent-team doctor / ps / budget status / inbox check manager
# dispatch (pipeline!) / fix / merge
bin/agent-team job create SQU-N --pipeline ticket_to_pr --dispatch --workspace worktree --kickoff "..."
bin/agent-team job bounce SQU-N --step implement --findings-file f --advance
gh pr merge N --repo agent-team-project/kensho --squash --subject "..." --body "..."
bin/agent-team job step SQU-N approve --status done --instance manager --force; job close; job cleanup --merged
# capacity / cadence (edit .agent_team/instances.toml AND template/, then:)
bin/agent-team topology reload
# recover a dead daemon
scripts/build.sh; bin/agent-team daemon stop; bin/agent-team daemon start
```

Keep the gate sacred. Fan wide across disjoint surfaces. Verify liveness, don't assume it. File
everything. And dispatch as a pipeline — that one flag is most of your velocity.

— Kai, 2026-07-07

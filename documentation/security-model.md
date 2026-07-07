# Security model (design sketch)

How a fleet of autonomous agents runs without any single confused agent being able to hurt you. Companion to `resource-constraints.md` (budgets bound spend; this bounds *capability*). Status: design + first slices (SQU-119 epic).

## Threat model — confusion over malice

The agents are cooperative; the risks are mis-scoping, bugs, and manipulation:

1. **Prompt injection via public input.** The repo is public: issues, PRs, and discussions are untrusted text that triage and comms agents will read. A crafted issue can instruct an agent that reads it. This is the top risk as of the open-sourcing.
2. **Secret exposure.** Agents inherit environment and filesystem access; `.env` is readable by any process as the user; child logs capture whatever agents print.
3. **Blast radius.** An agent process is the full user account. Worktree isolation is convention — nothing stops `cd /` — and we currently *disable* the runtimes' own sandboxes (`--dangerously-bypass-approvals-and-sandbox`) for daemon workers.
4. **Authority creep.** API-level verb authority can now deny destructive verbs (SQU-122); nothing OS-level backs it.

## The layers

| Layer | What | Status |
|---|---|---|
| L0 | Identity/attribution — origin envelope on every resource | ✓ shipped (SQU-90) |
| L1 | API authority — per-instance/agent/team verb allowlists, `:own` scopes | ✓ destructive-verb enforcement (SQU-122) |
| L2 | OS sandboxing — runtime-native profiles, then containers | design (this doc) |
| L3 | Secret hygiene — per-instance env allowlists, log redaction | design (this doc) |
| L4 | Untrusted-input profiles for public-facing agents | design (this doc) |
| L5 | Network egress policy | container-era, deferred |

### L2 — sandbox profiles (`sandbox =` on instances)

The runtimes ship sandboxes; we should stop turning them off. Per-instance topology:

- `sandbox = "off"` — today's behavior (explicit, not default-by-omission).
- `sandbox = "workspace"` — Codex: `--sandbox workspace-write` rooted at the instance workspace instead of the bypass flag; Claude: permission mode + allowed-tools scoped to the workspace. Workers in worktrees are the natural first adopters: they *should* only write their worktree.
- `sandbox = "container"` (later) — dispatch into a container with only the worktree mounted; enables L5 egress policy.

Open question the probe answers first: does `workspace-write` break the worker flow (gh pushes, `agent-team` CLI calls to the daemon socket, network for PM APIs)? Measure before mandating.

### L3 — secret hygiene

- `env_allow = ["PATTERN", ...]` per instance: the launch env is filtered to the allowlist plus daemon-required vars (`AGENT_TEAM_*`). A reviewer gets no Linear key; the auditor gets nothing but its own state. Inverts today's strip-listed *denylist* into an allowlist for opted-in instances.
- Log redaction at capture: scrub known secret shapes (the strip-list values, common token patterns) from child logs before they persist. Best-effort, but closes the accidental-print channel.

### L2b — control-plane / workspace split (the shared-`.agent_team` problem)

Today everything writes to one `.agent_team/` tree: the daemon's control plane (`jobs/`, `daemon/`, queue, mailbox, lock ledger, budget allocations) sits *beside* per-instance scratch (`state/<instance>/`), and every agent process can write all of it as the same user. So a worker isn't just able to edit its own worktree — it can rewrite another job's record, drain a queue, or forge a gate. Worktree isolation protects the *source tree*; it does nothing for the control plane. Two moves, cheapest first:

- **The daemon owns the control plane; agents reach it only through the API.** Agents already talk to the daemon over the unix socket for dispatch/gate/mailbox — the direct-filesystem paths into `jobs/`/`daemon/` are the ones to close. An agent's *filesystem* write surface shrinks to its own `state/<instance>/` and its worktree; everything durable and shared goes through authority-checked API verbs (which L1 already gates). This makes the CLI-surface concern and the directory concern the *same* fix: once the only way to mutate a job is an authority-checked verb, an unfiltered CLI can't do damage a filtered API wouldn't, and a rogue file write has nowhere to land.
- **OS-level backing (L2 sandbox).** `sandbox = "workspace"` then makes the filesystem *enforce* what the API design intends — the worker literally cannot write outside its worktree + state dir, so the split is guaranteed, not just conventional.

Sequencing note: this is why the sandbox probe (SQU-120) is the keystone — it measures whether `workspace-write` can hold while the agent still reaches the daemon socket. If yes, L1 (authority) + L2 (sandbox) + L2b (control-plane split) compose into real isolation; the CLI allowlist (SQU-123) is then defense-in-depth, not the only wall.

### L4 — untrusted-input profiles

Any instance whose job includes reading public content (community triage, comms intake) declares `input = "untrusted"`:

- env allowlist forced minimal (no PM keys — it files via a broker or the feedback store instead)
- workspace read-only; no push/merge/gate authority in L1
- prompt contract: external text is data, never instructions; instructions found in content are quoted back to a supervisor, not acted on
- their outputs (draft replies, ticket text) are reviewed by a *non*-exposed agent before any outward action

### Graduation discipline

Same as scoping: measure first (audit/probe), enforce second, default-on for new templates third. Nothing flips to enforced without observed evidence it won't break the fleet — the SQU-92 violation stream and the sandbox probe are the instruments.

## Sequencing

1. Probe: Codex `workspace-write` compatibility with the worker flow (probe profile job).
2. `env_allow` per instance (small, high value, independent).
3. `sandbox = "workspace"` for workers/reviewers, informed by the probe.
4. Untrusted-input profile before community triage goes live.
5. Log redaction; authority enforcement graduation; containers + egress last.

## Prior art & adopted decisions (research synthesis, 2026-07-05)

From a prior-art review (SPIFFE/SPIRE, systemd credentials, k8s SA/RBAC, Vault, capability systems, OPA/Cedar, MCP security guidance, CaMeL/dual-LLM, Copilot/Codex-cloud agent postures), five decisions, cheap→dear:

1. **Identity: the instance secret leaves env.** Daemon mints a 256-bit per-instance token at spawn, delivered as a 0600 file in the instance's private state dir (systemd-credentials shape) — env keeps only non-secret labels (`AGENT_TEAM_*`). Socket connects present the token; the daemon cross-checks `SO_PEERCRED`/`getpeereid` (kernel-attested locality; can't be the sole identity since all agents share a UID). Graduate to SPIFFE/SPIRE only if we go multi-host.
2. **Secrets: broker, don't distribute.** The daemon holds provider keys (Linear/GitHub/PM); agents get narrow daemon verbs (create_ticket, open_pr, comment) and provider keys are stripped from agent env entirely. Our socket+verb API already is a capability broker — this extends it. Also the confused-deputy fix: the daemon, not the agent, decides may-X-call-Y. Vault only when downstream creds must reach agent processes and multiply.
3. **Authz: capabilities over roles.** The per-instance token carries a narrow verb-set; managers hand spawned workers an attenuated (strictly-subset) capability. Enforced at the socket boundary. Hand-rolled allowlists stay right-sized; embedded Cedar (not OPA) only if policy ever becomes relational.
4. **Untrusted input: reader/actor split (structural, not prompt-level).** The instance reading public issues holds no credentialed write verbs and emits a fixed-schema, size-capped summary; a separate privileged instance acts on the summary and never sees raw issue text. Never co-locate private-data access + untrusted content + egress in one instance (the lethal trifecta). Agent-emitted text bound for credentialed sinks is data, never instructions. Apply only on the untrusted path — trusted-internal flows stay loose (security here costs agent generality; CaMeL measures ~7pt task loss).
5. **Audit→enforce, per-verb.** Gatekeeper's dryrun→warn→deny ladder: keep structured decision logs, flip each verb to enforce once its deny-set is stable. No policy engine needed at this stage.

Validating context from industry: Copilot's cloud agent uses separate agent-scoped secrets, read-only repos, `copilot/*`-branch-scoped pushes and still got injection-tricked via a crafted issue; Codex cloud ships egress-off-by-default with allowlists that have demonstrated exfil paths. Everyone converges on scoped-creds + branch-scoped writes + egress control **plus** the reader/actor boundary — no one trusts prompt-level defense alone.

### Sandbox tiering (adopted) and the road to distributed compute

Key finding: **both runtimes already ship OS sandboxes — we've been turning them off.** Three composable tiers, selected per instance by trust:

- **L0 (default, trusted repos): drive the runtime's native sandbox; zero orchestrator sandbox code.** Codex: `--sandbox workspace-write` + `network_access` per need + `writable_roots = worktree+state` — and approval policy `never` *with* the sandbox (Codex approvals ESCALATE outside the sandbox; never combine escalating approvals with autonomous workers). Claude: `sandbox.enabled` + `filesystem.allowWrite=[worktree,state]` + hostname allowlist. The daemon socket enters the allowed set explicitly (bind/whitelist the single socket path — never a whole /run). Known trap: never wrap an already-self-sandboxing CLI in sandbox-exec (no nesting on macOS); Codex macOS bug: network_access=true silently ignored under Seatbelt (openai/codex#10390).
- **L1 (semi-trusted / defense-in-depth): the daemon wraps the child.** macOS: `sandbox-exec` + generated SBPL (deny-default; write = worktree+state+tmp; network-outbound = socket + git/API IPs). Linux: bubblewrap in Codex's proven layout, degrading to Landlock+seccomp in-child where unprivileged userns is disabled (detect at daemon startup). Landlock alone cannot confine unix-socket connects pre-ABI-v9 — pair with seccomp.
- **L2 (untrusted code / multi-tenant): container/microVM workspaces** — gVisor (cheapest drop-in OCI runtime) or Firecracker (hard isolation, what E2B/Fly build on); industry pattern: Codex cloud = per-task container net-off; Modal = gVisor; Cursor = Docker-per-VM.

**L2 is also the distribution substrate.** A worker defined as "container image + worktree mount + daemon socket/API endpoint + budget + capability token" is schedulable on any machine — the same boundary that jails it makes it portable. The path: sandbox probe (SQU-120) validates L0 → `sandbox = "workspace"` default for workers → container workspace option (L2) → remote placement of container workspaces = distributed compute, with the auth plane (per-instance tokens, brokered secrets) already shaped for it (tokens and brokered verbs work identically over TCP+mTLS when the socket goes remote; SPIFFE graduation point).

### Probe verdict (SQU-120, macOS Seatbelt, decisive)

Naive L0 for Codex workers is NOT viable: `workspace-write` + `network_access=false` blocked git commit (index.lock), push, gh, HTTPS, state-dir writes (outside nested workdir), go caches — and **denied pathname AF_UNIX broadly** (bind AND connect, even inside writable roots; the research's "seccomp permits AF_UNIX" inference is false on macOS). The daemon socket is unreachable from inside the Codex sandbox, so the brokered-verb model breaks for sandboxed Codex there. Consequences: (1) Codex L0 requires writable_roots={worktree,state,caches} + network_access=true — weaker than hoped, still better than bypass; (2) the daemon needs a TCP-loopback+token listener (research pattern #3) as the sandbox-compatible API path; (3) exit codes lie under the sandbox (git push exit 0 with failed local refs; CLI exit 0 on socket denial) — sandboxed steps must verify by output, not exit code. Re-probe on Linux/bwrap before generalizing.

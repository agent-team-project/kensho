# Fractal architecture — how a Kensho contains Kenshos

**DESIGN DECISION** — 2026-07-09. Resolves the nesting question in #262 (fractal Kensho), drawing
on #255 (persistent agents), #155/#251 (dynamic teams / secure spawn), #202 (adaptive
concurrency), #223 (composable units), #261 (abstraction levels), and #263/#286 (intent-faithful
handoffs). Companion to `dynamic-teams.md` (the spawn substrate), `resource-constraints.md` (the
budget tree), `distributed-resources.md` (identity and transport), and `kensho-constitution.md`
(the intent model this must implement, not merely gesture at). Ratification per the amendment
process; open questions flagged in §9.

---

## 0. Context: what exists, and the question

Today each project is a **separate, flat, peer** control plane: one `agent-teamd` per repo, its
own `.agent_team/`, its own Unix socket plus loopback HTTP port (`127.0.0.1:0` by default in
`internal/daemon/daemon.go`; operator-pinned in practice — 8787 for this repo, 8788 for
excel-lite). Three peer Kenshos (kensho-project, chess, excel-lite) share one machine with **no
cross-daemon coordination**: each daemon runs its own adaptive concurrency controller
(`internal/daemon/concurrency.go`, target 0.85 load/core), each reads the same machine load, and
neither knows the other's in-flight dispatches. On 2026-07-08 two daemons collectively pegged the
box — the concrete forcing function recorded in #262.

James's framing: *"Kensho is many Kenshos working together — hierarchical at the moment, like a
hive mind"* — and a team could itself BE a Kensho. The question this document decides: **how does
Kensho become genuinely nested — a Kensho that contains child Kenshos — and is that the right
direction?**

One observation dissolves half the apparent difficulty. Kensho already has a recursion grammar
with three rungs:

```
instance  <  team  <  deployment (a Kensho)
```

Teams are **already namespaces inside one daemon**: topology teams own instances, pipelines,
schedules, and channels; budgets are keyed by team; locks and channels are scoped
`machine | team | job` (`topology.md`, `resource-constraints.md`). And deployments are **already
separate daemons** with a designed parent edge: `resource.Deployment` carries `ParentURI`, and the
dynamic-teams foundation (`internal/daemon/dynamic_team.go`) persists charters with
`parent_deployment_uri`, `child_deployment_uri`, granted-vs-requested budgets, attenuated
authority, TTL, and reap states. The two candidate models in §1 are therefore not alternatives to
choose between in the abstract — **both already exist, at different rungs.** The real decision is
where the Kensho boundary sits, and what a parent must own over a child.

## 1. The decision

**D1 — A Kensho is a deployment: one control plane, one daemon, one `.agent_team/` root. Nesting
is parent-spawns-child daemons (model B), not namespaces inside one daemon (model A).**
`deployment = daemon = agt:// URI authority` stays an identity, exactly as the resource model
already assumes.

**D2 — Namespaces are not rejected; they are the rung below.** A team is the in-daemon namespace
tier. Model A "wins" at the scale where it already exists: work that shares the parent's repo,
lifecycle, and failure domain is a **team** (or a chartered dynamic team), not a child Kensho.
A team is promoted to a child Kensho only when it needs its **own control plane**: own repo or
root, own placement, own restart/upgrade cadence, own gates and backlog.

**D3 — Two orthogonal trees, never conflated.** The **budget/authority tree follows the intent
graph** (parent charters child, grants spend and capability). The **machine scheduler follows
placement** (a host-scoped governor arbitrates physics — CPU, build slots — for every daemon on
the box, peer or nested, parent or child). Making the parent the machine scheduler is the trap:
it breaks for peers with no parent (today's reality) and breaks for children placed on other
machines (tomorrow's). Today's contention is a **host** problem and gets a host fix (§3.2),
independent of nesting.

**D4 — Nest only along the intent graph.** A child Kensho exists when its goal is a sub-goal of
the parent's backlog — when the parent authors its charter as part of the parent's own delivery.
Today's three Kenshos stay **flat**, because their intent parent is James, not kensho-project:
nesting chess under this repo's Kensho would falsify the intent graph the constitution is built
on (§I: intent originates at the top and is transmitted at every layer). Peers that share only a
machine share a governor, not a parent.

**D5 — Depth is capability-bounded and starts at 2.** Per #261, recurse only where the work
requires the split; better models flatten the tree. Operationally: parent → child, with
grandchildren expressible only as teams inside the child, until the outcomes ledger shows a real
case for depth 3.

## 2. The two models, head-to-head

| Dimension | (A) One daemon, first-class namespaces | (B) Parent-spawns-child daemons |
|---|---|---|
| Identity model | Breaks `deployment = daemon`; `agt://<deployment-id>/...` URIs, origin envelopes, and `[project].id` all assume one control plane per deployment | Preserves the shipped resource model unchanged |
| Failure domain | One: a wedged queue store, corrupt channel ledger, or daemon bug degrades every Kensho at once; upgrades restart the fleet | Per-Kensho: crash-only reconcile per daemon; children pin versions independently |
| Recursion | Namespaces-in-namespaces: every store, verb, and view grows a scope parameter; the daemon becomes a multi-tenant kernel — against "minimal surface, one responsibility" | Free: a child spawning a grandchild uses the same verb, binary, and CLI. Self-similarity is literal |
| Placement | Host-bound forever; cannot express a containerized or remote child | A child daemon runs same-host, in a container (L2 sandbox tier), or remote — same parent edge, same URIs (`dynamic-teams.md` placement) |
| Scheduler | Trivially global — but only for Kenshos that merged; peers outside the daemon still contend | Needs a shared governor — which is needed anyway for flat peers (§3.2) |
| Budget accounting | Atomic, one ledger | Hierarchical: parent ledger authoritative for **grants**, child ledger authoritative for **spend within grant**; no cross-process transaction needed (§3.1) |
| Security | One token authority; one namespace-scoping bug leaks across all Kenshos | OS-level separation of state, tokens, secrets per Kensho; a confused child is bounded by its grant |
| Ops overhead | One process | N daemons — real cost, bounded by parent supervision (§3.4) and by D2 (don't nest what a team can do) |

### Pressure-testing B (the prior lean)

The honest case for A, and why each argument loses:

1. **"A fixes contention with one scheduler."** Only for whatever merged into the one daemon.
   The peers that exist today have independent intent sources and per-repo control planes;
   merging them into one process to fix a load problem is using an authority mechanism to solve
   a physics problem. The host governor (§3.2) fixes contention for peers *and* nested children,
   at a fraction of the surface.
2. **"A gives atomic budget accounting."** Budgets are admission governors, not correctness
   invariants (`resource-constraints.md`: admission over interruption). The tree invariant
   decomposes hierarchically: each node enforces locally against its grant, so a child can never
   overspend what the parent allocated even when the parent is unreachable. Eventual rollup of
   consumption is acceptable exactly because the grant is enforced at the edge. `reserve`-mode
   allocation (SQU-106, live in `internal/budget/allocations.go`) bounds outstanding promises at
   grant time.
3. **"A gives one pane of glass."** True, and B must pay for aggregation — but this is CLI work
   (`fleet ps` over the deployment registry), not control-plane work. A pays the mirror cost in
   the kernel: every existing view, verb, and store grows namespace plumbing. B's cost sits in
   the safest place to pay it.
4. **"N daemons is operational sprawl."** The genuinely strongest objection. Answered three
   ways: child daemons are parent-*supervised* (reconcile owns their lifecycle, §3.4), so
   operator burden does not scale with N; D2 caps N by refusing to nest what a team can do; and
   a Go daemon with file-backed state is a cheap unit — the marginal cost is a process and a
   directory, not a service.

And B's real weaknesses, stated so they are designed for rather than discovered: parent/child
partial failure (child daemon dead while parent believes it running — same `kill(pid,0)` probe
semantics extended across the deployment edge, TTL as backstop); capability-token lifecycle
across restarts; version skew between parent and child binaries (the versioned `/v1` API is the
contract; the registry records capability flags per deployment). None are novel — they are the
standard price of process isolation, and Kensho's crash-only design was built to pay it.

**Decision: B.** The clincher is not any single row but the compounding: B preserves the shipped
identity model, inherits the dynamic-teams substrate nearly verbatim, is the only model that
reaches containers and second machines, and makes the fractal claim *true* — every node really is
a complete Kensho with the same interface, rather than a tenant in a shared kernel.

## 3. The coordination layer — what a parent owns over its children

Four things, and only four. Everything else the child owns itself — that is what makes it a
Kensho.

### 3.1 The budget tree

The ledger shape already anticipates this. `AllocationRecord` in `internal/budget/allocations.go`
carries explicit `Parent`/`Child` fields with the comment *"future nested teams can reuse the
same ledger shape"*. Extension across the deployment edge:

```
operator (James)
  └─ parent Kensho operator budget
       ├─ parent team budgets ─ job/step allowances        (today, in-daemon)
       └─ child-Kensho grant (charter, reserve-mode)       (new edge)
            └─ child Kensho operator budget = the grant
                 └─ child team budgets ─ child job allowances  (same machinery, one level down)
```

- **Grant at charter.** The charter's `requested_budget` → `granted_tokens` flow already exists
  in `TeamCharterBudget`. A child-Kensho grant is `reserve`-mode by default: it debits parent
  headroom at spawn, so outstanding promises are bounded even if the child is never heard from
  again.
- **The grant is the child's whole world.** The child daemon boots with its operator-level
  budget set to the grant. Its internal subdivision (teams → jobs → steps) is its own business,
  enforced by its own admission checks — the parent never reaches inside.
- **Rollup, not surveillance.** The child reports consumption to the parent at interval and at
  reap through a report resource / outbox verb (the `dynamic-teams.md` report flow). The parent's
  `budget status` rolls child allocation and spend into the tree. Hard enforcement at the parent
  edge is exactly one verb: **reap** — kill the child deployment when the grant is exhausted,
  with the same watchdog semantics as SQU-105 hard cutoffs.
- **Denominate grants in dollars, not tokens**, per `model-economy.md` §9 — a child free to
  choose its own model tiers inside a token-denominated grant can silently 10× real spend.

This is also where a constitutional guarantee becomes mechanical: the entrenched core's hard-stop
on large sums (§VI.3) is *enforced by construction* at every depth — no layer can spend what its
parent never granted, however confused or prompt-injected it gets.

### 3.2 The shared scheduler — a host governor, not a parent power

Fixes today's contention, and deliberately does **not** live on the parent edge (D3).

- **Mechanism:** a host-scoped dispatch-slot ledger at `~/.agent-team/host/` (uid-scoped):
  lease files carrying `{deployment_id, pid, load_weight, ts}`, crash-safe via the same
  pid-probe used by reconcile. This is the machine-scoped-lock idea already in
  `resource-constraints.md` ("some resources are naturally machine-scoped — two teams SHOULD
  share build slots") promoted from per-repo to per-host.
- **Integration:** each daemon's `concurrencyController` admission check adds one term — admit
  when *(host in-flight weight + incoming) ≤ host ceiling* — alongside its local ceiling. The
  controllers already sample load1/cores; the ledger fixes what load-sampling cannot: load1 lags
  dispatch, so N independent controllers all admit into the same dip. Queue-don't-fail semantics
  are unchanged (`reason=host_saturated` joins `budget_exhausted`).
- **What the parent does own, scheduler-wise:** a *ceiling attenuation* in the charter — the
  child's `max_ceiling` and `load_weight` are capped at grant time, so a child cannot configure
  itself into hogging the host. Priority classes propagate: a parent incident dispatch may
  preempt a child batch job (kill + requeue is already the preemption model).
- **No new component.** No `hostd`, no election. Pure file leases; every daemon is a peer of the
  ledger. If a daemon dies its leases go stale and are reaped by pid-probe.

This stage ships value with **zero nesting**: it is the "lightweight shared resource-governor"
that makes staying flat viable (§7), and it is a prerequisite for nesting. Build it first either
way.

### 3.3 Resource namespacing

Already solved by the resource model — the deployment id is the namespace:

- **URIs:** `agt://<child-deployment-id>/job/...`, `/mailbox/...`, `/channel/%23blocked` are
  scoped by construction (`distributed-resources.md`). No renaming, no prefixes.
- **Mailboxes and channels** are per-daemon files, so isolation is the default. Cross-boundary
  communication is explicit: the deployment registry (`~/.agent-team/deployments.toml`, SQU-127)
  resolves a deployment name → transport endpoint + token file, and feedback routes
  (`internal/feedback/routes.go`, route v2 `type = "deployment"`) are the designed wire for
  cross-deployment delivery.
- **Names:** child deployment ids are minted at charter (`ChildDeploymentID` in the charter
  record today); human aliases live only in the registry. Sibling children never collide because
  they never share an authority.

### 3.4 Lifecycle — the parent spawns and reaps

The charter state machine in `dynamic_team.go` (`chartered → spawning → running → draining →
reaped | failed`) is the contract; what changes is what "spawn" materializes:

- **Spawn** renders a full child `.agent_team/` from a curated template (`agent-team init` of a
  template ref into the child root — the templates-as-images machinery, unchanged), writes the
  child's `config.toml` with `[project].id` + parent URI, mints tokens, and starts `agent-teamd`
  in the child root.
- **Supervise** reuses recoverable-managers machinery one level up: the child *daemon* is a
  supervised child of the parent's reconcile — restart policy, backoff caps, health probe via
  the child's `/v1` status. A parent restart adopts surviving child daemons exactly as reconcile
  adopts surviving instance PIDs today.
- **Reap** (TTL, goal-complete, grant-exhausted, parent cancel, authority violation): stop the
  child daemon, revoke tokens, collect the final report and usage, release unspent reserve,
  archive per retention, leave a tombstone resource. Reap removes live capability, never audit
  history (`dynamic-teams.md` design constraint).

## 4. Intent propagation across the nesting

The constitution's structure — *James → Kan → Kai → workers*, authority flowing down, intent
transmitted-never-authored at each layer (§I, §II.3) — maps onto nesting with one instrument:

**The charter is the constitutional interface between Kenshos.** It is append-only intent
(`dynamic-teams.md`: reconcile may add status, never rewrite requested scope): goal, success
criteria, granted budget, granted authority, lifecycle. The child cannot amend its own charter —
that is corrigibility made structural, not prompted.

```
James ──charters──► top Kensho   (Kan beholds, Kai delivers)
                        │
              parent Kai ──charters──► child Kensho   (child Kai delivers)
                        ▲                    │
                        └── escalations ◄────┘
```

- **The parent's Kai holds the human *position* for the child** — precisely the #262 refinement:
  "a layer holds the human position, not the human authority." Parent Kai curates what flows into
  the child (the charter; subsequent vetted backlog items transmitted through a deployment
  feedback route into the child's intake), answers the child's questions, and reviews its
  reports. It is simultaneously agent (to its own layer above) and principal (to the child) —
  the dual role that #262 identifies as the load-bearing structure of scaling.
- **Where the child's Kai comes from:** its template roster. The child's manifest declares a
  persistent `manager` instance (restart policy, generated brief, managed resume — all landed
  machinery), and the charter-derived kickoff brief is its founding document. The child's Kai is
  born knowing: whose intent it transmits, what done means, what it may spend and touch, and
  where to escalate.
- **Where the child's Kan comes from — rule:** *Kan is instantiated per persistent Kensho;
  ephemeral children borrow the parent's oversight.* A goal-scoped, TTL'd child does not carry a
  full overseer — the parent's Kai (and above it Kan) is its oversight, and the charter's
  success criteria are its ratification surface. A durable child Kensho (a sub-product with its
  own long-lived backlog) declares its own Kan in topology, whose ratification authority is —
  like everything else — an attenuated delegation recorded at charter time.
- **Escalation composes:** child-worker → child-Kai → parent-Kai → Kan → James (§IV.2 extended
  by one hop per nesting level). Each layer resolves what it can from its own context. The
  two-way clarification channel (#286) exists at every seam by construction: the child's mailbox
  and the parent-visible channel are the wire; a charter is a spec, and specs are lossy (§II.1),
  so asking upward is the mechanism, not a failure.
- **The constitution inherits downward; the entrenched core never attenuates.** A child's
  constitution is rendered at init from the parent's, with charter-scoped addenda. The evolvable
  body may be specialized; §VI.3 is carried verbatim at every depth — intent originates with the
  human; every layer can be corrected or halted by the layer above it; hard prohibitions
  escalate to the human. Mechanically: the budget tree enforces the money hard-stop (§3.1), reap
  enforces haltability, and the append-only charter enforces transmitted-not-authored intent.

The alignment risk that grows with depth is *attenuation compounding*: the tower is only as
aligned as its weakest handoff (#263), and each nesting level adds one. This is the deepest
reason for D5's depth cap and for §7's bias against nesting — not implementation cost, but
intent fidelity.

## 5. Attenuation and security — the child as a strict capability subset

The dynamic-teams work (#155, foundation landed; #251 secure-spawn) is the substrate, and its
invariants transfer whole:

1. **Attenuation is computed by the daemon, never asserted by the caller.** The spawn admission
   sequence (origin resolution → template authorization → budget grant → capability
   intersection → provenance stamp → manifest render → queued reconcile) already exists as the
   designed flow; the charter records requested-vs-granted with per-denial reasons.
2. **A child token carries only the intersection** of requested verbs/resources with the
   creator's effective capability, further capped by template, TTL, and placement. A child
   Kensho's daemon mints its *own* instance tokens internally — but everything it can mint is
   bounded by its deployment grant. Attenuation composes down the tree.
3. **Templates gate authoring.** A parent Kai instantiates curated child-Kensho templates with
   declared parameters; free-form child constitutions/prompts are deferred to the reviewed
   template-proposal workflow. This is a security boundary, not a UX preference.
4. **External authority attenuates too.** A child receives brokered secret handles or
   child-scoped provider credentials (a GitHub token scoped to the child's repo), never the
   parent's raw `.env`. The per-repo `.env` convention is already a de facto attenuation between
   peers; the broker makes it deliberate for children.
5. **Honesty about today's boundary.** Live spawn is feature-flagged off
   (`AGENT_TEAM_DYNAMIC_TEAM_SPAWN_ENABLED`; `ErrDynamicTeamSpawnDisabled`) precisely because L1
   verb authority without L2 filesystem enforcement is advisory against a confused agent
   (`security-model.md`: same OS account, direct file writes bypass L1). A same-host child
   Kensho today is *blast-radius control for cooperating agents*, not containment. Real
   containment arrives with `sandbox = "container"` placement — which model B uniquely supports:
   a child Kensho in a container with a mounted workspace lease, a daemon endpoint, and a token
   file is exactly the L2 shape `security-model.md` and `distributed-resources.md` converge on.
   The secure-spawn gate (deny-by-default, resource-scoped attenuation across every daemon and
   CLI verb, including scoped reads) must complete before the flag flips — unchanged by this
   decision, and now on the critical path for nesting.

## 6. What's needed to build it

Ordered so every stage ships standalone value; earliest stages are nesting-independent.

| # | Stage | Reuses | Genuinely new |
|---|---|---|---|
| 0 | **Host resource governor** (§3.2): uid-scoped slot-lease ledger + one added term in `concurrencyController` admission. Fixes the live contention incident for today's flat peers. | Concurrency controller, pid-probe liveness, queue-don't-fail | The `~/.agent-team/host/` lease ledger (small) |
| 1 | **Deployment registry + tokenized transport** (SQU-127 / `distributed-resources.md` phases 1–2): names → endpoints + token files; capability tokens on loopback HTTP everywhere. | Loopback HTTP + bearer auth (shipped), `[project].id` | Registry file + handshake; capability-token claims |
| 2 | **Charter → child-Kensho materialization**: extend the landed `team.spawn` foundation so a charter renders a full child `.agent_team/` (template init) in a target root and starts/stops `agent-teamd` there. | Charter store + state machine (`dynamic_team.go`), templates-as-images init, restart/backoff from recoverable-managers | `deployment` as a reconciled resource kind: child-daemon spawn/health/adopt/reap in parent reconcile |
| 3 | **Cross-daemon budget rollup**: child usage reports → parent allocation ledger; `budget status` tree view; reap-on-grant-exhausted. | `AllocationRecord` parent/child ledger, reserve mode (SQU-106), usage-at-reap capture (SQU-73), outbox | Report/rollup verb + dollar-denominated grants (`model-economy.md` §9) |
| 4 | **Intent plumbing**: charter-derived founding brief for child Kai (brief generation exists); parent→child backlog transmission and child→parent escalation over deployment feedback routes; constitution inheritance at init (entrenched core verbatim, digest recorded in the charter). | Brief generation (`internal/daemon/brief.go`), feedback routes, mailbox/channels, template rendering | Constitution-inheritance render rule + charter digest check |
| 5 | **Secure-spawn completion** (#251): deny-by-default resource-scoped attenuation across all daemon and CLI surfaces; then the spawn flag turns on. | L0/L1 (shipped), attenuation computation (landed foundation) | Uniform resource-audit on scoped read verbs (the stated gap) |
| 6 | **Placement**: containerized child Kenshos (L2), then remote hosts (mTLS per `orchestrator.md`'s transport note), each host running its own §3.2 governor. | Workspace leases, sandbox tiers, docker runtime (SQU-131 landed) | Container/remote deployment placement in spawn |

Nothing in stages 0–4 requires abandoning or dual-pathing an existing surface; the pre-v1 rule
(no compatibility shims) holds — where a path-shaped identity blocks a stage, replace it with the
URI outright per the `distributed-resources.md` migration plan.

## 7. When not to nest

Nesting has real failure modes. Name them before choosing it:

| Failure mode | Shape | Mitigation / when it says "don't nest" |
|---|---|---|
| **Intent attenuation compounding** | Each level is a lossy spec handoff (#263); depth multiplies drift | Two-way channels at every seam; D5 depth cap; if a goal can be expressed as a well-scoped ticket, it wants a *worker*, not a child Kensho |
| **Governance overhead per node** | Every Kensho carries a daemon, a Kai, gates, loops — pure overhead when the work didn't need a split (#262: recursion is capability-bounded) | D2: teams first. A stronger model at the parent flattens the tree (#261) — decompose the model before decomposing the org |
| **Zombie children / reap debt** | The 244-dead-state-dirs problem (#255) at deployment scale: orphaned daemons, stale grants, leaked tokens | TTL mandatory on ephemeral charters; reserve-mode grants auto-release at reap; tombstones over silence |
| **Distributed partial failure** | Parent believes child healthy; child daemon wedged; reports stale | Health probes + TTL backstop; crash-only on both sides; a child that cannot be probed is drained, not trusted |
| **Observability fragmentation** | N daemons, N event logs, no single pane | Registry-driven `fleet` aggregation before deep trees are allowed to exist |
| **Premature authority theater** | Nesting before secure-spawn completes gives the *appearance* of containment with prompt-discipline reality | The spawn flag stays off until stage 5; same-host children are cooperation, not isolation — say so |

**The honest case for staying flat-peer longer.** Today's actual pain — machine contention — is
fully solved by stage 0, a shared file-lease governor with no nesting at all. Today's actual
topology — three projects whose intent comes directly from James — is *correctly* flat under D4:
there is no parent Kensho whose backlog those projects serve, so building the parent edge for
them would be architecture for its own sake. Flat-peer + host governor is sufficient as long as
all of the following hold: every Kensho's charter comes from a human; no Kensho needs to spend
another's budget; and the only shared resource is the machine. The nesting machinery earns its
complexity the day a Kensho needs to charter a sub-organization as part of its own delivery — a
sub-product, a migration campaign, an experiment fleet with its own gates — and per D2, only
after a plain team demonstrably can't hold that shape.

**The decision rule, compressed:**

- Shares only *physics* (a machine) → **peer daemons + host governor** (stage 0).
- Shares *intent and control plane* (same repo, same lifecycle) → **team / dynamic team**.
- Shares *intent* but needs its own control plane (own root/repo, placement, cadence, gates)
  → **child Kensho** (model B, chartered, attenuated, budgeted, reaped).

## 8. Decision summary

1. Nesting model: **parent-spawns-child daemons** (B). A Kensho is a deployment; deployment =
   daemon = URI authority. Namespaces-in-one-daemon rejected as the Kensho boundary — teams
   already are that tier, one rung down.
2. Parent owns exactly four things over a child: **budget grant** (reserve-mode, dollar-
   denominated, enforced at the edge, reaped on exhaustion), **charter** (append-only intent +
   attenuated capability + ceiling caps), **lifecycle** (spawn/supervise/reap via reconcile),
   and **oversight** (escalation target, report review). Everything else the child owns.
3. Machine scheduling is **host-scoped, not parent-scoped**: a uid-scoped slot-lease ledger all
   daemons consult — the fix for today's peer contention and a prerequisite for nesting.
4. Intent propagates by **charter**: parent Kai holds the human position (not authority) for the
   child; child Kai comes from the child's template; Kan instantiates only in persistent
   children; the entrenched core inherits verbatim at every depth and is mechanically backed by
   the budget tree (money), reap (halt), and append-only charters (transmitted intent).
5. Build order: host governor → registry/transport → charter-materialization → budget rollup →
   intent plumbing → secure-spawn gate → container/remote placement. Stages 0–1 ship value with
   zero nesting.
6. Bias: **flat until intent nests.** Today's three peers stay peers under a shared governor;
   the first real child Kensho is chartered when a parent's own backlog demands a
   sub-organization a team cannot hold.

## 9. Open questions

- **Kan at depth.** Does oversight instantiate per persistent child Kensho, or does one Kan
  behold many (an overseer whose scope is the subtree)? Per-child Kan preserves self-similarity;
  aggregated Kan preserves scarce top-tier judgment (`model-economy.md` — Kan-work is T0-shaped).
  Leaning per-child-declared but aggregation-friendly; decide on first persistent child.
- **Rollup cadence and unreachable children.** What report interval, and how long may a child be
  unprobeable before drain? TTL is the backstop; the interval is a policy knob that needs field
  evidence.
- **Constitution inheritance mechanics.** Is a rendered-constitution digest in the charter,
  verified at child boot, enough — or does the child daemon need to refuse to start when its
  constitution's entrenched core diverges from the parent's record?
- **Sibling mesh.** #262's hierarchy→mesh trajectory: do sibling Kenshos talk directly
  (data-plane channels via the registry) with parent-mediated authority grants, or is all
  cross-child traffic parent-routed? Leaning direct-data / parent-authority, consistent with the
  capability model — but the mesh should be evidence-driven, not built ahead of need.
- **Same-repo child Kenshos.** D2 routes same-repo work to teams; is there a real case for a
  child Kensho sharing the parent's repo through workspace leases (e.g. an isolated security-
  response org on the same codebase)? Possible under the lease model; deferred until asked for.
- **Host governor on shared machines.** The uid-scoped ledger assumes one operator per account;
  multi-user hosts would need OS-level arbitration that is out of scope for now.

---

*Ratification: this document decides architecture direction (§VI evolvable body — Kan ratifies).
Nothing here amends the entrenched core; §4 exists to show the core is preserved, mechanically,
at every depth.*

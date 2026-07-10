# Terminal UI specification

Status: accepted architecture contract for [GH-383](https://github.com/agent-team-project/kensho/issues/383), the specification slice of [epic #153](https://github.com/agent-team-project/kensho/issues/153).

This document freezes the product, interaction, package, verification, and
cutover contracts for Kensho's terminal-first human interface. It does not
authorize implementation or removal work. In particular, the embedded web
dashboard remains intact until the independently verified parity slice and the
manual cutover gate described below.

`MUST`, `MUST NOT`, `SHOULD`, and `MAY` are normative. The machine-readable
companion is [`parity.yaml`](./parity.yaml). The dependency decision is
[`ADR-001-charm-stack.md`](./ADR-001-charm-stack.md).

## Product intent

`agent-team ui` is a calm, keyboard-complete command center over the daemon's
HTTP/JSON control plane. Its first obligation is deliberately smaller than its
eventual ambition:

1. reproduce every capability actually present in the embedded dashboard;
2. prove that parity through deterministic frames, PTY flows, and a seeded live
   daemon;
3. remove the web surface in one manually approved PR; and
4. grow the command center only after the TUI is the sole human UI.

This ordering is the core product decision. Dashboard parity is a small surface
over the current daemon API. Work/gates/evidence, logs and attach, activity,
research, requirements, and release operations are new product. They MUST NOT
extend the period in which two human interfaces can drift.

The TUI is a frontend, not a second control plane. It renders typed daemon API
responses. It MUST NOT import daemon-internal storage structs, parse CLI output,
or read daemon-owned runtime state from disk. Repository artifacts already read
by existing CLI verbs, such as study reports and documentation, MAY use a shared
artifact reader after their screen slice defines that boundary.

### Non-goals for the parity and cutover phase

- No daemon API behavior change is required.
- No action mode or other mutation is required.
- No compatibility redirect, browser shim, or parallel web UI is permitted.
- No work may modify or depend on GH-381 or RESEARCH-001.
- No terminal box-and-line requirements graph is required; the first graph view
  is a tree with focus-expand and a reverse-dependency list.

## Current-dashboard ground truth

The parity inventory was derived directly from:

- [`internal/daemon/ui/index.html`](../../internal/daemon/ui/index.html), which
  declares the controls, twelve summary tiles, org view, and nine tabular
  sections;
- [`internal/daemon/ui/app.js`](../../internal/daemon/ui/app.js), which derives
  the rendered data, status/error states, refresh behavior, and resource
  enrichment;
- [`internal/daemon/ui/app_test.mjs`](../../internal/daemon/ui/app_test.mjs),
  which fixes the model/tier and bounce-class semantics;
- [`internal/daemon/http.go`](../../internal/daemon/http.go), which registers the
  routes; and
- [`internal/daemon/http_auth.go`](../../internal/daemon/http_auth.go), which
  exempts the static shell from TCP bearer authentication while keeping data
  routes gated.

The dashboard has three primary collection requests:

- `GET /v1/instances`
- `GET /v1/jobs`
- `GET /v1/topology`

It also discovers resource URIs in the instance/job collections and performs
zero or more `GET /v1/resources?uri=...` enrichment requests. That fourth route
shape supplies richer state, model/tier and bounce outcome data, deployment
charters, and deadlines. “Three endpoints” in planning shorthand means three
primary collections; parity MUST include the resource enrichment behavior.

The dashboard is read-only. Its only user operations are bearer-token entry,
connect/refresh, a five-second auto-refresh toggle, scrolling, and responsive
browser layout. Manual token entry and browser session storage are explicitly
dropped in the TUI because the shared daemon client performs existing token and
transport discovery automatically. Every data and observability capability is
covered. See `capabilities` in [`parity.yaml`](./parity.yaml) for the row-level
contract.

## Cut over early, then grow in sole possession

The early-cutover sequence is serial:

1. **Specification:** this contract, parity inventory, and ADR merge after
   independent verification and review.
2. **Shared client seam:** mechanically extract the existing CLI daemon client
   to `internal/daemonclient`, then move CLI callers onto it without behavior
   change.
3. **Walking skeleton:** ship the shell, navigation, overview, canonical golden
   harness, `--once`, connection honesty, and seeded-daemon profile.
4. **Parity and cutover:** add instances, jobs/telemetry, and org/topology
   screens; prove every parity row; then, in the same manually approved PR,
   delete the embedded web surface and turn on permanent anti-regression lints.
5. **Sole-TUI growth:** add work detail, logs/attach, activity, release,
   research, and requirements screens as independent issues.
6. **Action mode last:** add mutations only after the read-only UI has soaked and
   daemon-authority capability checks have an operator-safe presentation.

The web dashboard MUST NOT be removed in steps 1–3. The cutover PR MUST satisfy
the complete oracle in [`parity.yaml`](./parity.yaml), and James MUST approve the
exact head commit manually. A green machine gate without that approval is not
sufficient.

## Information architecture

The shell owns one navigation grammar and one model. A “screen” below is a
logical route, not a separate program or state store.

| Route | Screen | Phase | Purpose and principal data |
| --- | --- | --- | --- |
| `overview` | Overview | walking skeleton | Connection freshness, fleet/work summary, active problems, and shortcuts. Current parity tiles appear here. |
| `work/jobs` | Jobs | parity | Job identity, ticket, state, pipeline, model/tier/runtime, bounces, owning instance. |
| `work/telemetry` | Model and bounce telemetry | parity | Recent-24-job model/tier, active-job, and bounce-class aggregates. |
| `work/detail` | Work, gates, evidence | sole-TUI growth | Pipeline steps, gates, approvals, evidence, PR, audit timeline, and recovery hints. |
| `fleet/org` | Live org | parity | Declared lanes joined to runtime instances, grouped by role, with capacity, schedules, phase, lifecycle, and latest work. |
| `fleet/instances` | Instances | parity | Current instance table and later instance detail/mailbox links. |
| `fleet/topology` | Topology | parity | Deployments/charters, pipelines, budgets, schedules, deadlines, and teams. Sections are focusable tabs in compact/standard layouts and simultaneous panes in wide layout. |
| `logs` | Logs and attach | sole-TUI growth | Redacted log viewport, follow state, instance selection, and PTY handoff to existing attach behavior. |
| `activity` | Events and inbox | sole-TUI growth | Lifecycle events, channels, direct inbox, unread/freshness state, and trace/origin. |
| `release` | Release | sole-TUI growth | Release readiness, artifact/check status, and handoff links. |
| `research` | Research | sole-TUI growth | Read-only study/report index and evidence. RESEARCH-001 remains an external producer. |
| `requirements` | Requirements | sole-TUI growth | Tree, focused expansion, reverse dependencies, evidence links, and experiment state. |

Overview, Jobs, Instances, Live org, and Topology together cover the old
dashboard. Later routes MUST NOT be prerequisites for the cutover.

### Shell regions

Every interactive frame has these stable regions, in order:

1. title and connection/freshness banner;
2. primary navigation;
3. screen-local filter/query line when active;
4. content panes;
5. one-line status/command feedback; and
6. footer key hints.

The title, connection state, selected route, focused control, and exit path are
always visible at 80×24 or larger. Content may paginate or move behind a local
tab; controls MUST NOT become unreachable.

## Layout and canonical wireframes

Layout is a pure function of terminal dimensions and model state. Size-class
predicates are evaluated in the following order; the first match wins:

| Order | Class | Predicate | Canonical geometry | Behavior |
| --- | --- | --- | --- | --- |
| 1 | too-small | width `< 60` or height `< 16` | none | Stable diagnostic with current/required dimensions plus Help and Quit. No content layout is attempted. |
| 2 | compact | width `< 100` or height `< 27` | 80×24 | One content pane. Top-level routes use a one-line tab strip; topology subsections and detail panes are reached by tab cycling. Dense tables become priority columns plus a focused-row detail block. |
| 3 | standard | width `< 145` or height `< 40` | 120×30 | Main pane plus optional right detail pane. Full parity table columns render when they fit; overflow becomes a detail block, never horizontal scrolling. |
| 4 | wide | otherwise | 160×50 | Navigation rail, main pane, and contextual right pane. Topology may show two simultaneous sections. |

This ordered classifier is total and mutually exclusive: every non-negative
`(width, height)` has exactly one class, and every supported geometry (anything
not `too-small`) has exactly one content layout. The mixed-aspect boundary
vectors are normative regression cases:

| Geometry | Required class | Boundary exercised |
| --- | --- | --- |
| 59×50 | too-small | minimum width |
| 60×15 | too-small | minimum height |
| 60×16 | compact | smallest supported geometry |
| 99×50 | compact | narrow and tall |
| 100×26 | compact | wide enough, short |
| 100×27 | standard | compact-to-standard corner |
| 100×40 | standard | standard width, tall |
| 120×50 | standard | canonical width, tall |
| 144×40 | standard | last standard width |
| 145×27 | standard | wide width, minimum standard height |
| 145×30 | standard | wide width, medium height |
| 160×30 | standard | very wide, medium height |
| 145×39 | standard | last standard height |
| 145×40 | wide | standard-to-wide corner |
| 160×50 | wide | canonical wide geometry |

The too-small frame MUST NOT panic, emit partial escape sequences, or silently
drop input. Resize reflows immediately; focus remains on the same semantic item
when it still exists, otherwise it moves to the nearest surviving item in
stable sort order.

The following are structural wireframes, not snapshot test outputs. Every row
inside each `text` fence is normative terminal-cell data: one ASCII character is
one cell, there are no tabs or implicit margins, and the row count and width
MUST equal the geometry in its heading. A documentation check counts those
source rows and cells directly. It also checks every multi-pane junction for its
full inclusive source-row span: the 120×30 Jobs/Detail junction is column 70 on
rows 5–14; the 160×50 Live-org/Context junction is column 105 on rows 3–13; and
the wide Pipelines/Budgets, Schedules/Teams, and Deployments/Deadlines junctions
are column 73 on rows 14–19, 20–24, and 25–47 respectively. Every listed row,
including pane headers, content, separators, and closing rows, MUST contain `|`
or `+` at exactly that column and MUST NOT shift to either adjacent column.
Canonical goldens use the same regions and the exact fixture defined under
Acceptance.

### 80×24 — compact overview

```text
+ agent-team | OVERVIEW ------------------------ DISCONNECTED 12:04:05 --------+
| [Overview] Work  Fleet  Activity  Logs  More...                 ? Help       |
+------------------------------------------------------------------------------+
| Fleet      8 instances   5 running   3 teams   1 crashed                     |
| Work      12 jobs        4 active    2 blocked 1 failed                      |
| Capacity   4 pipelines   2 budgets   5 schedules                             |
+ Attention -------------------------------------------------------------------+
| > job gh383-tui-spec      implementing     frontend-worker                   |
|   job release-2026-07     blocked          ask: release-manager              |
|   instance verifier-2     crashed          exit 1                            |
+ Live org --------------------------------------------------------------------+
| worker    2 working  1 idle   [3/4 running, 1 queued]                        |
| reviewer  1 working  0 idle   [review GH-382]                                |
| manager   0 working  1 idle   [persistent]                                   |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
|                                                                              |
+------------------------------------------------------------------------------+
| Filter: none | Snapshot 12:04:05 | 3/3 collections, 8/8 resources            |
| Tab focus  Enter inspect  / filter  ^K commands  r refresh  ? help  q quit   |
+------------------------------------------------------------------------------+
```

At compact width, moving focus onto a summary group replaces the lower content
pane with its detail. Jobs and instances show only priority columns; `Enter`
opens the complete row as a full-width detail pane.

### 120×30 — standard jobs

```text
+ agent-team | WORK / JOBS -------------------------------- CONNECTED 12:04:05 ----------------------------------------+
| Overview  [Work]  Fleet  Activity  Logs  Research  Requirements  Release               ? Help                        |
+----------------------------------------------------------------------------------------------------------------------+
| Query: status:active bounce:capability                                             4 matching / 12                   |
+ Jobs --------------------------------------------------------------+ Detail -----------------------------------------+
| ID                 Ticket   Status       Pipeline       Model/Tier | gh383-tui-spec                                  |
| > gh383-tui-spec   GH-383   implementing frontend...   gpt-5.6/T2  | instance frontend-worker                        |
|   gh382-discord    GH-382   review       frontend...   gpt-5.6/T2  | pipeline frontend_ticket_to_pr                  |
|   gh381-research   GH-381   running      platform...   gpt-5.6/T2  | runtime codex                                   |
|   release-july     -        blocked      release...    unknown     | bounces capability=1                            |
|                                                                    | updated 12:03:51                                |
|                                                                    |                                                 |
|                                                                    | [Enter] work detail (later)                     |
+ Model / tier ------------------------------------------------------+-------------------------------------------------+
| gpt-5.6 / T2       jobs 3    active 3    bounces 1                                                                   |
| not reported       jobs 1    active 1    bounces -                                                                   |
+ Bounce classes ------------------------------------------------------------------------------------------------------+
| capability 1 job   scope 0 jobs   infra 0 jobs   spec-ambiguity 0 jobs                                               |
|                                                                                                                      |
|                                                                                                                      |
|                                                                                                                      |
|                                                                                                                      |
|                                                                                                                      |
|                                                                                                                      |
|                                                                                                                      |
|                                                                                                                      |
+----------------------------------------------------------------------------------------------------------------------+
| Snapshot 12:04:05 | /v1/jobs ok | resources 15/15 | sort updated desc                                                |
| Tab focus  Enter inspect  / query  Esc clear/back  g+key screen  ^K commands  ? help  q quit                         |
+----------------------------------------------------------------------------------------------------------------------+
```

Standard layout keeps the selected row visible while presenting its complete
data in the right pane. When the right pane would make the table unreadable it
becomes a full-width pane below the list.

### 160×50 — wide topology

```text
+ agent-team | FLEET / TOPOLOGY --------------------------------------------------------------- CONNECTED 12:04:05 --------------------------------------------+
| OVERVIEW | WORK | FLEET | ACTIVITY | LOGS | RESEARCH | REQUIREMENTS | RELEASE                                  ? Help                                        |
+----------------------+--------------------------------------------------------------------------------+------------------------------------------------------+
| Fleet                | Live org                                                                       | Context                                              |
|   Org                | Worker                                            2 working  1 idle            | platform-worker                                      |
|   Instances          | > platform-worker       2/2 running / 1 queued    [working]                    | ephemeral                                            |
| > Topology           |     frontend-worker-1   GH-383 / writing spec     [implementing] [running]     | replica cap 2                                        |
|                      |     platform-worker-2   GH-381 / research         [implementing] [running]     | running 2                                            |
| Work                 |   reviewer              1/2 running               [working]                    | queued 1                                             |
|   Jobs               |     reviewer-gh382      GH-382 / review           [awaiting_review] [running]  | schedule: none                                       |
|   Telemetry          |   manager               persistent                [idle]                       |                                                      |
|                      |                                                                                | Enter: lane detail                                   |
| Activity             +--------------------------------------------------------------------------------+                                                      |
| Logs                 | Pipelines                                      | Budgets                                                                              |
| Research             | frontend_ticket_to_pr  agent.dispatch          | frontend  40M/day  cap 2  active 1  open 1                                           |
| Requirements         | implement -> worker (60M / 1h)                 | platform  80M/day  cap 2  active 2  open 0                                           |
| Release              | verify -> verifier (10M / 20m)                 |                                                                                      |
|                      | review -> reviewer (20M / 30m)                 |                                                                                      |
|                      +------------------------------------------------+------------------------------------------------------                                +
|                      | Schedules                                      | Teams                                                                                |
|                      | product-verify  24h   09:00  quality           | frontend  4 instances  1 pipeline  1 channel                                         |
|                      | debt-sweep      24h   08:00  platform          | platform  6 instances  2 pipelines 2 channels                                        |
|                      | docs-freshness  24h   pending quality          | quality   4 instances  1 pipeline  3 channels                                        |
|                      +------------------------------------------------+------------------------------------------------------                                +
|                      | Deployments / charters                         | Deadlines                                                                            |
|                      | project/a129... root  8 inst / 12 jobs active  | gh383-tui-spec  13:04  runtime  job resource                                         |
|                      | child/test      root  2 inst / 2 jobs observed | verifier-gh382  12:20  set      instance resource                                    |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
|                      |                                                |                                                                                      |
+----------------------+------------------------------------------------+--------------------------------------------------------------------------------------+
| Query: none | Snapshot 12:04:05 | collections 3/3 | resources 24/24 | topology section: org                                                                  |
| Tab focus  arrows/hjkl move  Enter inspect  [/] section  / filter  ^K commands  ? help  q quit                                                               |
+--------------------------------------------------------------------------------------------------------------------------------------------------------------+
```

Wide layout does not create new capabilities. It exposes more parity sections
simultaneously, while focus order remains the same as compact and standard.

## Keyboard, navigation, and focus grammar

All functions are available from the keyboard. Mouse support MAY be added, but
no acceptance criterion or advertised operation may require it.

### Global bindings

| Key | Effect |
| --- | --- |
| `q` | Quit when no text field, palette, help, confirmation, or attach handoff owns focus. |
| `Ctrl+C` | Cancel the active modal/input; when none is active, quit. |
| `?` | Toggle contextual help. Help lists global and current-screen bindings and reports any capability-disabled action. |
| `Ctrl+K` | Open the command palette. Search routes and currently permitted commands; unavailable later actions remain visible with the missing capability. |
| `/` | Focus the current screen's query/filter input. |
| `Esc` | Clear query, close the topmost overlay, or navigate one detail level back, in that priority order. |
| `Tab` / `Shift+Tab` | Move forward/back through the stable focus ring. Empty and disabled regions are skipped. |
| arrows or `h j k l` | Move within the focused list, table, tab strip, viewport, or tree. Text input consumes ordinary text and arrows. |
| `Enter` | Inspect/activate the focused non-destructive item. |
| `Space` | Toggle the focused selection or boolean control. It never confirms a destructive action. |
| `PgUp` / `PgDn`, `Home` / `End` | Page or jump in the focused viewport/list. |
| `[` / `]` | Previous/next local subsection, such as Topology panels. |
| `r` | Request an immediate read refresh. Repeated presses coalesce while one request is in flight. |
| `p` | Pause/resume the automatic five-second refresh for this UI session; polling starts enabled. Manual refresh and reconnect remain available. |
| `g o`, `g w`, `g f`, `g a`, `g l`, `g s`, `g r`, `g e` | Go to Overview, Work, Fleet, Activity, Logs, Research (study), Requirements, or Release. The second key times out after one second and otherwise has no effect. |

Bindings are data, not scattered conditionals. Help, the palette, dispatch, and
the PTY keyboard-completeness test consume the same binding registry.

### Focus rules

- Focus has a semantic identity: region, route, item resource URI or stable ID,
  and control. It is not a row number.
- A refresh preserves the focused item across sorting/filtering when that item
  remains visible. Otherwise focus moves to the next item, then the previous
  item, then the region heading.
- Opening and closing an overlay restores its invoker. Moving between size
  classes preserves the semantic item even when its pane moves.
- Loading, empty, error, stale, and disconnected states all have a focusable
  explanation and recovery command where one exists.
- Color, glyph shape, pane location, and mouse hover are never the only focus or
  status indicators. The focused item has a text-safe marker (`>` in plain
  mode), and every status has a label.

### Search and filter

`/` opens a deterministic, screen-local query. Plain terms perform
case-insensitive substring matching over displayed and detail fields. Structured
terms use `field:value`; fields are declared by the screen and shown in help.
Unknown fields produce an inline error without changing results. Multiple terms
are ANDed; repeated values for one field are ORed. `Esc` clears the query before
leaving the screen. The initial parity screens support at least:

- Jobs: `id`, `ticket`, `status`, `pipeline`, `model`, `tier`, `bounce`,
  `instance`.
- Instances: `name`, `agent`, `status`, `phase`, `model`, `tier`, `job`.
- Org: `role`, `lane`, `state`, `schedule`, `job`, `ticket`.
- Topology: `section`, `name`, `team`, `status`, `trigger`.

The command palette searches navigation and commands, not data rows. A later
cross-resource search requires its own platform API and is not a cutover gate.

## Terminal and connection behavior

### Color and terminal capabilities

- Normal mode may use terminal colors, but text labels and structure carry all
  meaning.
- If `NO_COLOR` is present (regardless of value), color output is disabled.
- With `TERM=dumb`, the renderer uses ASCII borders, emits no terminal escape
  output, and uses a conservative one-frame redraw. All read functions remain
  available.
- Capability detection is injected into the model at startup. Pure views do not
  query environment variables or the terminal.

`TERM=dumb` is an executable profile, not an alias for `NO_COLOR`. At all three
canonical geometries its golden capture MUST use only `+`, `-`, and `|` for
borders; contain no Unicode box-drawing characters; and contain no `0x1b` ESC,
`0x9b` C1 CSI, or `0x9d` C1 OSC byte anywhere. Forbidding ESC as a byte rejects
all 7-bit terminal control forms, including CSI, OSC, SGR/color, and cursor
save/restore (`ESC 7` / `ESC 8`), rather than recognizing only selected
prefixes. A PTY flow at 80×24 sends `Tab`, arrows, `Enter`, `/`, `r`, `?`, and
`q`, and proves that navigation, filtering, inspect, refresh, help, and quit
remain readable and functional without cursor addressing. The machine form and
raw-byte assertions are in `parity.yaml`.

### Non-TTY and `--once`

- Interactive `agent-team ui` requires TTY stdin and stdout. If either is not a
  TTY, it prints one diagnostic to stderr pointing to `--once` and exits with
  status `2`; it MUST NOT wait for input.
- `agent-team ui --once` performs one snapshot load, renders a 120×30 plain
  (`NO_COLOR`, ASCII-safe) Overview frame to stdout, and exits. It accepts piped
  stdout and never emits cursor-control sequences.
- A successful complete or partial snapshot exits `0`; failure to discover or
  contact a daemon with no usable last-good snapshot exits `1` after rendering
  the honest empty/disconnected frame.
- Diagnostics go to stderr. The frame alone goes to stdout, making `--once` the
  scriptable and screen-reader-oriented surface.

### Freshness, disconnection, and reconnect

The model carries connection state (`connecting`, `fresh`, `partial`,
`disconnected`, `reconnecting`), the last successful snapshot, per-source
fetch times/errors, and an injected clock.

- On partial failure, successful sources update independently and failed
  sections retain their last-good data, dimmed and labelled with their source
  timestamp and error.
- On connection loss, the last-good snapshot remains visible beneath a
  persistent `DISCONNECTED — snapshot from HH:MM:SS` banner. No data may look
  fresh merely because a redraw occurred.
- Reconnect uses cancellable exponential backoff with jitter disabled in tests.
  Manual `r` triggers an immediate coalesced attempt.
- The first successful full refresh after a loss produces one visible
  `RECONNECTED — refreshed HH:MM:SS` transition, then returns to `CONNECTED` on
  the next ordinary tick.
- Startup with no daemon uses the client-owned last-good cache when one exists
  for the same deployment and labels it stale; otherwise it renders a calm
  empty state naming `agent-team daemon start`. The cache is written and read by
  `internal/daemonclient`, bound to deployment identity and team directory,
  permissioned as sensitive local state, and invalidated when its schema or
  identity does not match. The TUI itself MUST NOT read daemon state files.

## Package and I/O boundaries

The initial serial seam is a mechanical extraction from
[`internal/cli/client.go`](../../internal/cli/client.go). It precedes the TUI so
CLI and TUI cannot grow separate discovery, auth, or DTO implementations.

```text
cmd/agent-team (`ui`, `--once` only)
             |
             v
internal/tui/model   <- pure state and typed messages
internal/tui/update  <- pure (Model, Msg) -> (Model, []Command)
internal/tui/view    <- deterministic render from Model; Charm rendering only
internal/tui/commands<- I/O adapters; execute daemonclient calls and return Msg
             |
             v
internal/daemonclient <- typed HTTP client, discovery, auth, polling/cursors
             |
             v
agent-teamd HTTP/JSON API (canonical data and authority plane)
```

Normative rules:

- `internal/daemonclient` owns the current ordered transport discovery chain:
  `AGENT_TEAM_DAEMON_URL`, then the persisted daemon HTTP address, then a live
  pidfile plus the derived Unix socket. For HTTP, bearer-token-file discovery is
  `AGENT_TEAM_DAEMON_TOKEN_FILE`, then the repository's operator-token path.
  It also owns build/origin headers, typed request/response DTOs, timeouts,
  long-lived keep-alive mode, and last-good snapshot metadata.
- The extraction MUST preserve existing CLI behavior and tests. CLI verbs and
  the TUI consume the same exported client.
- `model`, `update`, and view-model projection have no filesystem, network,
  process, wall-clock, environment, or terminal reads. Time and terminal
  capabilities arrive as messages/fields.
- Only `internal/tui/commands` performs daemon requests, ticks, and process
  handoffs. Commands return typed messages; they do not mutate the model.
- Charm imports are confined as specified by the ADR. No package linked by
  `cmd/agent-teamd` may import Charm directly or transitively.
- The TUI consumes API DTOs and canonical `agt://` identities. It does not parse
  human CLI output or reach into daemon packages for storage structs.
- Missing data for a post-cutover screen becomes a platform-owned API issue. It
  never becomes a TUI filesystem workaround.

The walking-skeleton `live-daemon` gate MUST prove replacement of the removed
browser token prompt in a fresh `tui-small-v1` repository with
`AGENT_TEAM_DAEMON_URL`, `AGENT_TEAM_DAEMON_TOKEN_FILE`, and
`AGENT_TEAM_DAEMON_SOCKET` unset. With both transports present, the TUI must
select the persisted HTTP address and automatically load the default operator
token; the daemon observes an authenticated request. With no persisted HTTP
address, the same zero-daemon-environment invocation must require a live
pidfile, select the derived Unix socket, and load the same snapshot without a
prompt. A stale/dead pidfile or missing socket must fail closed as “daemon not
running”; it may not fall through to guessed endpoints. The harness records the
selected transport and token-file path as typed test evidence without recording
the token value.

One model owns route, focus, size class, connection/freshness, immutable
snapshots, query, overlay stack, and attach state. Screens do not keep shadow
copies of shared data.

## Read-only first and later action mode

The parity TUI ships with no mutating command constructors. This is a structural
guarantee: read-only is not a boolean checked immediately before a write; there
are no write adapters to call.

Later action mode MUST satisfy all of these conditions:

- it is explicitly enabled with `--actions`;
- available verbs and denial reasons come from daemon authority, not a
  TUI-private allowlist;
- the daemon rechecks authority on every request;
- disabled actions remain visible with the missing verb/resource scope;
- every request carries operator origin/build attribution and appears in the
  existing authority/lifecycle ledger;
- every mutating request has an idempotency key;
- destructive actions have no default-yes path and require typing the canonical
  target name; and
- disconnect/stale mode disables all actions while leaving read navigation
  usable.

Candidate later verbs include instance lifecycle, job/pipeline recovery,
approval decisions, queue/outbox retry/drop/drain, schedule fire, channel
publish/ack, and topology reload. Each action slice needs its own capability and
ledger acceptance; none blocks the read-only cutover.

## Logs, follow, and attach

Logs use the daemon's redacted log endpoint in a viewport. Follow is a cursor or
tail mode over the same authority plane and uses the same freshness banner and
reconnect behavior as all other screens. The TUI never opens raw log paths.

Attach is a handoff, not a terminal emulator. The command layer suspends Bubble
Tea with `tea.ExecProcess`, invokes the existing attach/tmux path, gives the
child the PTY, and restores the model, alternate-screen state, dimensions,
focus, and refresh loop after detach. Suspend, child-start failure, normal
detach, signal exit, and restore are distinct typed messages and PTY tests. The
TUI MUST NOT reimplement tmux or multiplex an attached runtime inside a pane.

## Accessibility decision

Replacing HTML with a TUI is an explicit screen-reader accessibility regression.
That cost is accepted for the terminal-first directive; it is not disguised as
equivalent accessibility.

Mitigations are part of the contract:

- all existing CLI verbs remain supported;
- `--once` is stable, plain text, non-interactive, and pipe-friendly;
- no meaning depends on color, animation, pointer input, or box drawing;
- `NO_COLOR` and `TERM=dumb` are acceptance modes; and
- help and command names use concise text labels.

If these mitigations prove insufficient for an operator, screen growth pauses
and a new accessible surface is designed explicitly. The deleted web dashboard
is not retained as an undocumented fallback.

## Platform-owned API gaps for command-center growth

These gaps are not required for dashboard parity and MUST NOT delay the early
cutover. Before its dependent screen begins, each row becomes a separate
platform-owned GitHub issue, is sequenced behind GH-381 where it touches daemon
internals, and links back to epic #153. Route names are illustrative contracts,
not authorization for this frontend slice to add them.

| Gap | Dependent screen | Missing read contract | Existing partial surface | Owner / sequencing |
| --- | --- | --- | --- | --- |
| `TUI-API-001` | Work detail | Stable list/detail projections for steps, gates, approvals, evidence, PR state, and combined audit timeline. | Per-job resource reads and several file-backed CLI verbs. | Platform; separate issue before Work detail. |
| `TUI-API-002` | Overview | One typed health/attention projection joining queue, outbox, stuck gates, inbox, locks, budgets, and recovery hints. | `/v1/queue`, `/v1/outbox`, `/v1/locks`, collections, and CLI `health`/`monitor`. | Platform; may compose existing reads first, but no CLI scraping. |
| `TUI-API-003` | Activity | Paginated/cursor lifecycle events plus direct-mailbox unread/read/ack projection with origin and redaction. | `/v1/events`, channel reads, write-only direct message route, file-backed inbox CLI. | Platform; behind GH-381 if event durability changes. |
| `TUI-API-004` | Logs | Cursor/follow semantics, truncation/redaction metadata, and stable resume token. | Snapshot-oriented `/v1/logs/<instance>`. | Platform; required before follow, not before snapshot logs. |
| `TUI-API-005` | Research | Typed report/study index, evidence references, state, and safe artifact locator. | Repository/state artifacts and CLI/report skills. | Platform plus research owner; RESEARCH-001 remains untouched. |
| `TUI-API-006` | Requirements | Requirement/dependency/evidence projection supporting tree and reverse edges. | CLI graph/traceability artifacts; the primitive is still experimental. | Platform after the graph experiment earns product depth. |
| `TUI-API-007` | Release | Typed release readiness, artifact/check, changelog, and deployment projection. | Release CLI/skill and provider state. | Platform/release; separate issue. |
| `TUI-API-008` | Action mode | Capability discovery/explain response, idempotency contract, and operator ledger query. | Daemon enforcement exists, but the UI cannot yet explain allowed/denied actions before attempting them. | Platform/security; action mode last. |
| `TUI-API-009` | Large-fleet parity and later screens | Batch or collection resource enrichment to replace N resource requests while preserving typed envelopes. | `GET /v1/resources?uri=...` one URI at a time. | Platform optimization; current fan-out is valid for cutover if performance gates pass. |

## Acceptance model

All tests run with an injected clock, fixed locale (`C`), fixed timezone (`UTC`),
stable sort tie-breakers, deterministic terminal capability profile, and no
network outside the seeded local daemon. Fixture IDs and timestamps are fixed.

### Canonical fixtures

`tui-small-v1` is the functional fixture. It contains:

- 8 declared lanes across worker, reviewer, verifier, manager, and scheduled
  roles; 6 runtime instances spanning running/idle/stopped/crashed;
- 12 jobs spanning queued/running/blocked/done/failed, including reported and
  missing model/tier, all four bounce classes, and an outcome resource;
- 4 pipelines, 3 teams, 2 budgets, 5 schedules, 2 deployments/charters, and 3
  durable/runtime deadlines;
- success, empty, 401, 403, 503, partial-resource-failure, disconnect, and
  reconnect response variants, plus startup with matching, mismatched, and
  absent last-good cache; and
- one attachable persistent instance backed by a deterministic fake process.

`tui-large-v1` is the performance fixture: exactly 100 runtime instances and
500 jobs, with declared topology and resource enrichment distributed across all
parity panels. Fixture generators use a fixed seed and commit their normalized
input digest.

### Pure update transition contract

Table-driven tests cover every message type against its applicable canonical
states. At minimum:

| Message | Required transitions/invariants |
| --- | --- |
| `Boot` | Select Overview, establish first valid focus, emit discovery/load command once. |
| `SnapshotOK(source, data, at)` | Replace only that source, update fetched-at, preserve semantic focus, recompute projections deterministically. |
| `SnapshotError(source, err, at)` | Preserve last-good source, mark stale/error, never change its successful fetched-at. |
| `Tick(at)` / `ReconnectTick(at)` | Coalesce refreshes; deterministic backoff in tests; no I/O in update. |
| `Key(binding)` | Every advertised binding dispatches in every applicable focus state; unavailable bindings return explicit feedback, never a silent dead key. |
| `QueryChanged` / `QueryCommit` | Stable filtering, unknown-field error, semantic focus preservation. |
| `Resize(w,h)` | Apply the ordered total classifier; cover every mixed-aspect boundary vector above plus an exhaustive bounded grid; produce exactly one class, no invalid dimensions, and preserve semantic focus or the documented nearest fallback. |
| `OpenOverlay` / `CloseOverlay` | LIFO overlay behavior and invoker focus restoration. |
| `AttachRequested` / `AttachStarted` | Freeze polling and emit exactly one process command. |
| `AttachReturned` / `AttachFailed` | Restore terminal/focus/polling and show result without losing snapshot. |
| `QuitRequested` | Close the topmost modal first; quit only from an unowned global state. |

The full state matrix belongs in cheap pure tests, not in an explosion of
goldens. Model/update code remains framework-free so the matrix does not require
a PTY.

### Deterministic acceptance tiers

| Tier | Gate | Required evidence |
| --- | --- | --- |
| `unit` | Pure update and projection tables | Every message × applicable canonical states; sort/filter/aggregate tests match the current dashboard formulas captured in `parity.yaml`. |
| `golden` | Canonical renders | Every implemented screen at 80×24, 120×30, and 160×50 in color, `NO_COLOR`, and `TERM=dumb`; two consecutive clean renders are byte-identical. Golden diffs are review artifacts. |
| `pty` | `teatest` keyboard/process integration | Binding registry sweep, focus traversal, help/palette/search, mixed-aspect resize boundary vectors plus a resize storm, disconnect→stale→reconnect, and attach suspend/restore. |
| `term-dumb` | Plain render plus PTY byte/keyboard assertions | At all canonical geometries use ASCII borders and reject every `0x1b`, `0x9b`, and `0x9d` byte, thereby excluding all 7-bit escape forms (including cursor save/restore), 8-bit CSI/OSC, SGR, and color dependency; at 80×24 retain readable keyboard navigation, filter, inspect, refresh, help, and quit. |
| `live-daemon` | Seeded local daemon parity and discovery | TUI projections for every `covered` parity row equal canonical API/CLI ground truth from the same `tui-small-v1` state. A fresh repository with all daemon endpoint/token environment variables unset proves persisted-HTTP-before-socket precedence, default operator-token acquisition, Unix-socket fallback, and fail-closed stale pidfile behavior. No TUI test parses human CLI output; the harness compares typed normalized values. |
| `non-tty` | Pipe/exit/control-sequence assertions | Interactive pipe refuses with exit `2`; `--once` exact plain 120×30 frame and exit codes `0/1`; stdout contains no cursor-control sequence. |
| `cutover-lints` | Permanent surface-removal checks | All deletion and anti-regression rules in `parity.yaml`, including the dedicated auth regression, are green. Enabled only by the cutover PR and permanent thereafter. |
| `performance` | First paint and refresh | With `tui-large-v1`, overview render completes within 150 ms after the first complete API response; tick refresh is flicker-free and does not grow goroutines. |
| `soak` | One-hour interactive run | Fixed refresh cadence, disconnect/reconnect cycle, navigation, filters, and log follow; stable memory after warm-up, no goroutine/file-descriptor growth, no corrupt frames. |

“Stable memory” means the final 30-minute linear-regression slope is no more than
1 MiB/hour and retained heap after forced collection is no more than 10% above
the post-warm-up baseline. “Flicker-free” means one atomic frame commit per
completed update and no intermediate cleared frame in the PTY capture.

The walking skeleton creates every tier and proves it on Overview. Each later
screen extends the same tiers. At cutover, every implemented parity screen plus
all global behavior is green from a clean checkout.

## Exact cutover oracle

The authoritative machine form is `cutover` in
[`parity.yaml`](./parity.yaml). In prose, one PR must do all of the following:

1. provide evidence for every `covered` parity row and a cited decision for every
   `dropped-by-decision` row;
2. build one synthetic integration case from the exact current PR target head
   and cutover head; prove the target input's complete embedded-UI/route/auth
   source digest matches the inventory, then run deletion, path-absence, rewrite,
   auth, and lint gates on that case's result tree. The target ref MUST still
   resolve to the recorded SHA immediately before merge; otherwise all evidence
   is invalidated and the integration case is rebuilt. Any digest mismatch or
   target-side UI/route/auth addition fails closed and requires a reviewed
   inventory refresh before deletion;
3. pass all acceptance tiers—including zero-environment discovery and the
   distinct `TERM=dumb` render/PTY gate—the 100-instance/500-job performance
   fixture, and the one-hour soak;
4. delete `internal/daemon/ui/` and `internal/daemon/ui.go`;
5. remove both `/ui` route registrations and the static-shell authentication
   exception, with a dedicated TCP auth regression proving all remaining routes
   require the normal bearer policy;
6. delete browser-only product verification and its Playwright tests/dependency
   instructions, and convert the surviving product-verify loop to PTY,
   `--once`, and API/CLI checks;
7. remove or rewrite all user-facing embedded-dashboard documentation;
8. enable permanent lints that prevent embedded daemon HTML/JS/CSS, `/ui` route
   registration, static UI auth bypass, daemon `text/html` handlers, Playwright
   browser verification, and stale `/ui` instructions from returning;
9. add a changelog note that names `agent-team ui` and explicitly promises no
   redirect or compatibility shim; and
10. record James's GitHub approval against the exact head SHA after all changes
   and CI results are present.

The cutover PR is the only irreversible slice. Deletion and lints land together;
there is no “delete now, guard later” interval. The architecture records may
retain historical `/ui` text, so docs lints exclude this directory while
checking all user-facing guides. No daemon `/v1` behavior changes are allowed in
that PR; the only daemon-handler edits are removal of UI registration/auth code
and their tests. Freshness necessarily reads the pre-deletion target input while
absence reads the post-deletion result; both SHAs and the result tree are one
recorded integration proof and no gate may substitute a merge base, stale target,
or branch-only tree.

## Serial seams and ownership

```text
spec
  -> daemonclient mechanical extraction
  -> walking skeleton + Overview + all harness tiers
  -> parity screens
  -> James-approved early cutover + permanent lints
  -> sole-TUI screen growth
  -> authority-backed action mode
```

- `internal/daemonclient` has one owner while its extraction lands; all callers
  serialize on that contract.
- Navigation grammar, binding registry, layout tokens, and the one Model shape
  are shell-owner seams. Screen issues extend them rather than fork them.
- API additions are platform-owned follow-ups. GH-381 has right of way on daemon
  internals. The dashboard cutover requires zero additions.
- The cutover is one PR and cannot be parallelized into independent deletions.
- The frontend unit begins as one track with implementation WIP 2 plus the
  existing shared verify/review lanes. Screens are issues in that track, not
  recursive sub-units.

## Implementation falsifiers

These thresholds halt screen growth until the underlying problem is corrected:

- golden updates caused by unintended layout churn in more than 30% of view PRs
  over a rolling ten-view-PR window;
- PTY retry/flake rate above 2% over at least 100 CI executions; or
- any of the five core operator tasks—triage overview, inspect a failing job,
  read its log, check gate evidence, find a stuck instance—measurably slower at
  median in the TUI than the equivalent raw CLI workflow across ten seeded runs.

The remedy is to simplify layout/navigation or repair the harness, not to add
more screens. These falsifiers do not resurrect the deleted dashboard; the
replacement strategy is fixed, while the implementation remains accountable to
its reason for existing.

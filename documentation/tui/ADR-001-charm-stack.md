# ADR-001: Isolate and pin the Charm TUI stack

- Status: Accepted for epic #153; dependencies are not added by this ADR
- Date: 2026-07-10
- Decision owners: Frontend unit and Kensho maintainers
- Supersedes for the sole human UI: the Vite/browser recommendation in
  [`documentation/ui-design.md`](../ui-design.md) after the cutover gate only
- Related contract: [`SPEC.md`](./SPEC.md)

## Context

Kensho will replace its embedded web dashboard with a terminal-first human
interface. The interface needs a pure, deterministic update loop; responsive
terminal layout; composable list, table, viewport, and text-input behavior; and
a PTY test harness. The repository currently keeps runtime dependencies small,
and `agent-teamd` must remain a lightweight control-plane daemon with no UI
framework in its dependency closure.

The replacement is intentionally staged. This ADR chooses the implementation
stack, but GH-383 is documentation-only: it does not add modules, imports, or
production Go. The walking-skeleton slice will resolve one mutually compatible
released version set and commit the exact versions through `go.mod` and
`go.sum`.

## Decision drivers

- A model/update/view architecture that keeps state transitions cheap to test
  without a PTY.
- Deterministic rendering and an established PTY/golden test path.
- Good primitives for lists, tables, viewports, text input, help, and process
  handoff without adopting an application framework larger than the problem.
- A hard dependency boundary that preserves the daemon binary's current
  dependency posture.
- Low exit cost if terminal-framework churn later becomes unacceptable.

## Decision

Use the Charm stack:

- Bubble Tea for the program loop and command/process integration;
- Lip Gloss for terminal layout and styling; and
- Bubbles for focused, reusable terminal components.

Use `teatest` for PTY integration and golden-oriented program tests.

The walking-skeleton PR MUST pin exact, mutually compatible versions. Floating
branches, pseudo-versions chosen implicitly by tooling, and unreviewed
`@latest` upgrades are not allowed. Once the set is accepted, major-version
migration is deferred until epic #153 is complete. Security or correctness
patches within the pinned major remain permitted after normal review. If the
walking-skeleton spike discovers that the current released majors are mutually
incompatible, it stops and updates this ADR rather than mixing major lines.

### Import boundary

Charm imports are permitted only in:

- `internal/tui/...`; and
- the `agent-team ui` entrypoint wiring under `cmd/agent-team`.

They are forbidden in:

- `internal/daemonclient`;
- `internal/daemon`;
- non-UI `internal/cli` packages and verbs;
- `cmd/agent-teamd`; and
- any shared package whose inclusion would put Charm in `agent-teamd`'s
  transitive dependency graph.

The entrypoint may construct and run the Bubble Tea program, select interactive
or `--once` mode, and translate process exit status. It does not own screen
state or daemon calls.

`internal/tui` keeps the dependency boundary narrower internally:

- model and domain messages are framework-free Go;
- update behavior is pure apart from returning declarative commands;
- view/render and component adapters may use Lip Gloss/Bubbles;
- command adapters may use Bubble Tea command types and
  `tea.ExecProcess`; and
- daemon transport and typed DTOs live in `internal/daemonclient`, which has no
  Charm imports.

### Dependency enforcement

The walking-skeleton slice adds permanent checks equivalent to:

```sh
test "$(go list -deps ./cmd/agent-teamd | grep -c 'github.com/charmbracelet')" -eq 0
```

The production check should avoid `grep` exit-code ambiguity and report the
offending packages, but the invariant is exact: `agent-teamd` has zero direct or
transitive packages whose import path begins `github.com/charmbracelet/`.

A second import-boundary lint allows Charm paths only from `internal/tui/...`
and the `agent-team ui` entrypoint files. Dependency review records the direct
and transitive delta to the CLI binary when versions are first pinned.

## Alternatives considered

### tview/tcell

These provide mature imperative widgets and terminal primitives. Their
widget-centric mutable style makes the required pure transition matrix and
deterministic state ownership harder to enforce. They are rejected for this
architecture, not judged unsuitable in general.

### Bespoke ANSI renderer

A custom renderer minimizes third-party dependencies but would make Kensho own
terminal capability detection, focus/input behavior, resize handling,
wide-character correctness, process suspension, and a PTY harness. That is more
surface and risk than the UI itself warrants.

### Continue the embedded browser UI

This conflicts with the accepted terminal-first directive and preserves the
dual-surface drift window. It is not an available implementation alternative.

### Parse existing CLI output

This avoids a typed client only superficially. Human output is not an API, and
parsing it would duplicate discovery/auth behavior while coupling views to
formatting. Both CLI and TUI instead share `internal/daemonclient`.

## Consequences

Positive:

- The Elm-style loop matches the required pure update tests.
- Standard terminal components and `teatest` reduce bespoke input/PTY code.
- Framework-free model/update domain types keep a later renderer migration
  bounded.
- The daemon binary remains Charm-free by construction and by CI.

Costs and risks:

- The CLI binary gains the Charm stack and its transitive dependencies.
- Major-version API churn could touch every view, so major upgrades are frozen
  through the epic.
- Terminal behavior still has a combinatorial state space; canonical goldens
  are deliberately limited while pure transitions carry the larger matrix.
- If the exit is exercised, view/component and program-wiring code will be
  rewritten. Domain model, update rules, daemon client, fixtures, and acceptance
  oracles should survive.

## Revisit conditions

Revisit this ADR after epic #153, or sooner only if one of these is demonstrated:

- a security issue cannot be patched within the pinned major;
- required terminal behavior cannot be made deterministic with the chosen
  stack;
- PTY flake remains above the 2% falsifier after harness repair; or
- the Charm dependency boundary cannot keep `agent-teamd`'s closure clean.

A revisit requires measured evidence and a replacement plan for the existing
golden/PTY contracts. Routine upstream major releases alone are not a reason to
migrate during the epic.

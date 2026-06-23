# Codex Runtime Documentation Validation - 2026-06-22

## Scope

Validated the developer docs and README command examples against a fresh sandbox
using Codex as the selected runtime.

Repository under test:

- Source repo: `/Users/jamesaud/projects/squirtle-squad`
- Built binaries: `bin/agent-team`, `bin/agent-teamd`
- Sandbox repo: `/tmp/agent-team-codex-docs-opWOQn`
- Runtime selector: `AGENT_TEAM_RUNTIME=codex`
- Codex CLI: `codex-cli 0.141.0`

The sandbox was initialized from the bundled template with:

```sh
agent-team init \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=DOC \
  --no-input
```

The sandbox was committed locally so Codex and worktree-related commands saw a
real Git repository.

## Docs Covered

Read through and exercised commands represented across:

- `README.md`
- `docs/index.md`
- `docs/guide/index.md`
- `docs/guide/concepts.md`
- `docs/guide/architecture.md`
- `docs/guide/repository-layout.md`
- `docs/authoring/templates.md`
- `docs/authoring/agents-and-skills.md`
- `docs/authoring/topology.md`
- `docs/runtime/daemon.md`
- `docs/runtime/instances.md`
- `docs/runtime/status-mailbox-channels.md`
- `docs/workflows/jobs.md`
- `docs/workflows/queues-and-recovery.md`
- `docs/workflows/pipelines-and-teams.md`
- `docs/workflows/intake-and-schedules.md`
- `docs/workflows/diagnostics-and-repair.md`
- `docs/reference/cli.md`
- `docs/reference/file-formats.md`
- `docs/reference/runtime-api.md`
- `docs/use-cases/index.md`
- `docs/use-cases/ticket-to-pr.md`
- `docs/use-cases/multi-team-repo.md`
- `docs/use-cases/external-intake.md`
- `docs/use-cases/on-call-recovery.md`
- `docs/use-cases/template-authoring.md`
- `docs/contributing/development.md`
- `docs/contributing/testing.md`
- `docs/contributing/roadmap.md`

The forward-looking files under `documentation/` were treated as design notes,
not strict runnable docs, but their runtime/topology claims were cross-checked
where the current implementation has landed.

## What Worked

### Build and baseline

- `go build -o bin/agent-team ./cmd/agent-team`
- `go build -o bin/agent-teamd ./cmd/agent-teamd`
- `agent-team init ... --no-input`
- `AGENT_TEAM_RUNTIME=codex agent-team runtime --target .`
- `agent-team doctor --target .`

Result: all passed. Runtime reported Codex available at
`/opt/homebrew/bin/codex`, with direct runs and daemon dispatch supported.

### Daemon lifecycle and observability

Validated:

- `agent-team daemon start`
- `agent-team daemon status`
- `agent-team status --summary --json`
- `agent-team topology show --json`
- `agent-team topology summary --json`
- `agent-team plan --json`
- `agent-team sync --dry-run --summary --json`
- `agent-team ps --all --json`
- `agent-team stats --all --summary --json`
- `agent-team monitor --jobs --schedules --events 5 --json`
- `agent-team events --tail 20 --json`
- `agent-team stop manager --wait --json`

Result: lifecycle state and diagnostics were usable with Codex-selected daemon
runs.

### Real Codex daemon dispatch

Command:

```sh
AGENT_TEAM_RUNTIME=codex agent-team run manager \
  --name codex-smoke \
  --prompt "Codex runtime smoke test. Do not modify files. Reply with exactly: agent-team-codex-smoke-ok" \
  --detach \
  --json \
  -- --ephemeral --sandbox read-only
```

Result:

- Daemon dispatched `codex exec`.
- `agent-team wait codex-smoke --until terminal` observed a clean exit.
- `agent-team logs codex-smoke --tail all` included the expected final text:
  `agent-team-codex-smoke-ok`.
- `agent-team inspect codex-smoke --json` showed runtime `codex`, exit code
  `0`, and the expected daemon log path.

### Direct template run with Codex

Initial `template run` failed in an auto-created tempdir until
`--skip-git-repo-check` was forwarded to Codex. With that flag, this worked:

```sh
AGENT_TEAM_RUNTIME=codex agent-team template run bundled manager \
  --no-input \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=DOC \
  --prompt "Template-run Codex smoke. Do not modify files. Reply exactly: template-run-codex-ok" \
  -- --skip-git-repo-check --ephemeral --sandbox read-only
```

Result: Codex returned `template-run-codex-ok`.

### Jobs and dispatch previews

Validated:

- `agent-team job create DOC-304 ... --dispatch --dry-run --json`
- `agent-team job ls --summary --json`
- `agent-team job show doc-101 --json`
- `agent-team job next doc-101 --json`
- `agent-team job ready --json`
- `agent-team job advance doc-101 --dry-run --json`
- `agent-team job step doc-101 implement --status done --advance --json`
- `agent-team job triage --json`
- `agent-team job queue doc-101 --summary --json`
- `agent-team job cleanup doc-101 --dry-run --json`
- `agent-team dispatch worker DOC-303 ... --dry-run --json`

Result: job creation, dry-run dispatch previews, step transitions, audit state,
and triage worked. The dry-run route previews were especially useful.

### Pipelines and teams

Validated:

- `agent-team pipeline ls --json`
- `agent-team pipeline doctor --all --json`
- `agent-team pipeline run ticket_to_pr DOC-101 ... --json`
- `agent-team pipeline ready ticket_to_pr --json`
- `agent-team pipeline ready ticket_to_pr --state all --json`
- `agent-team pipeline advance ticket_to_pr --dry-run --preview-routes --json`
- `agent-team pipeline status --json`
- `agent-team team ls --json`
- `agent-team team run delivery DOC-305 ... --dispatch --dry-run --json`
- `agent-team team overview delivery --json`
- `agent-team team ready delivery --state all --json`
- `agent-team team advance delivery --dry-run --preview-routes --json`
- `agent-team team health delivery --jobs --json`

Result: declaration inspection, dry-run previews, team scoping, and diagnostics
worked. The default pipeline exposed two behavior issues listed below.

### Queues and recovery

Validated empty-state and job-scoped commands:

- `agent-team queue ls --json`
- `agent-team queue doctor --json`
- `agent-team queue quarantine ls --json`
- `agent-team job queue doc-101 --summary --json`
- `agent-team repair --dry-run --preview-routes --jobs --json`

Result: queue doctor and repair dry-runs worked. Empty JSON shape issues are
listed below.

### Intake and schedules

Validated:

- `agent-team schedule ls --json`
- `agent-team schedule due --json`
- `agent-team schedule next --json`
- `agent-team schedule fire --dry-run --preview-triggers --json`
- `agent-team intake summary --json`
- `agent-team intake doctor --json`
- `agent-team intake deliveries --tail 20 --json`
- `agent-team intake linear --payload ... --dry-run --preview-triggers --json`
- `agent-team intake schedule nightly --dry-run --preview-triggers --payload ... --json`

Result: empty schedules and dry-run intake previews worked. The Linear dry-run
correctly previewed creation of a `ticket_to_pr` pipeline job.

### Status, mailbox, and channels

Validated:

- status skill `set` and `show`
- `agent-team job reconcile status --dry-run --json`
- `agent-team send manager --message ... --allow-missing --json`
- `agent-team send manager --message ... --dry-run --allow-missing --json`
- `agent-team channels`
- `agent-team channel publish '#standup' ...`

Result: status files, status reconciliation, mailbox sends, and channel publish
mostly worked. Channel docs and `show` behavior need fixes below.

### Diagnostics

Validated:

- `agent-team overview --json`
- `agent-team next --json`
- `agent-team health --jobs --json`
- `agent-team monitor --jobs --schedules --events 5 --json`
- `agent-team snapshot --events -1 --json`
- `agent-team repair --dry-run --preview-routes --jobs --json`

Result: diagnostics surfaced useful next actions and correctly detected the
intentionally stuck pipeline job as `running_without_instance`.

## Issues Found

### 1. Bundled template can embed local Python `__pycache__`

Severity: medium

Observed during every `init` and `template run`: the generated `.agent_team`
included:

```text
.agent_team/skills/status/scripts/__pycache__/_status_write.cpython-313.pyc
```

The source checkout has an ignored but present file at:

```text
template/skills/status/scripts/__pycache__/_status_write.cpython-313.pyc
```

Because `go:embed all:template` embeds ignored working-tree files, local
generated files can leak into released template output if present at build time.

Suggested improvement:

- Remove the local `__pycache__` directory from the working tree.
- Add a CI check that fails when ignored/generated files exist under
  `template/` before embedding.
- Consider narrowing the embed implementation or filtering generated files at
  render time.

### 2. Runtime-facing text still says Claude in several paths

Severity: low

Observed examples:

- `agent-team init` next step: `Run agent-team run to launch Claude Code...`
- `agent-team template run --help`: says the command execs `claude` directly.
- `template run` completion repeats the same Claude-specific init guidance.

This is confusing when the selected runtime is Codex and the command does work
through the Codex adapter.

Suggested improvement:

- Replace user-facing wording with "selected runtime" where runtime selection
  applies.
- Keep Claude-specific language only in capability notes or Claude-only paths.

### 3. `template run` with Codex fails in auto tempdirs without a forwarded flag

Severity: medium

Repro:

```sh
AGENT_TEAM_RUNTIME=codex agent-team template run bundled manager \
  --no-input \
  --set linear.team_id=00000000-0000-0000-0000-000000000000 \
  --set linear.ticket_prefix=DOC \
  --prompt "smoke" \
  -- --ephemeral --sandbox read-only
```

Observed:

```text
Not inside a trusted directory and --skip-git-repo-check was not specified.
```

The same command succeeds when forwarding `--skip-git-repo-check`.

Suggested improvement:

- For Codex `template run` without `--target`, either initialize a temporary
  Git repo, automatically add `--skip-git-repo-check`, or document the required
  forwarded flag in the Codex runtime section.

### 4. Fresh pipeline jobs are not advanced by `pipeline advance`, `team advance`, or `tick`

Severity: high

Repro:

```sh
agent-team pipeline run ticket_to_pr DOC-101 "Codex pipeline validation" --json
agent-team pipeline ready ticket_to_pr --json
agent-team pipeline advance ticket_to_pr --dry-run --preview-routes --json
agent-team tick --dry-run --preview-routes --json
```

Observed:

- `pipeline ready` reports the first step as `state:"queued"` and message
  `step implement is queued and ready`.
- `pipeline advance ...` returns `[]`.
- `tick --dry-run ...` does not include an `advance` section.
- `job advance doc-101 --dry-run --json` does correctly preview dispatch for
  the same step.

The implementation appears to collect only `state == ready` rows for batch
advancement, while newly-created first steps are represented as `queued`.

Suggested improvement:

- Treat queued, dependency-satisfied pipeline steps as advanceable in
  `pipeline advance`, `team advance`, and `tick`, or initialize the first step
  as blocked/ready rather than queued when no queue item exists.
- Update action hints if the intended path is `job advance`, because current
  `pipeline ready` suggests `agent-team tick`.

### 5. Default `ticket_to_pr` review step can be marked running without a live Codex manager

Severity: high for Codex runtime

Repro after creating `doc-101`:

```sh
agent-team job step doc-101 implement --status done --advance --json
agent-team ps --all --json
agent-team job triage --json
```

Observed:

- Step `review` was marked `running` with instance `manager`.
- Event result was `messaged:["manager"]`.
- `ps --all` did not show a running `manager` at that moment.
- The daemon had `.agent_team/daemon/manager/mailbox.jsonl`, but no live
  process was consuming it.
- `health --jobs` later flagged `running_without_instance`.

This is not a pure Codex adapter crash; it is the persistent-target semantics.
The issue is more visible with Codex because native resume/subagent behavior is
limited and the default pipeline review target is persistent `manager`.

Suggested improvement:

- Do not mark a persistent-target pipeline step `running` unless the target is
  known running or successfully started.
- Alternatively mark it `queued`/`blocked` with a clear action:
  `agent-team start manager` or `agent-team run manager --prompt ...`.
- Consider making the default Codex-compatible pipeline review step target an
  ephemeral reviewer, or make `tick` start required persistent targets before
  message delivery.

### 6. `plan` suggests resume for stopped Codex instances, but resume fails

Severity: medium

Status: fixed after validation. `plan`, `sync`, and lifecycle dry-runs now
surface stopped Codex metadata as action `unsupported` because the daemon cannot
resume Codex sessions from metadata.

Repro:

```sh
AGENT_TEAM_RUNTIME=codex agent-team start manager --json
AGENT_TEAM_RUNTIME=codex agent-team stop manager --wait --json
AGENT_TEAM_RUNTIME=codex agent-team plan --json
AGENT_TEAM_RUNTIME=codex agent-team start manager --json
```

Observed:

- `plan` reported action `resume` for stopped `manager`.
- The actual `start manager` command failed with:

```text
start: runtime "codex" does not support managed resume; create a new run instead
```

Suggested improvement:

- Make plan/sync action selection runtime-aware.
- For stopped Codex instances, suggest a new one-shot/direct run path rather
  than `resume`, or clearly mark the instance as not resumable.

### 7. Channel docs use invalid channel names

Severity: low

Docs currently show examples like:

```sh
agent-team channel show standup
agent-team channel publish standup "Worker squ-42 is blocked on review"
```

Observed:

```text
channel name "standup" invalid: must match ^#[a-z0-9][a-z0-9-]{0,63}$
```

The working publish form is:

```sh
agent-team channel publish '#standup' "Codex docs validation"
```

Suggested improvement:

- Update channel examples to include the required `#` prefix.

### 8. `channel show` cannot show a channel that `channels` lists

Severity: medium

Repro:

```sh
agent-team channel publish '#standup' "Codex docs validation"
agent-team channels
agent-team channel show '#standup'
```

Observed:

- `channels` listed `#standup` with one message.
- `channel show '#standup'` returned `agent-team: no such channel: #standup`.

Suggested improvement:

- Normalize channel names consistently across list/show/publish/delete.
- Add CLI tests for `publish -> list -> show -> rm` using a `#name` channel.

### 9. Empty queue JSON uses `null` in some places

Severity: low

Observed:

```sh
agent-team queue ls --json
# null

agent-team queue quarantine ls --json
# null
```

Also, `monitor --json` emitted queue summary fields as:

```json
"instances": null,
"events": null
```

where other commands emit `{}` for those maps.

Suggested improvement:

- Return `[]` for empty list commands.
- Return `{}` for empty map fields consistently.

### 10. Codex logs are noisy for successful one-shot runs

Severity: low

The captured Codex logs include:

- `Reading additional input from stdin...`
- repeated user-plugin warnings unrelated to agent-team
- the full composed agent prompt
- final answer and token usage

This is still usable, but `agent-team logs` is noisy for simple status checks.

Suggested improvement:

- Consider a `--last-message` capture path for Codex runs using
  `codex exec --output-last-message`.
- Consider documenting that Codex adapter logs include Codex CLI diagnostics and
  full prompt echo today.

## Suggested Next Fix Order

1. Fix pipeline advancement for queued first steps.
2. Fix persistent-target pipeline state so steps are not marked running when no
   consumer exists.
3. Remove/filter `__pycache__` from embedded templates and add CI coverage.
4. Make plan/sync/start recovery runtime-aware for stopped Codex instances.
5. Update runtime-neutral wording in init/template-run help and docs.
6. Fix channel name normalization and docs.
7. Normalize empty JSON output for queue/monitor surfaces.
8. Improve Codex log capture ergonomics.

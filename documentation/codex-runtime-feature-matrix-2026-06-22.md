# Codex Runtime Feature Matrix Validation - 2026-06-22

This is the follow-up pass to `documentation/codex-runtime-doc-validation-2026-06-22.md`.
The earlier pass was documentation-oriented. This pass explicitly exercised the
feature families one by one in a fresh sandbox using Codex as the runtime where
runtime execution was relevant.

Sandbox repo:

```text
/tmp/agent-team-codex-exhaustive-1BU0kh
```

Runtime:

```text
AGENT_TEAM_RUNTIME=codex
codex path: /opt/homebrew/bin/codex
agent-team version: 0.1.0
```

## Direct Answer

Yes, pipelines were run. The pass created and advanced real pipeline jobs:

```sh
agent-team pipeline run ticket_to_pr DOC-402 "Pipeline matrix job" --json
agent-team job next doc-402 --json
agent-team job advance doc-402 --dry-run --json
agent-team pipeline advance ticket_to_pr --dry-run --preview-routes --json
agent-team team advance delivery --dry-run --preview-routes --json
agent-team tick --dry-run --preview-routes --json
agent-team job step doc-402 implement --status done --advance --dry-run --json
agent-team job step doc-402 implement --status done --advance --json
```

The pipeline model works for declaration, job creation, state inspection, and
manual step mutation. It still has an advancement gap: fresh queued first steps
are visible to `job advance --dry-run`, but `pipeline advance`, `team advance`,
and `tick` do not advance them.

## Matrix

| Area | Commands exercised | Result |
| --- | --- | --- |
| Template/bootstrap | `init`, `template ls/show/pull/rm/run`, `doctor`, `upgrade --check`, `upgrade --apply --dry-run`, `runtime` | Passed. `template run` with Codex succeeded when forwarding `--skip-git-repo-check`. |
| Direct Codex run | `run manager --detach --prompt ... -- --ephemeral --sandbox read-only`, `wait`, `logs`, `inspect` | Passed. The detached daemon run printed `feature-matrix-ok` and exited 0. |
| Daemon | `daemon start/status/reconcile/logs/restart/stop` | Passed. Daemon start/restart/stop and reconcile worked. |
| Topology | `topology summary/show/reload` | Passed. `topology reload` has no `--json`; `show --json` reflects edits after reload. |
| Schedules | `schedule ls/show/due/next/run/fire`, `intake schedule`, `team schedules` | Passed in dry-run/preview mode. The validation schedule matched `manager`. |
| Jobs | `job create/ls/show/events/update/dispatch/send/logs/attach/start/stop/kill/wait/close/reopen/retry/unblock/rm/prune` | Mostly passed. Dry-run lifecycle and messaging paths worked. |
| Actual job dispatch | `job create DOC-701`, `job dispatch doc-701 --workspace worktree`, `wait`, `job show` | Partially passed. Dispatch created `worker-doc-701`, branch, and worktree, but the ephemeral instance was removed after exit and the job remained `running`. |
| Worktree cleanup | `job cleanup doc-701 --merged --dry-run`, then actual cleanup | Passed mechanically. It removed only the job-owned worktree and branch. It also allowed cleanup while the job status was still `running`. |
| Queue | `queue doctor/ls/show/retry/drop/drain/prune`, `job queue ...`, `team queue ...`, quarantine `ls/show/restore/drop` | Passed. Doctor detected invalid queue files, quarantine worked, retry reset a dead item to pending, and daemon recovery dispatched a persisted pending item. |
| Pipelines | `pipeline ls/show/doctor/run/jobs/status/ready/advance`, `job step --advance`, `team advance`, `tick` | Mixed. Inspection and manual step changes worked; automatic advancement skipped fresh queued first steps. |
| Intake | `intake linear/github/schedule` dry-runs, `intake serve`, `/healthz`, `/linear`, `/github`, `deliveries`, `summary`, `doctor`, `replay`, `prune` | Passed. HTTP dry-run server recorded deliveries and replay previews worked. |
| Channels | `channel publish/ls/show/rm` | Passed with valid `#standup` names. Plain `standup` is correctly rejected. |
| Lifecycle aliases | `start/up`, `stop/down`, `kill`, `restart`, `reload`, `prune`, `rm`, `plan`, `sync`, `tick`, `repair`, `wait` | Passed in dry-run or safe actual paths. |
| Monitor/operator | `status`, `health`, `monitor`, `watch`, `stats/top`, `overview`, `next`, `snapshot` | Passed. Health returns non-zero with JSON when unhealthy. |
| Teams | `team ls/show/doctor/status/jobs/triage/pipelines/ready/schedules/plan/sync/up/down/restart/ps/stats/events/logs/monitor/overview/next/repair/tick/drain/prune/cleanup/queue/snapshot` | Mostly passed. Some team commands intentionally expose narrower flags than top-level equivalents. |
| Legacy instance namespace | `instance ls/ps/show` | Passed. |
| Logs/events/send/attach | `send`, `events`, `logs --list`, `logs --daemon`, `attach --no-follow` | Passed. Interactive `attach` resume was not run. |
| Shell completion/version | `completion zsh`, `--version` | Passed. |

## Important Findings

1. **Repo-root flag names are inconsistent.**
   Some command families use `--repo`; older ones use `--target`. Examples:
   `job create` and `team ...` use `--repo`; `dispatch`, `intake`,
   `channel`, `instance`, and top-level lifecycle commands use `--target`.
   This caused real operator mistakes during validation.

   Status after follow-up on 2026-06-23: the global `--repo` selector is now a
   supported override for repo-scoped commands that still expose legacy
   repo-root `--target`, including `run`, `doctor`, `runtime`, and `snapshot`.
   The remaining work is command-shape cleanup and help-text consistency, not a
   functional repo-selection blocker.

   Status after help-text follow-up: fixed for the current CLI surface. Local
   `--repo` flags now use one shared repo-root description, legacy repo-root
   `--target` flags are explicitly labeled as legacy and point operators to
   global `--repo`, and `job create --target` remains documented as the target
   agent selector. Generated CLI docs and help-output regression coverage pin
   the distinction.

2. **Roadmap command shape is not exactly current CLI shape.**
   The roadmap example `agent-team job create <ticket> --target worker` is
   valid for target agent selection, but when combined with commands that use
   `--target` for repo root it is easy to misapply. Prefer standardizing on
   `--repo` for repo root everywhere and reserving `--target` for agent/event
   target.

3. **Fresh pipeline jobs do not advance through the general maintenance paths.**
   `job advance doc-402 --dry-run` previewed worker dispatch, but
   `pipeline advance`, `team advance`, and `tick` returned no work for the same
   fresh queued first step.

   Status after follow-up: fresh first steps are now covered by
   `TestPipelineAdvanceIncludesQueuedReadyFirstStep`, `TestTickRunsMaintenanceCycle`,
   and team-scoped tick/advance tests. `pipeline advance`, `team advance`, and
   `tick` share the same ready-row path for queued ready first steps.

4. **Pipeline persistent steps can be marked running without a live instance.**
   `job step doc-402 implement --status done --advance` marked `review` as
   running with instance `manager` even though no live manager session existed.
   Health/triage later reported `running_without_instance`.

5. **Actual Codex job dispatch leaves weak post-mortem state for ephemeral workers.**
   `job dispatch doc-701 --workspace worktree` created the expected branch and
   worktree, and the daemon recorded dispatch/exit/remove events. After exit,
   `logs worker-doc-701` and `inspect worker-doc-701` failed because ephemeral
   metadata/log state had been removed. The durable job remained `running`.

   Status after follow-up: lifecycle events now carry job/ticket/branch/PR and
   exit-code metadata, and `job reconcile events` can complete or fail a durable
   job from the terminal lifecycle row even after daemon instance metadata has
   been removed. Preserving post-mortem child logs and inspectable metadata is
   still separate follow-up work.

6. **`job reconcile status` does not recover missing-state ephemeral jobs.**
   After `worker-doc-701` was removed, `job reconcile status --dry-run` returned
   `[]`, because there was no `status.toml` left to read. GitHub reconciliation
   by branch can still close the job, but there is no local automatic recovery
   from ephemeral exit/removal.

   Status after follow-up: status-file reconciliation still cannot recover a
   missing state directory by design, but `job reconcile events` now provides a
   local recovery path when the daemon lifecycle log contains a job-scoped
   terminal event.

7. **Daemon-spawned Codex workers could not use daemon-local tools from inside the Codex sandbox.**
   The queue-recovered worker log showed `inbox check` missing and
   `agent-team daemon status` failing to connect to the Unix socket with
   `operation not permitted`. A `printenv` probe showed only `PWD` from the
   expected agent-team environment set. This weakens worker self-reporting and
   mailbox/status flows under Codex.

   Status after follow-up: the launcher and daemon event path now export
   `AGENT_TEAM_DAEMON_SOCKET` alongside the existing `AGENT_TEAM_*` variables,
   and Codex receives it through shell-environment policy. The bundled inbox,
   channel, and assign-worker helpers use that resolved socket path, falling
   back to `.agent_team/daemon.sock` for older sessions. Remaining validation:
   confirm whether the selected Codex sandbox allows Unix socket connections to
   that path during real worker execution.

8. **`job cleanup --merged` can remove a worktree for a job still marked `running`.**
   In the sandbox, `job cleanup doc-701 --merged` removed the owned worktree and
   branch and cleared the job metadata even though the job was still `running`.
   It was constrained to the owned worktree, but the status precondition should
   probably be stricter or require a reconciled PR URL/status.

   Status after follow-up: non-dry-run cleanup now requires `status = "done"`.
   `TestJobCleanupMergedRejectsRunningJob` covers the running-job rejection.

9. **Queue quarantine and recovery work, but recovery starts pending fixtures immediately.**
   Starting the daemon with a pending queue fixture dispatched it as
   `worker-doc-501-dead`. That validates recovery, but it means tests/operators
   should drain/drop pending fixtures before unrelated runtime tests.

10. **Empty queue maps still sometimes render as `null`.**
    `monitor --json` emitted `queue.instances:null` and `queue.events:null` in
    one nested health object, while other health/queue paths emitted `{}`.

    Status after follow-up: fixed. `queueSummary.MarshalJSON` now normalizes
    empty `instances`, `events`, and `runtimes` maps to `{}`; queue list paths
    also return empty arrays. `TestQueueSummaryEncodesEmptyMapsAsObjects`,
    `TestQueueListJSONEmptyArray`, and monitor JSON coverage pin the behavior.

11. **The bundled template still copies Python bytecode cache files.**
    `template run` output included
    `.agent_team/skills/status/scripts/__pycache__/_status_write.cpython-313.pyc`.

    Status after follow-up: fixed in the renderer. Template rendering now skips
    generated/cache artifact directories and files (`__pycache__`, `.pyc`,
    `.pyo`, `.DS_Store`, cache dirs, and `node_modules`) before copying into a
    target repo.

12. **Runtime-specific wording still mentions Claude in user-facing paths.**
    `template run` next steps say "Run `agent-team run` to launch Claude Code"
    even when `AGENT_TEAM_RUNTIME=codex`. `attach --help` also says
    "Claude-compatible child". Some of this wording may be intentional, but it
    is noisy when validating Codex.

    Status after follow-up: attach, daemon, and job-attach help text now use
    runtime-neutral wording for managed resume and daemon child lifecycle.
    Claude-specific wording remains in runtime capability docs and examples
    where the selected profile is explicitly Claude.

13. **Codex runtime logs are noisy even for successful short runs.**
    Successful Codex runs printed plugin/skill warnings before the expected
    marker text. This is usable, but it makes `logs` harder to scan.

14. **Large Codex adapter prompts should not be passed as argv.**
    Follow-up socket validation exposed daemon-managed `codex exec` runs that
    hung or exited without a last-message sidecar while the full manager prompt
    appeared in the process argument list. Raw `codex exec` worked, including
    stdin prompt mode. Status: partially addressed after validation. Codex one-shot adapter
    paths now invoke `codex exec -` and send the assembled agent prompt through
    stdin, including daemon `/v1/dispatch` and event-dispatched workers. Raw
    Codex replay with the captured agent-team stdin, add-dir, environment
    config, and last-message path succeeded.

15. **Previously observed supervised Codex stdin stall did not reproduce.**
    Earlier direct `agent-team run ... AGENT_TEAM_RUNTIME=codex` and
    daemon-managed variants were observed with the Codex child holding fd 0 on
    the generated stdin temp file at EOF, no stdout/stderr output, and no
    last-message sidecar. Re-running direct `agent-team run --prompt
    --no-daemon` with the current stdin adapter completed successfully. Keep
    this on the watch list for full validation, but the known remaining UX
    issue is noisy raw Codex output; `run --prompt --last-message` now provides
    a quiet direct one-shot path by printing only the captured final sidecar on
    success.

## Feature Notes

- Channels are valid with leading `#`. The earlier report's concern that
  `channel show '#standup'` failed did not reproduce in this pass.
- `team snapshot` reports the runtime selected for that command. It reports
  Codex when `AGENT_TEAM_RUNTIME=codex` is set, and the configured default
  otherwise.
- `template run` with Codex works when Codex receives
  `--skip-git-repo-check`; the earlier failure without that flag is still a
  product/documentation concern for auto-created temp directories.
- Interactive `attach` resume was not run because it would take over the
  terminal. Its non-following log compatibility mode was validated.
- External live Linear/GitHub webhooks were not used; the intake CLI and local
  HTTP server were exercised with realistic webhook JSON payloads.

## Suggested Next Fixes

1. Preserve post-mortem metadata/logs for job-owned ephemeral workers. Job
   status can now be reconciled from job-scoped daemon lifecycle exit events.
2. Confirm the selected Codex sandbox allows daemon Unix socket connections
   from worker sessions now that `AGENT_TEAM_DAEMON_SOCKET` is exported.
3. Reduce noisy raw Codex adapter logs in successful short runs, especially
   plugin/skill warnings that obscure the useful last message.
4. Investigate why `agent-team`-supervised `codex exec -` stalls even though
   raw replay with the same stdin/add-dir succeeds.

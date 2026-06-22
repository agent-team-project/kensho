# Use Cases

This section shows how the product layers combine into real workflows.

## Covered Scenarios

| Use case | What it demonstrates |
| --- | --- |
| [Ticket to PR](./ticket-to-pr.md) | Durable job, worker dispatch, status, PR ownership, cleanup |
| [Multi-Team Repo](./multi-team-repo.md) | Team-scoped topology, operations, queue, and diagnostics |
| [External Intake](./external-intake.md) | Linear/GitHub events, delivery history, replay, job updates |
| [On-call Recovery](./on-call-recovery.md) | Health, overview, queue retry, quarantine, unblock, repair |
| [Template Authoring](./template-authoring.md) | Creating reusable teams and parameters |

## Choosing the Right Entry Point

| Goal | Start with |
| --- | --- |
| Try the system locally | `agent-team init` then `agent-team run manager` |
| Operate a long-lived team | `agent-team sync --wait` |
| Dispatch one unit of work | `agent-team job create ... --dispatch` |
| Preview routing | `agent-team job dispatch ... --dry-run` |
| Recover stuck work | `agent-team overview` |
| Debug a handoff | `agent-team snapshot --output diagnostics.json` |
| Scope to a product area | `agent-team team overview <team>` |
| Integrate webhooks | `agent-team intake serve` |

## Common Command Pattern

Most mutating workflows should use:

1. inspect
2. dry-run
3. apply
4. verify

Example:

```sh
agent-team overview
agent-team job show squ-42 --events all
agent-team job queue retry squ-42 --all --dry-run
agent-team job queue retry squ-42 --all
agent-team job show squ-42
```

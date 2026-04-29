---
name: status
description: Emit instance status to .agent_team/state/<instance>/status.toml so `agent-team instance ps` and the future daemon can see what each instance is doing without scraping logs. Call at phase transitions.
---

# Instance status emission

Every running instance writes a small `status.toml` to its own state dir at phase transitions. An outside observer (`agent-team instance ps`, the future daemon, a teammate) reads these files to see who is doing what without scraping logs or attaching to your session.

The file is the contract; this skill is a thin wrapper that writes it atomically.

## When to call

Emit a transition each time **what you're doing changes shape**, not on every message. Concretely:

- **At the start of a session** — `status set planning` (or whatever fits) so observers see you're alive before any artifacts exist.
- **Each major phase change** — finished planning and started editing? `status set implementing`. Pushed for review? `status set awaiting_review`. Stuck? `status block`.
- **On exit** — ephemeral instances: `status set done`. Persistent instances returning to wait: `status set idle`.

Don't ping `status set <same-phase>` repeatedly to update `last_action`. If the phase isn't changing, omit the call. (You can update `last_action` without changing phase via `status set <current-phase> --last-action "..."`, but only do this when you'd want an observer to see the new action — e.g. once per significant step, not per file edit.)

## Phases

Pick the phase that best matches *what an observer would care about*:

| Phase | Meaning |
|---|---|
| `planning` | Reading docs, exploring code, drafting an approach. No code changes yet. |
| `implementing` | Actively editing code, running commands, making the change happen. |
| `awaiting_review` | PR opened, work handed off, or a question is on a teammate / human. |
| `blocked` | Cannot proceed without input. Use `status block` (not `status set blocked`) so the reason and ask are recorded. |
| `idle` | Persistent instance has nothing in flight; waiting for the next ask. |
| `done` | Terminal — typically ephemeral instances right before they exit. |

## Surface

```sh
status set <phase> [--desc "..."] [--ticket <id>] [--pr <url>] [--branch <name>] [--last-action "..."]
status block --reason "..." --ask <instance|role>
status clear-block                          # transitions back to the prior phase
status show                                  # debug: print the current file
```

`<phase>` is one of `planning`, `implementing`, `awaiting_review`, `idle`, `done`. Use `status block` for the `blocked` phase so the reason is recorded.

Anything not passed is preserved from the prior write. The skill auto-manages `since` (reset whenever `phase` changes; untouched on description-only updates) and `[status].last_action`.

## Examples

**Starting a worker on a ticket:**

```sh
"$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh set planning \
  --desc "Reading SQU-25 ticket and supporting docs" \
  --ticket SQU-25 \
  --branch "$(git branch --show-current)"
```

**Switching from planning to implementing:**

```sh
"$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh set implementing \
  --desc "Writing the status skill"
```

**Opening a PR:**

```sh
"$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh set awaiting_review \
  --desc "PR open, awaiting review" \
  --pr "https://github.com/jamesaud/agent-team/pull/26"
```

**Hitting a blocker:**

```sh
"$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh block \
  --reason "Need clarification on the rendered/ subdir contract" \
  --ask manager
```

**Resolving a blocker:**

```sh
"$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh clear-block
```

**An ephemeral instance finishing:**

```sh
"$AGENT_TEAM_ROOT"/skills/status/scripts/status.sh set done \
  --desc "PR merged"
```

## Implementation notes

- The script writes `status.toml.tmp` and `rename`s over `status.toml` — readers never see partial writes.
- `last_action` is a free-form human string. Reader staleness is judged from file mtime, not from `last_action` content.
- `$AGENT_TEAM_STATE_DIR` is exported by the launcher (`agent-team run`); the script will fail loudly if it isn't set, since writing to the wrong directory would corrupt another instance's status.

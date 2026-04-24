---
name: pull-request
description: Create a pull request via the gh CLI, with optional PM-tool ticket linking.
user_invocable: true
---

# Create a Pull Request

## Workflow

1. **Commit** any uncommitted changes with a clear commit message.
2. **Push** the branch.
3. **Create the PR** with `gh pr create`.

## PR format

- Title under 70 characters. Keep details in the body.
- Pass the body via HEREDOC with this structure:

```
## Summary
<1-3 bullet points>

## Test plan
<Bulleted checklist of verification steps>

🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

## Linking a PM-tool ticket

If a ticket was mentioned or worked on earlier in this conversation, include a reference in the PR body so the PM tool can auto-close or track the ticket on merge:

- `Closes <ticket URL>` — this PR fully resolves the ticket.
- `Contributes to <ticket URL>` — this PR is partial progress.

Look for ticket references already in the conversation: Linear URLs (e.g. `https://linear.app/<org>/issue/<PREFIX>-<n>/...`), or ticket codes matching the consumer's `linear.ticket_prefix` from `.agent_squad/config.toml` if that file exists. Don't fabricate ticket references — if no ticket was discussed, skip the link and open the PR without one.

## Notes

- v1 assumes Linear as the PM tool. A future PM-tool adapter (Jira, GitHub Issues) will let this skill remain unchanged while the URL-resolution logic moves behind a capability boundary. Until then, Linear URL conventions are baked in here.
- The `Co-Authored-By` trailer in commits is handled by the calling agent's commit workflow, not this skill.

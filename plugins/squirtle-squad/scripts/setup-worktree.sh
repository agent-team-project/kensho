#!/usr/bin/env bash
#
# Generic worktree setup for squirtle-squad workers.
#
# Creates:
#   .worktrees/<ticket-slug>/               — isolated git worktree on branch worker/<ticket-slug>
#   .worktrees/<ticket-slug>/.worker_agent/ — state directory for the worker agent
#
# Symlinks $PWD/.env into the worktree (gitignored but needed for credentials).
# Runs $PWD/.agent_squad/post-worktree-setup.sh if it exists and is executable —
# that's the hook for consumer-specific post-setup (dependency installs,
# symlinking project-specific config, etc.).
#
# Called by the worker agent as:
#   "${CLAUDE_PLUGIN_ROOT}/scripts/setup-worktree.sh" <ticket-slug>
#
# Usage:
#   setup-worktree.sh <TICKET-SLUG>
#
# Example:
#   setup-worktree.sh SQU-14-worker-extraction

set -euo pipefail

if [ $# -ne 1 ]; then
    echo "Usage: $0 <ticket-slug>" >&2
    echo "Example: $0 SQU-14-worker-extraction" >&2
    exit 1
fi

TICKET_SLUG="$1"
REPO_ROOT="$(git rev-parse --show-toplevel)"
WORKTREE_DIR="$REPO_ROOT/.worktrees/$TICKET_SLUG"
BRANCH_NAME="worker/$TICKET_SLUG"

if [ -d "$WORKTREE_DIR" ]; then
    echo "Worktree already exists at $WORKTREE_DIR"
    exit 0
fi

git fetch origin main --quiet

echo "Creating worktree at $WORKTREE_DIR on branch $BRANCH_NAME..."
git worktree add "$WORKTREE_DIR" -b "$BRANCH_NAME" origin/main
mkdir -p "$WORKTREE_DIR/.worker_agent"

# Symlink .env if present at repo root — gitignored but usually needed for API keys.
if [ -f "$REPO_ROOT/.env" ]; then
    ln -sf "$REPO_ROOT/.env" "$WORKTREE_DIR/.env"
    echo "  symlinked .env"
fi

# Consumer-specific post-setup hook. Put project-specific steps here —
# dependency installs, symlinking config files, etc.
POST_HOOK="$REPO_ROOT/.agent_squad/post-worktree-setup.sh"
if [ -x "$POST_HOOK" ]; then
    echo "Running $POST_HOOK (consumer post-setup hook)..."
    (cd "$WORKTREE_DIR" && "$POST_HOOK")
elif [ -f "$POST_HOOK" ]; then
    echo "note: $POST_HOOK found but not executable — skipping. Run 'chmod +x $POST_HOOK' to enable." >&2
fi

echo ""
echo "Worktree ready: $WORKTREE_DIR"
echo "Branch:         $BRANCH_NAME"

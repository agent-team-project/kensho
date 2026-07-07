---
name: release
description: |
  Run the agent-team release pipeline: changelog and version prep, verification, approval handoff, tag shipping, asset checks, and comms announcement through sanctioned webhook machinery. Use when dispatched by the release pipeline or asked to prepare, verify, ship, or announce a release.
---

# Release pipeline runbook

This skill turns the repo's documented manual release flow into repeatable
pipeline steps. The release pipeline is manual-only: an operator starts it with
`agent-team pipeline run release <version> --id release-<version> --dispatch`.
Do not publish, tag, or announce outside the step you own.

## Cadence

- Use the v0.3/v0.4 cadence: start `prepare` while the last arc is still
  cycling, draft the changelog and version bump early, then keep the release PR
  current until verification is green.
- Always dry-run before the tag. A tag push is the point of no return because
  `.github/workflows/release.yml` runs GoReleaser on `v*` tags.
- The public `--version` output is release-wired through GoReleaser ldflags:
  `.goreleaser.yaml` must set
  `-X github.com/agent-team-project/agent-team/internal/cli.Version={{.Version}}`.
- For the v0.5.0 lane, the API-cleanup sweep gate is SQU-138. It must be Done
  or explicitly waived by the manager/operator before the release is approved.

## Prepare

Goal: a release PR that can be reviewed and verified, with no tag pushed.

1. Resolve the requested version from the job ticket/kickoff. Use `vX.Y.Z` for
   the tag and `X.Y.Z` where tools expect the bare version.
2. Fetch the release baseline:

   ```sh
   git fetch origin main --tags
   LAST_TAG="$(git describe --tags --abbrev=0 origin/main)"
   ```

3. Draft `CHANGELOG.md` from merged PRs since `LAST_TAG`, then tighten it into
   release notes. Prefer user-visible features, fixes, and operator lessons;
   fold chores into a short line or omit them.

   ```sh
   SINCE="$(git log -1 --format=%cs "$LAST_TAG")"
   gh pr list --state merged --base main \
     --search "merged:>=$SINCE" \
     --json number,title,url,mergedAt,labels
   ```

4. Bump release references that should move with the release: changelog heading,
   docs that name the latest released version, install examples, and any
   release-specific checklist text. Do not change `.goreleaser.yaml` unless the
   release mechanics changed.
5. Check the SQU-138 API-cleanup gate. If it is not Done, record it as blocking
   unless the manager/operator explicitly defers it for this release.
6. Open or update the release PR. The PR body should name the version, last tag,
   included PR range, SQU-138 status, and verification still required.

## Verify

Goal: prove the release PR can ship before the approval gate. Record each gate
with `agent-team job gate set` when `AGENT_TEAM_JOB_ID` is available.

Core gates:

```sh
goreleaser release --snapshot --clean --skip=publish
python3 scripts/ci/validate_toml.py
python3 scripts/ci/validate_frontmatter.py
python3 scripts/ci/validate_template_tree.py
go vet ./...
go test ./...
go build -o bin/agent-team ./cmd/agent-team
go build -o bin/agent-teamd ./cmd/agent-teamd
bin/agent-team docs cli --check docs/reference/cli.generated.md
npm ci
npm run docs:build
python3 scripts/ci/smoke_init.py bin/agent-team
python3 scripts/demo/local_feedback_delivery.py bin/agent-team
python3 scripts/demo/local_orchestration.py bin/agent-team --runtime codex
python3 scripts/demo/local_orchestration.py bin/agent-team --runtime claude
```

Run the remaining CI-equivalent gates when the local environment supports them:

```sh
docker build -t agent-team:release .
shellcheck template/skills/linear/scripts/*.sh
```

If Docker or shellcheck is unavailable locally, record the skip and rely on CI
for that gate. Do not approve the release while a content failure is open.

## Approve

The approve step is a manual gate owned by the manager/operator. Approval means:

- The release PR diff and changelog match the intended release.
- SQU-138 is Done or explicitly waived for this release.
- Verify-step gates are green, or any red gate is classified as infrastructure
  and rerun/waived by the operator.
- The release PR has been merged into `main`.

Only after those conditions are true should the approve step be marked done and
advanced to `ship`.

## Ship

Goal: tag the approved `main`, watch the release workflow, and prove assets are
downloadable.

1. Start from the merged release commit:

   ```sh
   VERSION="<requested vX.Y.Z>"
   git fetch origin main --tags
   git checkout --detach origin/main
   git status --short
   ```

2. Confirm the version wiring before tagging:

   ```sh
   grep -q 'internal/cli.Version={{.Version}}' .goreleaser.yaml
   go build -ldflags "-X github.com/agent-team-project/agent-team/internal/cli.Version=${VERSION#v}" \
     -o bin/agent-team ./cmd/agent-team
   bin/agent-team --version
   ```

3. Push the release tag:

   ```sh
   git tag "$VERSION"
   git push origin "$VERSION"
   ```

4. Watch `.github/workflows/release.yml` until it completes. On failure, read
   the failed logs, record the gate, and stop for operator action.
5. Verify the GitHub release exposes four downloadable tarballs:

   ```sh
   mkdir -p /tmp/agent-team-release-assets
   for asset in \
     "agent-team_${VERSION#v}_darwin_amd64.tar.gz" \
     "agent-team_${VERSION#v}_darwin_arm64.tar.gz" \
     "agent-team_${VERSION#v}_linux_amd64.tar.gz" \
     "agent-team_${VERSION#v}_linux_arm64.tar.gz"
   do
     gh release download "$VERSION" --pattern "$asset" --dir /tmp/agent-team-release-assets
     tar -tzf "/tmp/agent-team-release-assets/$asset" >/dev/null
   done
   ```

6. Emit the daemon self-upgrade checklist for operators:

   ```sh
   go install ./cmd/agent-team ./cmd/agent-teamd
   agent-team daemon status
   # Wait for an empty fleet, or explicitly accept orphan adoption on running workers.
   agent-team daemon restart
   agent-team doctor --canary
   ```

Do not announce from the ship step.

## Announce

The comms agent owns announcement delivery. Use its existing digest machinery in
release-announcement mode:

- Source claims only from the merged changelog, GitHub release notes, merged
  PRs, and closed tickets.
- Keep Discord content concise and concrete.
- Post only through the configured webhook path. If no webhook is configured or
  delivery fails, write the pending announcement to the comms state dir and send
  it to the supervisor.
- Never automate a user account and never let a worker post directly.

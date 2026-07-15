#!/bin/sh
# Build an activation-capable CLI/daemon pair with one immutable source marker.
set -eu

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

if [ -n "$(git status --porcelain --untracked-files=all)" ]; then
  echo "scripts/build.sh: source checkout must be clean so the build identity covers every input" >&2
  exit 1
fi
ignored_inputs=$(git ls-files --others --ignored --exclude-standard -- cmd internal template embed.go go.mod go.sum)
if [ -n "$ignored_inputs" ]; then
  echo "scripts/build.sh: ignored files overlap build inputs and cannot be bound safely:" >&2
  echo "$ignored_inputs" >&2
  exit 1
fi

revision=$(git rev-parse HEAD)
case "$revision" in
  *[!0-9a-fA-F]*|'')
    echo "scripts/build.sh: unsupported git revision: $revision" >&2
    exit 1
    ;;
esac
if [ "${#revision}" -ne 40 ]; then
  echo "scripts/build.sh: expected a full 40-character git revision, got: $revision" >&2
  exit 1
fi

output_dir=${1:-bin}
mkdir -p "$output_dir"
marker="agent-team-source-v1:git:$revision:end"
identity_flag="-X github.com/agent-team-project/agent-team/internal/buildinfo.LinkedSourceIdentity=$marker"
short_revision=$(printf '%s' "$revision" | cut -c1-12)

go build -ldflags "$identity_flag" -o "$output_dir/agent-team" ./cmd/agent-team
go build -ldflags "$identity_flag" -o "$output_dir/agent-teamd" ./cmd/agent-teamd

verify_identity() {
  binary=$1
  if ! LC_ALL=C grep -aFq "$marker" "$binary"; then
    echo "scripts/build.sh: $binary does not contain the captured full source identity" >&2
    exit 1
  fi
  version_line=$("$binary" --version)
  case " $version_line " in
    *" source git:$short_revision "*) ;;
    *)
      echo "scripts/build.sh: $binary reported the wrong source identity: $version_line" >&2
      exit 1
      ;;
  esac
  printf '%s\n' "$version_line"
}

verify_identity "$output_dir/agent-team"
verify_identity "$output_dir/agent-teamd"

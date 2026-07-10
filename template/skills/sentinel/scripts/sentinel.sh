#!/usr/bin/env bash
set -u

REPO="${SENTINEL_REPO:-agent-team-project/kensho}"
BRANCH="${SENTINEL_BRANCH:-main}"
CI_WORKFLOW="${SENTINEL_CI_WORKFLOW:-CI}"
RTD_PROJECT="${SENTINEL_RTD_PROJECT:-agent-team}"
DOC_BASE="${SENTINEL_DOC_BASE:-https://agent-team.readthedocs.io/en/latest}"
DOC_PAGES="${SENTINEL_DOC_PAGES:-/,/getting-started.html,/guide/quickstart.html,/reference/cli.html,/workflows/intake-and-schedules.html}"

failures=()
tmpdir=""
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
DOC_MARKER_CHECKER="$SCRIPT_DIR/docs_marker_check.py"

log() {
  printf 'sentinel: %s\n' "$*"
}

record_failure() {
  failures+=("$*")
  printf 'sentinel: FAIL: %s\n' "$*" >&2
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    record_failure "missing required command: $1"
    return 1
  fi
  return 0
}

json_get() {
  jq -er "$1" 2>/dev/null
}

check_repo_metadata() {
  local repo_json name default_branch visibility archived
  if ! repo_json="$(gh repo view "$REPO" --json nameWithOwner,defaultBranchRef,visibility,isArchived,url 2>&1)"; then
    record_failure "GitHub repo metadata fetch failed: $repo_json"
    return
  fi

  name="$(printf '%s' "$repo_json" | json_get '.nameWithOwner' || true)"
  default_branch="$(printf '%s' "$repo_json" | json_get '.defaultBranchRef.name' || true)"
  visibility="$(printf '%s' "$repo_json" | json_get '.visibility' || true)"
  archived="$(printf '%s' "$repo_json" | json_get '.isArchived' || true)"

  [[ "$name" == "$REPO" ]] || record_failure "GitHub repo name changed: got ${name:-<empty>}, want $REPO"
  [[ "$default_branch" == "$BRANCH" ]] || record_failure "GitHub default branch changed: got ${default_branch:-<empty>}, want $BRANCH"
  [[ "$visibility" == "PUBLIC" ]] || record_failure "GitHub repo visibility changed: got ${visibility:-<empty>}, want PUBLIC"
  [[ "$archived" == "false" ]] || record_failure "GitHub repo archived flag is $archived"
}

main_head_sha() {
  gh api "repos/$REPO/branches/$BRANCH" --jq '.commit.sha'
}

check_main_ci() {
  local head_sha runs_json run_count status conclusion run_sha run_url workflow
  if ! head_sha="$(main_head_sha 2>&1)"; then
    record_failure "could not resolve $REPO $BRANCH head: $head_sha"
    return
  fi

  if ! runs_json="$(gh run list --repo "$REPO" --workflow "$CI_WORKFLOW" --branch "$BRANCH" --limit 1 --json status,conclusion,headSha,url,workflowName 2>&1)"; then
    record_failure "could not fetch latest $CI_WORKFLOW run on $BRANCH: $runs_json"
    return
  fi

  run_count="$(printf '%s' "$runs_json" | jq 'length' 2>/dev/null || printf '0')"
  if [[ "$run_count" == "0" ]]; then
    record_failure "no $CI_WORKFLOW workflow run found on $BRANCH"
    return
  fi

  status="$(printf '%s' "$runs_json" | json_get '.[0].status' || true)"
  conclusion="$(printf '%s' "$runs_json" | json_get '.[0].conclusion' || true)"
  run_sha="$(printf '%s' "$runs_json" | json_get '.[0].headSha' || true)"
  run_url="$(printf '%s' "$runs_json" | json_get '.[0].url' || true)"
  workflow="$(printf '%s' "$runs_json" | json_get '.[0].workflowName' || true)"

  [[ "$run_sha" == "$head_sha" ]] || record_failure "$workflow is not current for $BRANCH: run ${run_sha:-<empty>} vs head $head_sha ($run_url)"
  [[ "$status" == "completed" ]] || record_failure "$workflow has not completed: status=${status:-<empty>} ($run_url)"
  [[ "$conclusion" == "success" ]] || record_failure "$workflow conclusion is ${conclusion:-<empty>}, want success ($run_url)"
}

check_rtd_build() {
  local head_sha project_json builds_json latest_count success state commit repo_url default_branch privacy
  if ! head_sha="$(main_head_sha 2>&1)"; then
    record_failure "could not resolve $REPO $BRANCH head for Read the Docs comparison: $head_sha"
    return
  fi

  if ! project_json="$(curl -fsSL "https://readthedocs.org/api/v3/projects/$RTD_PROJECT/" 2>&1)"; then
    record_failure "Read the Docs project metadata fetch failed: $project_json"
    return
  fi
  repo_url="$(printf '%s' "$project_json" | json_get '.repository.url' || true)"
  default_branch="$(printf '%s' "$project_json" | json_get '.default_branch' || true)"
  privacy="$(printf '%s' "$project_json" | json_get '.privacy_level' || true)"

  [[ "$repo_url" == "https://github.com/$REPO.git" ]] || record_failure "Read the Docs repository changed: got ${repo_url:-<empty>}, want https://github.com/$REPO.git"
  [[ "$default_branch" == "$BRANCH" ]] || record_failure "Read the Docs default branch changed: got ${default_branch:-<empty>}, want $BRANCH"
  [[ "$privacy" == "public" ]] || record_failure "Read the Docs privacy changed: got ${privacy:-<empty>}, want public"

  if ! builds_json="$(curl -fsSL "https://readthedocs.org/api/v3/projects/$RTD_PROJECT/builds/" 2>&1)"; then
    record_failure "Read the Docs builds fetch failed: $builds_json"
    return
  fi

  latest_count="$(printf '%s' "$builds_json" | jq '.results | length' 2>/dev/null || printf '0')"
  if [[ "$latest_count" == "0" ]]; then
    record_failure "no Read the Docs builds returned for $RTD_PROJECT"
    return
  fi

  success="$(printf '%s' "$builds_json" | json_get '.results[0].success' || true)"
  state="$(printf '%s' "$builds_json" | json_get '.results[0].state.code' || true)"
  commit="$(printf '%s' "$builds_json" | json_get '.results[0].commit' || true)"

  [[ "$state" == "finished" ]] || record_failure "latest Read the Docs build state is ${state:-<empty>}, want finished"
  [[ "$success" == "true" ]] || record_failure "latest Read the Docs build success is ${success:-<empty>}, want true"
  [[ "$commit" == "$head_sha" ]] || record_failure "latest Read the Docs build is not current for $BRANCH: build ${commit:-<empty>} vs head $head_sha"
}

check_docs_pages() {
  local page url body_file http_code curl_status marker_check_output
  while IFS= read -r page; do
    [[ -n "$page" ]] || continue
    if [[ "$page" == "/" ]]; then
      url="${DOC_BASE%/}/"
    else
      url="${DOC_BASE%/}${page}"
    fi
    body_file="$tmpdir/docs-$(printf '%s' "$page" | tr -cs 'A-Za-z0-9' '_').html"
    http_code="$(curl -sSL -o "$body_file" -w '%{http_code}' "$url")"
    curl_status=$?
    if [[ "$curl_status" -ne 0 ]]; then
      record_failure "docs page fetch failed: $url (curl exit $curl_status, HTTP $http_code)"
      continue
    fi
    [[ "$http_code" == "200" ]] || record_failure "docs page returned HTTP $http_code, want 200: $url"
    if ! marker_check_output="$(python3 "$DOC_MARKER_CHECKER" "$url" "$body_file" 2>&1)"; then
      record_failure "$marker_check_output"
    fi
  done < <(printf '%s' "$DOC_PAGES" | tr ',' '\n')
}

check_release_assets() {
  local release_json tag bare draft prerelease assets_json expected asset_url
  if ! release_json="$(gh release view --repo "$REPO" --json tagName,isDraft,isPrerelease,assets,url 2>&1)"; then
    record_failure "latest GitHub release fetch failed: $release_json"
    return
  fi

  tag="$(printf '%s' "$release_json" | json_get '.tagName' || true)"
  draft="$(printf '%s' "$release_json" | json_get '.isDraft' || true)"
  prerelease="$(printf '%s' "$release_json" | json_get '.isPrerelease' || true)"
  assets_json="$(printf '%s' "$release_json" | jq -c '.assets' 2>/dev/null || printf '[]')"

  [[ -n "$tag" ]] || record_failure "latest GitHub release has no tag"
  [[ "$draft" == "false" ]] || record_failure "latest GitHub release $tag is draft"
  [[ "$prerelease" == "false" ]] || record_failure "latest GitHub release $tag is prerelease"
  [[ "$tag" == v* ]] || record_failure "latest GitHub release tag does not start with v: ${tag:-<empty>}"

  bare="${tag#v}"
  for expected in \
    "agent-team_${bare}_darwin_amd64.tar.gz" \
    "agent-team_${bare}_darwin_arm64.tar.gz" \
    "agent-team_${bare}_linux_amd64.tar.gz" \
    "agent-team_${bare}_linux_arm64.tar.gz" \
    "checksums.txt"
  do
    asset_url="$(printf '%s' "$assets_json" | jq -er --arg name "$expected" '.[] | select(.name == $name) | .url' 2>/dev/null || true)"
    if [[ -z "$asset_url" ]]; then
      record_failure "latest GitHub release $tag is missing asset $expected"
      continue
    fi
    if ! curl -fsSIL --max-redirs 5 --retry 2 "$asset_url" >/dev/null; then
      record_failure "latest GitHub release asset is not fetchable: $expected ($asset_url)"
    fi
  done
}

submit_incident() {
  local body
  body="sentinel production watcher failed for $REPO@$BRANCH with ${#failures[@]} failure(s):"
  for failure in "${failures[@]}"; do
    body+=$'\n'"- $failure"
  done

  if [[ "${SENTINEL_NO_FEEDBACK:-}" == "1" ]]; then
    printf '%s\n' "$body" >&2
    return 0
  fi

  if ! command -v agent-team >/dev/null 2>&1; then
    printf 'sentinel: unable to submit incident feedback: agent-team not found on PATH\n' >&2
    printf '%s\n' "$body" >&2
    return 1
  fi

  agent-team feedback submit --category incident "$body"
}

main() {
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  require_cmd gh
  require_cmd jq
  require_cmd curl
  require_cmd python3

  if [[ "${#failures[@]}" -eq 0 ]]; then
    check_repo_metadata
    check_main_ci
    check_rtd_build
    check_docs_pages
    check_release_assets
  fi

  if [[ "${#failures[@]}" -gt 0 ]]; then
    submit_incident || true
    printf 'sentinel: %d failure(s)\n' "${#failures[@]}" >&2
    return 1
  fi

  log "all checks passed for $REPO@$BRANCH"
}

main "$@"

#!/bin/sh
#
# Push the current branch, then verify the remote ref authoritatively.
# A push command can report an ambiguous timeout after the ref has landed;
# origin/<branch> matching local HEAD is success.

set -u

usage() {
    echo "usage: git-push-verify.sh [<branch>]" >&2
    exit 2
}

remote_tip() {
    branch=$1
    run_git ls-remote origin "refs/heads/$branch" | awk '{print $1; exit}'
}

team_root() {
    if [ -n "${AGENT_TEAM_ROOT:-}" ]; then
        printf '%s\n' "$AGENT_TEAM_ROOT"
        return 0
    fi
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
    printf '%s/.agent_team\n' "$repo_root"
}

github_auth_helper() {
    printf '%s/skills/github/scripts/github-auth.sh\n' "$(team_root)"
}

origin_url() {
    git remote get-url origin 2>/dev/null || true
}

origin_kind() {
    remote=$1
    host=${AGENT_TEAM_GITHUB_HOST:-github.com}
    case "$remote" in
        "https://$host"/*)
            printf '%s\n' "github-https"
            ;;
        "git@$host":*|"ssh://git@$host"/*)
            printf '%s\n' "github-ssh"
            ;;
        *)
            printf '%s\n' "other"
            ;;
    esac
}

run_git() {
    remote=$(origin_url)
    kind=$(origin_kind "$remote")
    case "$kind" in
        github-https)
            helper=$(github_auth_helper)
            if [ ! -x "$helper" ]; then
                echo "git-push-verify: GitHub auth helper is missing or not executable: $helper" >&2
                return 2
            fi
            "$helper" git "$@"
            ;;
        github-ssh)
            echo "git-push-verify: refusing GitHub SSH remote for worker push: $remote" >&2
            echo "git-push-verify: use an HTTPS origin so github-auth.sh can pin the GitHub actor" >&2
            return 2
            ;;
        *)
            git "$@"
            ;;
    esac
}

push_verify() {
    branch=${1:-}
    if [ -z "$branch" ]; then
        branch=$(git branch --show-current)
    fi
    if [ -z "$branch" ]; then
        echo "git-push-verify: could not determine current branch" >&2
        return 2
    fi

    attempt=1
    while [ "$attempt" -le 2 ]; do
        run_git push -u origin "$branch"
        push_status=$?
        if [ "$push_status" -ne 0 ]; then
            echo "git-push-verify: git push exited $push_status; checking origin/$branch with ls-remote" >&2
        fi

        local_sha=$(git rev-parse HEAD) || return 1
        remote_sha=$(remote_tip "$branch") || remote_sha=""

        if [ "$remote_sha" = "$local_sha" ]; then
            echo "git-push-verify: origin/$branch matches local HEAD $local_sha" >&2
            return 0
        fi

        if [ "$attempt" -eq 1 ]; then
            echo "git-push-verify: origin/$branch is ${remote_sha:-<missing>}, local HEAD is $local_sha; retrying push once" >&2
        else
            echo "git-push-verify: push verification failed: origin/$branch is ${remote_sha:-<missing>}, local HEAD is $local_sha" >&2
            return 1
        fi

        attempt=$((attempt + 1))
    done
}

case "${1:-}" in
    -h|--help|help)
        usage
        ;;
esac

push_verify "${1:-}"

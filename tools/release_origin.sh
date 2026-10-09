#!/usr/bin/env bash

is_registry_source() {
    [ "$1" = "$2" ]
}

is_primary_worktree() {
    local dir="$1" git_dir common_dir
    git_dir="$(/usr/bin/git -C "${dir}" rev-parse --path-format=absolute --git-dir 2>/dev/null)" || return 1
    common_dir="$(/usr/bin/git -C "${dir}" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || return 1
    [ "${git_dir}" = "${common_dir}" ]
}

beta_worktree_for() {
    local parent
    parent="$(cd "$1/.." 2>/dev/null && pwd)" || parent="$1/.."
    printf '%s/beta' "${parent}"
}

beta_branch_for() {
    if [ -n "${BETA_BRANCH:-}" ]; then printf '%s' "${BETA_BRANCH}"; return 0; fi
    if [ -f "$1/config/beta-branch" ]; then tr -d '[:space:]' < "$1/config/beta-branch"; return 0; fi
    printf 'beta'
}

RELEASE_ORIGIN_REPO_ROOT="${RELEASE_ORIGIN_REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

is_linked_worktree_of() {
    local dir="$1" main="$2" git_dir common_dir main_common
    git_dir="$(/usr/bin/git -C "${dir}" rev-parse --path-format=absolute --git-dir 2>/dev/null)" || return 1
    common_dir="$(/usr/bin/git -C "${dir}" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || return 1
    main_common="$(/usr/bin/git -C "${main}" rev-parse --path-format=absolute --git-common-dir 2>/dev/null)" || return 1
    [ "${git_dir}" != "${common_dir}" ] && [ "${common_dir}" = "${main_common}" ]
}

worktree_branch() {
    /usr/bin/git -C "$1" rev-parse --abbrev-ref HEAD 2>/dev/null || return 1
}

tree_clean() {
    [ -z "$(/usr/bin/git -C "$1" status --porcelain --untracked-files=all 2>/dev/null)" ]
}

tree_clean_except_staged() {
    local dir="$1"; shift
    local line code path allowed matched
    while IFS= read -r line; do
        [ -n "${line}" ] || continue
        code="${line:0:2}"
        path="${line:3}"
        case "${code}" in
            'M '|'A ') ;;
            *) return 1 ;;
        esac
        matched=1
        for allowed in "$@"; do
            if [ "${path}" = "${allowed}" ]; then matched=0; break; fi
        done
        [ "${matched}" = 0 ] || return 1
    done <<EOF
$(/usr/bin/git -C "${dir}" status --porcelain --untracked-files=all 2>/dev/null)
EOF
    return 0
}

staged_tolerance_for() {
    local distribute_only="$1" comp="$2" channel="${3:-stable}"
    [ "${distribute_only}" = 1 ] || return 0
    [ -n "${comp}" ] || return 0
    if [ "${channel}" = beta ]; then
        printf '%s\n%s\n' "versions/${comp}.beta" "versions/${comp}.beta.stamp"
    else
        printf '%s\n%s\n' "versions/${comp}" "versions/${comp}.stamp"
    fi
}

origin_sync_status() {
    local dir="$1" branch="${2:-main}" counts ahead behind
    if ! /usr/bin/git -C "${dir}" fetch --quiet origin "${branch}" 2>/dev/null; then
        printf 'fetch-failed'
        return 1
    fi
    counts="$(/usr/bin/git -C "${dir}" rev-list --left-right --count HEAD...FETCH_HEAD 2>/dev/null)" || {
        printf 'fetch-failed'
        return 1
    }
    ahead="${counts%%[[:space:]]*}"
    behind="${counts##*[[:space:]]}"
    if [ "${ahead}" = 0 ] && [ "${behind}" = 0 ]; then
        printf 'in-sync'
        return 0
    fi
    if [ "${ahead}" != 0 ] && [ "${behind}" != 0 ]; then
        printf 'diverged:%s:%s' "${ahead}" "${behind}"
    elif [ "${behind}" != 0 ]; then
        printf 'behind:%s' "${behind}"
    else
        printf 'ahead:%s' "${ahead}"
    fi
    return 1
}

assert_release_origin() {
    local label="$1" dir="$2" expected="$3" mode="$4"; shift 4
    local channel=stable
    if [ "$#" -gt 0 ]; then
        case "$1" in
            stable|beta) channel="$1"; shift ;;
            */*) : ;;
            *)
                printf '✗ %s unknown channel: %s\n    a channel positional must be exactly stable or beta\n' \
                    "${label}" "$1" >&2
                return 1
                ;;
        esac
    fi
    local -a allowed=("$@")
    local mark="✗" rc=1
    if [ "${mode}" = report ]; then mark="⚠"; rc=0; fi

    local want_branch=main
    if [ "${channel}" = beta ]; then
        want_branch="$(beta_branch_for "${RELEASE_ORIGIN_REPO_ROOT}")"
        local beta_dir
        beta_dir="$(beta_worktree_for "${expected}")"
        if [ ! -d "${beta_dir}" ]; then
            printf '%s %s beta worktree missing: %s — open a beta cycle first\n' \
                "${mark}" "${label}" "${beta_dir}" >&2
            [ "${mode}" = report ] || return 1
        fi
        beta_dir="$(cd "${beta_dir}" 2>/dev/null && pwd)" || beta_dir=""
        if [ "$(cd "${dir}" 2>/dev/null && pwd)" != "${beta_dir}" ]; then
            printf '%s %s beta source must be the registry beta worktree\n    expected: %s\n    got:      %s\n' \
                "${mark}" "${label}" "${beta_dir}" "${dir}" >&2
            [ "${mode}" = report ] || return 1
        fi
        if ! is_linked_worktree_of "${dir}" "${expected}"; then
            printf '%s %s beta source is not a linked worktree of %s: %s\n' \
                "${mark}" "${label}" "${expected}" "${dir}" >&2
            [ "${mode}" = report ] || return 1
        fi
    else
        if ! is_registry_source "${dir}" "${expected}"; then
            printf '%s %s source must be the registry main folder\n    expected: %s\n    got:      %s\n    (UMBREE_SRC_* overrides are permitted only with --dry-run)\n' \
                "${mark}" "${label}" "${expected}" "${dir}" >&2
            [ "${mode}" = report ] || return 1
        fi
        if ! is_primary_worktree "${dir}"; then
            printf '%s %s source is a linked worktree, not the main-branch folder: %s\n    a cut runs only from the primary checkout on main\n' \
                "${mark}" "${label}" "${dir}" >&2
            [ "${mode}" = report ] || return 1
        fi
    fi

    local branch
    branch="$(worktree_branch "${dir}")"
    if [ "${branch}" != "${want_branch}" ]; then
        printf '%s %s source not on %s (on %s): %s\n' "${mark}" "${label}" "${want_branch}" "${branch}" "${dir}" >&2
        [ "${mode}" = report ] || return 1
    fi
    if ! tree_clean_except_staged "${dir}" ${allowed[@]+"${allowed[@]}"}; then
        printf '%s %s source tree is dirty: %s\n    commit or clean it — a cut may not stamp a working-tree state\n' \
            "${mark}" "${label}" "${dir}" >&2
        if [ "${#allowed[@]}" -gt 0 ]; then
            printf '    (staged is tolerated for exactly: %s)\n' "${allowed[*]}" >&2
        fi
        [ "${mode}" = report ] || return 1
    fi

    local sync_status
    sync_status="$(origin_sync_status "${dir}" "${want_branch}")" || true
    case "${sync_status}" in
        in-sync) return 0 ;;
        behind:*)
            printf '%s %s source is %s commit(s) behind origin/%s: %s\n    run '"'"'git pull --ff-only'"'"' there, then re-run the cut\n' \
                "${mark}" "${label}" "${sync_status#behind:}" "${want_branch}" "${dir}" >&2 ;;
        ahead:*)
            printf '%s %s source is %s commit(s) ahead of origin/%s: %s\n    merge it through a PR first — a cut may only stamp a commit that exists on the remote\n' \
                "${mark}" "${label}" "${sync_status#ahead:}" "${want_branch}" "${dir}" >&2 ;;
        diverged:*)
            local counts="${sync_status#diverged:}"
            printf '%s %s source has diverged from origin/%s (%s ahead, %s behind): %s\n' \
                "${mark}" "${label}" "${want_branch}" "${counts%%:*}" "${counts##*:}" "${dir}" >&2 ;;
        fetch-failed)
            printf '%s %s could not fetch origin/%s: %s\n    a cut may not proceed against a stale remote ref — fix connectivity or credentials and re-run\n' \
                "${mark}" "${label}" "${want_branch}" "${dir}" >&2 ;;
        *)
            printf '%s %s unrecognised origin status '"'"'%s'"'"': %s\n    a cut may not proceed on an unknown status — investigate before re-running\n' \
                "${mark}" "${label}" "${sync_status}" "${dir}" >&2 ;;
    esac
    return "${rc}"
}

check_sync_back() {
    local label="$1" dir="$2" mode="${3:-strict}"
    local mark="✗" rc=1
    if [ "${mode}" = report ]; then mark="⚠"; rc=0; fi

    if [ "${mode}" != report ]; then
        if ! /usr/bin/git -C "${dir}" fetch --quiet origin \
                "+refs/heads/main:refs/remotes/origin/main" \
                "+refs/heads/dev:refs/remotes/origin/dev" 2>/dev/null; then
            printf '%s %s: could not fetch origin/main and origin/dev for the sync-back check: %s\n    a cut may not proceed against a stale remote ref — fix connectivity or credentials, or create dev from main if origin has none (dev.md)\n' \
                "${mark}" "${label}" "${dir}" >&2
            return "${rc}"
        fi
    fi

    if ! /usr/bin/git -C "${dir}" rev-parse --verify --quiet refs/remotes/origin/main >/dev/null \
        || ! /usr/bin/git -C "${dir}" rev-parse --verify --quiet refs/remotes/origin/dev >/dev/null; then
        printf '%s %s: no origin/main or origin/dev to compare for the sync-back check: %s\n    fetch both (a real cut does), or create dev from main if origin has none (dev.md)\n' \
            "${mark}" "${label}" "${dir}" >&2
        return "${rc}"
    fi

    if ! /usr/bin/git -C "${dir}" merge-base --is-ancestor refs/remotes/origin/main refs/remotes/origin/dev 2>/dev/null; then
        printf '%s %s: origin/main is not an ancestor of dev — the sync-back from main into dev is missing: %s\n    merge origin/main into dev (a merge, never a rebase), push dev, then re-run the cut\n' \
            "${mark}" "${label}" "${dir}" >&2
        return "${rc}"
    fi
    return 0
}

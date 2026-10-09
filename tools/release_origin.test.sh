#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "${HERE}/release_origin.sh"

fail=0
check() {
    if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi
}
check_contains() {
    case "$2" in
        *"$3"*) echo "ok: $1" ;;
        *) echo "FAIL: $1 — '$2' does not contain '$3'"; fail=1 ;;
    esac
}

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
export GIT_CONFIG_GLOBAL="${WORK}/gitconfig"
export RELEASE_ORIGIN_REPO_ROOT="${WORK}/release-root"
mkdir -p "${RELEASE_ORIGIN_REPO_ROOT}"
unset BETA_BRANCH
/usr/bin/git config --file "${GIT_CONFIG_GLOBAL}" user.name  "Cut Origin Test"
/usr/bin/git config --file "${GIT_CONFIG_GLOBAL}" user.email "cut-origin@test.invalid"
/usr/bin/git config --file "${GIT_CONFIG_GLOBAL}" init.defaultBranch main

new_origin_and_clone() {
    local bare="${WORK}/$1.git" clone="${WORK}/$1"
    /usr/bin/git init --quiet --bare "${bare}"
    /usr/bin/git clone --quiet "${bare}" "${clone}" 2>/dev/null
    echo "seed" > "${clone}/README.md"
    /usr/bin/git -C "${clone}" add README.md
    /usr/bin/git -C "${clone}" commit --quiet -m "seed"
    /usr/bin/git -C "${clone}" push --quiet -u origin main
    printf '%s' "${clone}"
}

is_registry_source /a/b /a/b && r=0 || r=1
check "registry: identical paths pass" "${r}" "0"
is_registry_source /a/b/.worktrees/dev /a/b && r=0 || r=1
check "registry: a different path fails" "${r}" "1"

MAIN="$(new_origin_and_clone primary)"
/usr/bin/git -C "${MAIN}" worktree add --quiet -b feature "${MAIN}/../primary-feature" >/dev/null 2>&1
is_primary_worktree "${MAIN}" && r=0 || r=1
check "primary: the clone itself passes" "${r}" "0"
is_primary_worktree "${MAIN}/../primary-feature" && r=0 || r=1
check "primary: a linked worktree fails" "${r}" "1"
is_primary_worktree "${WORK}" && r=0 || r=1
check "primary: a non-repo fails" "${r}" "1"

check "branch: reports main" "$(worktree_branch "${MAIN}")" "main"
check "branch: reports a feature branch" "$(worktree_branch "${MAIN}/../primary-feature")" "feature"
worktree_branch "${WORK}" >/dev/null; r=$?
check "branch: a non-repo returns 1, not git's 128" "${r}" "1"

tree_clean "${MAIN}" && r=0 || r=1
check "clean: a clean tree passes" "${r}" "0"
echo "edit" >> "${MAIN}/README.md"
tree_clean "${MAIN}" && r=0 || r=1
check "clean: a modified file fails" "${r}" "1"
/usr/bin/git -C "${MAIN}" checkout --quiet -- README.md
touch "${MAIN}/stray.txt"
tree_clean "${MAIN}" && r=0 || r=1
check "clean: an untracked file also fails" "${r}" "1"
rm -f "${MAIN}/stray.txt"

UNTRACKED_CFG="$(new_origin_and_clone untracked-config)"
/usr/bin/git -C "${UNTRACKED_CFG}" config --local status.showUntrackedFiles no
touch "${UNTRACKED_CFG}/stray.txt"
tree_clean "${UNTRACKED_CFG}" && r=0 || r=1
check "clean: an untracked file fails even under status.showUntrackedFiles=no" "${r}" "1"
rm -f "${UNTRACKED_CFG}/stray.txt"

TOL="$(new_origin_and_clone tolerance)"
mkdir -p "${TOL}/versions"
echo "1.0.0" > "${TOL}/versions/umbree"
/usr/bin/git -C "${TOL}" add versions/umbree
/usr/bin/git -C "${TOL}" commit --quiet -m "seed versions/umbree"
echo "1.0.1" > "${TOL}/versions/umbree"
echo "1.0.1" > "${TOL}/versions/umbree.stamp"
/usr/bin/git -C "${TOL}" add versions/umbree versions/umbree.stamp
tree_clean_except_staged "${TOL}" "versions/umbree" "versions/umbree.stamp" && r=0 || r=1
check "clean-except-staged: an allowed 'M ' bump plus an allowed 'A ' stamp pass" "${r}" "0"

tree_clean_except_staged "${TOL}" && r=0 || r=1
check "clean-except-staged: same fixture fails tree_clean_except_staged with no allow-list (mutation check)" "${r}" "1"

CES_CLEAN="$(new_origin_and_clone ces-clean)"
tree_clean_except_staged "${CES_CLEAN}" && r=0 || r=1
check "clean-except-staged: a clean tree with no allow-list passes" "${r}" "0"

echo "extra" > "${TOL}/README.md"
/usr/bin/git -C "${TOL}" add README.md
tree_clean_except_staged "${TOL}" "versions/umbree" "versions/umbree.stamp" && r=0 || r=1
check "clean-except-staged: an extra staged file outside the allow-list fails" "${r}" "1"
/usr/bin/git -C "${TOL}" reset --quiet -- README.md
/usr/bin/git -C "${TOL}" checkout --quiet -- README.md

echo "more" >> "${TOL}/versions/umbree"
tree_clean_except_staged "${TOL}" "versions/umbree" "versions/umbree.stamp" && r=0 || r=1
check "clean-except-staged: MM on an allowed path still fails" "${r}" "1"
/usr/bin/git -C "${TOL}" checkout --quiet -- versions/umbree

touch "${TOL}/versions/stray"
tree_clean_except_staged "${TOL}" "versions/umbree" "versions/umbree.stamp" && r=0 || r=1
check "clean-except-staged: an untracked file fails even with an allow-list" "${r}" "1"
rm -f "${TOL}/versions/stray"

echo "1.0.0" > "${TOL}/versions/umbree-extra"
/usr/bin/git -C "${TOL}" add versions/umbree-extra
tree_clean_except_staged "${TOL}" "versions/umbree" "versions/umbree.stamp" && r=0 || r=1
check "clean-except-staged: a staged path that is a prefix-match only fails" "${r}" "1"
/usr/bin/git -C "${TOL}" reset --quiet -- versions/umbree-extra
rm -f "${TOL}/versions/umbree-extra"

tree_clean_except_staged "${TOL}" "versions/umbree" "versions/umbree.stamp" && r=0 || r=1
check "clean-except-staged: the fixture is restored to the passing baseline" "${r}" "0"

check "tolerance: full cut (distribute_only=0) yields nothing" "$(staged_tolerance_for 0 umbree)" ""
staged_tolerance_for 0 umbree >/dev/null && r=0 || r=1
check "tolerance: full cut returns 0" "${r}" "0"

check "tolerance: distribute-only with no comp yields nothing" "$(staged_tolerance_for 1 "")" ""

check "tolerance: distribute-only names exactly versions/<comp>, never its stamp (the floor is written at promote)" \
    "$(staged_tolerance_for 1 umbree)" "versions/umbree"

SYNC="$(new_origin_and_clone sync)"
check "origin: an up-to-date clone is in-sync" "$(origin_sync_status "${SYNC}")" "in-sync"
origin_sync_status "${SYNC}" >/dev/null && r=0 || r=1
check "origin: in-sync returns 0" "${r}" "0"

OTHER="${WORK}/sync-other"
/usr/bin/git clone --quiet "${WORK}/sync.git" "${OTHER}" 2>/dev/null
echo "second" > "${OTHER}/second.txt"
/usr/bin/git -C "${OTHER}" add second.txt
/usr/bin/git -C "${OTHER}" commit --quiet -m "second"
/usr/bin/git -C "${OTHER}" push --quiet origin main
check "origin: one commit behind is reported with its distance" "$(origin_sync_status "${SYNC}")" "behind:1"
origin_sync_status "${SYNC}" >/dev/null && r=0 || r=1
check "origin: behind returns 1" "${r}" "1"

AHEAD="$(new_origin_and_clone ahead)"
echo "local" > "${AHEAD}/local.txt"
/usr/bin/git -C "${AHEAD}" add local.txt
/usr/bin/git -C "${AHEAD}" commit --quiet -m "local only"
check "origin: one commit ahead is reported" "$(origin_sync_status "${AHEAD}")" "ahead:1"

DIV="$(new_origin_and_clone diverged)"
DIVOTHER="${WORK}/diverged-other"
/usr/bin/git clone --quiet "${WORK}/diverged.git" "${DIVOTHER}" 2>/dev/null
echo "theirs" > "${DIVOTHER}/theirs.txt"
/usr/bin/git -C "${DIVOTHER}" add theirs.txt
/usr/bin/git -C "${DIVOTHER}" commit --quiet -m "theirs"
/usr/bin/git -C "${DIVOTHER}" push --quiet origin main
echo "mine" > "${DIV}/mine.txt"
/usr/bin/git -C "${DIV}" add mine.txt
/usr/bin/git -C "${DIV}" commit --quiet -m "mine"
check "origin: divergence reports both counts" "$(origin_sync_status "${DIV}")" "diverged:1:1"

STALE="$(new_origin_and_clone stale-ref)"
STALE_OTHER="${WORK}/stale-ref-other"
/usr/bin/git clone --quiet "${WORK}/stale-ref.git" "${STALE_OTHER}" 2>/dev/null
echo "newer" > "${STALE_OTHER}/newer.txt"
/usr/bin/git -C "${STALE_OTHER}" add newer.txt
/usr/bin/git -C "${STALE_OTHER}" commit --quiet -m "newer"
/usr/bin/git -C "${STALE_OTHER}" push --quiet origin main
/usr/bin/git -C "${STALE}" config --unset remote.origin.fetch
tracking_before="$(/usr/bin/git -C "${STALE}" rev-parse refs/remotes/origin/main)"
check "origin: a stale tracking ref does not mask a behind state" "$(origin_sync_status "${STALE}")" "behind:1"
tracking_after="$(/usr/bin/git -C "${STALE}" rev-parse refs/remotes/origin/main)"
check "origin: the tracking ref really was stale (the fetch could not move it)" "${tracking_after}" "${tracking_before}"

GONE="$(new_origin_and_clone gone)"
/usr/bin/git -C "${GONE}" remote set-url origin "${WORK}/no-such-repo.git"
check "origin: an unreachable remote is fetch-failed, not in-sync" "$(origin_sync_status "${GONE}")" "fetch-failed"
origin_sync_status "${GONE}" >/dev/null && r=0 || r=1
check "origin: fetch-failed returns 1" "${r}" "1"

COMP="$(new_origin_and_clone composite)"
out="$(assert_release_origin umbree "${COMP}" "${COMP}" strict 2>&1)" && r=0 || r=1
check "assert: the happy path passes" "${r}" "0"
check "assert: the happy path is silent" "${out}" ""

out="$(assert_release_origin umbree "${COMP}" "/registry/cli/code/main" strict 2>&1)" && r=0 || r=1
check "assert: a non-registry path is rejected" "${r}" "1"
check_contains "assert: rejection names the override mechanism" "${out}" "UMBREE_SRC_"
check_contains "assert: rejection shows what was expected" "${out}" "/registry/cli/code/main"

/usr/bin/git -C "${COMP}" worktree add --quiet -b wt "${WORK}/composite-wt" >/dev/null 2>&1
out="$(assert_release_origin umbree "${WORK}/composite-wt" "${WORK}/composite-wt" strict 2>&1)" && r=0 || r=1
check "assert: a linked worktree is rejected" "${r}" "1"
check_contains "assert: worktree rejection says why" "${out}" "linked worktree"

BEHIND="$(new_origin_and_clone composite-behind)"
BOTHER="${WORK}/composite-behind-other"
/usr/bin/git clone --quiet "${WORK}/composite-behind.git" "${BOTHER}" 2>/dev/null
echo x > "${BOTHER}/x.txt"; /usr/bin/git -C "${BOTHER}" add x.txt
/usr/bin/git -C "${BOTHER}" commit --quiet -m x
/usr/bin/git -C "${BOTHER}" push --quiet origin main
out="$(assert_release_origin umbree "${BEHIND}" "${BEHIND}" strict 2>&1)" && r=0 || r=1
check "assert: behind origin is rejected" "${r}" "1"
check_contains "assert: behind names the fix" "${out}" "git pull --ff-only"

AHEAD_COMPOSITE="$(new_origin_and_clone composite-ahead)"
echo local > "${AHEAD_COMPOSITE}/local.txt"
/usr/bin/git -C "${AHEAD_COMPOSITE}" add local.txt
/usr/bin/git -C "${AHEAD_COMPOSITE}" commit --quiet -m "local only"
out="$(assert_release_origin umbree "${AHEAD_COMPOSITE}" "${AHEAD_COMPOSITE}" strict 2>&1)" && r=0 || r=1
check "assert: ahead of origin is rejected" "${r}" "1"
check_contains "assert: ahead names the fix" "${out}" "merge it through a PR first"

DIST="$(new_origin_and_clone distribute)"
mkdir -p "${DIST}/versions"
echo "1.0.1" > "${DIST}/versions/umbree"
/usr/bin/git -C "${DIST}" add versions/umbree

DIST_ALLOWED=()
while IFS= read -r line; do
    [ -n "${line}" ] || continue
    DIST_ALLOWED+=("${line}")
done <<EOF
$(staged_tolerance_for 1 umbree)
EOF
out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict "${DIST_ALLOWED[@]}" 2>&1)" && r=0 || r=1
check "assert: distribute-only staged bump passes under strict mode" "${r}" "0"
check "assert: distribute-only staged bump is silent" "${out}" ""

out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict 2>&1)" && r=0 || r=1
check "assert: the same staged bump fails with no tolerance argument (the pre-fix behavior)" "${r}" "1"
check_contains "assert: the no-tolerance failure is the dirty-tree message" "${out}" "source tree is dirty"

echo "9.9.9" > "${DIST}/versions/umbreed"
/usr/bin/git -C "${DIST}" add versions/umbreed
out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict "${DIST_ALLOWED[@]}" 2>&1)" && r=0 || r=1
check "assert: an additional staged versions/umbreed is still refused" "${r}" "1"
check_contains "assert: refusal names the tolerated set" "${out}" "staged is tolerated for exactly"
/usr/bin/git -C "${DIST}" reset --quiet -- versions/umbreed
rm -f "${DIST}/versions/umbreed"

out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict "${DIST_ALLOWED[@]}" 2>&1)" && r=0 || r=1
check "assert: distribute-only fixture is restored to the passing baseline" "${r}" "0"

echo "v1.0.1.2026.10.09.deadbeef" > "${DIST}/versions/umbree.stamp"
/usr/bin/git -C "${DIST}" add versions/umbree.stamp
out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict "${DIST_ALLOWED[@]}" 2>&1)" && r=0 || r=1
check "staged .stamp refused at a stable cut" "${r}" "1"
check_contains "staged .stamp refused at a stable cut: the refusal names the tolerated set" "${out}" "staged is tolerated for exactly"
/usr/bin/git -C "${DIST}" reset --quiet -- versions/umbree.stamp
rm -f "${DIST}/versions/umbree.stamp"
out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict "${DIST_ALLOWED[@]}" 2>&1)" && r=0 || r=1
check "staged .stamp refused at a stable cut: keep-control, the bump alone passes" "${r}" "0"

FULLCUT_ALLOWED=()
while IFS= read -r line; do
    [ -n "${line}" ] || continue
    FULLCUT_ALLOWED+=("${line}")
done <<EOF
$(staged_tolerance_for 0 umbree)
EOF
check "assert: staged_tolerance_for really yields zero elements for a full cut (fixture check)" "${#FULLCUT_ALLOWED[@]}" "0"
out="$(assert_release_origin "release repo" "${DIST}" "${DIST}" strict ${FULLCUT_ALLOWED[@]+"${FULLCUT_ALLOWED[@]}"} 2>&1)" && r=0 || r=1
check "assert: the same staged bump is refused under a full-cut tolerance (empty list)" "${r}" "1"

out="$(assert_release_origin umbree "${BEHIND}" "/elsewhere" report 2>&1)" && r=0 || r=1
check "assert: report mode returns 0" "${r}" "0"
check_contains "assert: report mode still says what is wrong" "${out}" "⚠"

CLI="$(new_origin_and_clone task5-cli)"
mkdir -p "${CLI}/versions"
echo "1.0.0" > "${CLI}/versions/cli"
/usr/bin/git -C "${CLI}" add versions/cli
/usr/bin/git -C "${CLI}" commit --quiet -m "seed versions/cli"
/usr/bin/git -C "${CLI}" push --quiet origin main

echo "1.0.1" > "${CLI}/versions/cli"
echo "1.0.1" > "${CLI}/versions/cli.stamp"
/usr/bin/git -C "${CLI}" add versions/cli versions/cli.stamp
st="$(/usr/bin/git -C "${CLI}" status --porcelain --untracked-files=all)"
check_contains "case (a) fixture: versions/cli is really staged-modified (M )" "${st}" "M  versions/cli"
check_contains "case (a) fixture: versions/cli.stamp is really staged-added (A )" "${st}" "A  versions/cli.stamp"
out="$(assert_release_origin "release repo" "${CLI}" "${CLI}" strict versions/cli versions/cli.stamp 2>&1)" && r=0 || r=1
check "case (a): tolerance admits the staged versions/cli + versions/cli.stamp bump" "${r}" "0"
check "case (a): case (a) is silent" "${out}" ""

echo "9.9.9" > "${CLI}/versions/umbreed"
/usr/bin/git -C "${CLI}" add versions/umbreed
st="$(/usr/bin/git -C "${CLI}" status --porcelain --untracked-files=all)"
check_contains "case (b) fixture: versions/umbreed is really staged-added (A )" "${st}" "A  versions/umbreed"
out="$(assert_release_origin "release repo" "${CLI}" "${CLI}" strict versions/cli versions/cli.stamp 2>&1)" && r=0 || r=1
check "case (b): an unlisted staged file is refused" "${r}" "1"
/usr/bin/git -C "${CLI}" reset --quiet -- versions/umbreed
rm -f "${CLI}/versions/umbreed"

echo "more" >> "${CLI}/versions/cli"
st="$(/usr/bin/git -C "${CLI}" status --porcelain --untracked-files=all)"
check_contains "case (c) fixture: versions/cli is really MM (staged AND worktree-modified)" "${st}" "MM versions/cli"
out="$(assert_release_origin "release repo" "${CLI}" "${CLI}" strict versions/cli versions/cli.stamp 2>&1)" && r=0 || r=1
check "case (c): MM on an allowed path is refused (staged only, not staged-plus-dirty)" "${r}" "1"
/usr/bin/git -C "${CLI}" checkout --quiet -- versions/cli

touch "${CLI}/stray.txt"
st="$(/usr/bin/git -C "${CLI}" status --porcelain --untracked-files=all)"
check_contains "case (d) fixture: stray.txt is really untracked (?? )" "${st}" "?? stray.txt"
out="$(assert_release_origin "release repo" "${CLI}" "${CLI}" strict versions/cli versions/cli.stamp 2>&1)" && r=0 || r=1
check "case (d): an untracked file is refused even with an allow-list" "${r}" "1"
rm -f "${CLI}/stray.txt"

out="$(assert_release_origin "release repo" "${CLI}" "${CLI}" strict 2>&1)" && r=0 || r=1
check "case (f): the same fixture with no trailing paths is refused (default unchanged)" "${r}" "1"
check_contains "case (f): the no-tolerance refusal is the dirty-tree message" "${out}" "source tree is dirty"

out="$(assert_release_origin "release repo" "${CLI}" "${CLI}" strict versions/cli versions/cli.stamp 2>&1)" && r=0 || r=1
check "case (a)-(f): fixture restored to the passing baseline" "${r}" "0"

CLI_AHEAD="$(new_origin_and_clone task5-cli-ahead)"
mkdir -p "${CLI_AHEAD}/versions"
echo "1.0.0" > "${CLI_AHEAD}/versions/cli"
/usr/bin/git -C "${CLI_AHEAD}" add versions/cli
/usr/bin/git -C "${CLI_AHEAD}" commit --quiet -m "seed versions/cli"
/usr/bin/git -C "${CLI_AHEAD}" push --quiet origin main
echo "1.0.1" > "${CLI_AHEAD}/versions/cli"
echo "1.0.1" > "${CLI_AHEAD}/versions/cli.stamp"
/usr/bin/git -C "${CLI_AHEAD}" add versions/cli versions/cli.stamp
/usr/bin/git -C "${CLI_AHEAD}" commit --quiet -m "bump versions/cli"
st="$(/usr/bin/git -C "${CLI_AHEAD}" status --porcelain --untracked-files=all)"
check "case (e) fixture: worktree is clean once the bump is committed" "${st}" ""
out="$(assert_release_origin "release repo" "${CLI_AHEAD}" "${CLI_AHEAD}" strict versions/cli versions/cli.stamp 2>&1)" && r=0 || r=1
check "case (e): a committed (not staged) bump is refused, 1 ahead of origin" "${r}" "1"
check_contains "case (e): the refusal message names 'ahead'" "${out}" "ahead"

check "case (g): staged_tolerance_for 0 cli yields nothing" "$(staged_tolerance_for 0 cli)" ""
staged_tolerance_for 0 cli >/dev/null && r=0 || r=1
check "case (g): staged_tolerance_for 0 cli returns 0" "${r}" "0"
check "case (h): staged_tolerance_for 1 cli names exactly versions/cli" \
    "$(staged_tolerance_for 1 cli)" "versions/cli"

new_origin_and_clone beta_ok >/dev/null
MAIN="${WORK}/beta_ok"
/usr/bin/git -C "${MAIN}" worktree add --quiet -b beta "${WORK}/beta" origin/main 2>/dev/null \
    || /usr/bin/git -C "${MAIN}" worktree add --quiet -b beta "${MAIN}/../beta" origin/main
BETA="$(cd "${MAIN}/../beta" && pwd)"
/usr/bin/git -C "${BETA}" push --quiet -u origin beta
out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict beta 2>&1)"; check "beta: clean synced beta worktree accepted" "$?" "0"
out="$(assert_release_origin cli "${MAIN}" "${MAIN}" strict beta 2>&1)"; check "beta: primary main folder refused" "$?" "1"
check_contains "beta: refusal names the beta worktree" "${out}" "${BETA}"
out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict stable 2>&1)"; check "stable: beta worktree refused" "$?" "1"
echo dirty > "${BETA}/README.md"
out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict beta 2>&1)"; check "beta: dirty refused" "$?" "1"
/usr/bin/git -C "${BETA}" checkout --quiet -- README.md
echo more > "${BETA}/x"; /usr/bin/git -C "${BETA}" add x; /usr/bin/git -C "${BETA}" -c user.name=t -c user.email=t@t commit --quiet -m ahead
out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict beta 2>&1)"; check "beta: ahead of origin/beta refused" "$?" "1"
check_contains "beta: ahead message names origin/beta" "${out}" "origin/beta"
/usr/bin/git -C "${MAIN}" worktree remove --force "${BETA}"
out="$(assert_release_origin cli "${MAIN}/../beta" "${MAIN}" strict beta 2>&1)"; check "beta: missing worktree refused" "$?" "1"
check_contains "beta: missing worktree hint" "${out}" "open a beta cycle"
check_contains "beta: missing worktree message names the resolved sibling path" \
    "${out}" "beta worktree missing: $(cd "${MAIN}/.." && pwd)/beta — open a beta cycle first"

new_origin_and_clone beta_decoy_main >/dev/null
DECOY_MAIN="${WORK}/beta_decoy_main"
new_origin_and_clone beta_decoy_unrelated >/dev/null
/usr/bin/git -C "${WORK}/beta_decoy_unrelated" checkout --quiet -b beta
/usr/bin/git -C "${WORK}/beta_decoy_unrelated" push --quiet -u origin beta
mv "${WORK}/beta_decoy_unrelated" "${WORK}/beta"
DECOY_BETA="$(cd "${WORK}/beta" && pwd)"
out="$(assert_release_origin cli "${DECOY_BETA}" "${DECOY_MAIN}" strict beta 2>&1)"
check "beta: same-path decoy with different provenance refused" "$?" "1"
check_contains "beta: decoy refusal names 'not a linked worktree'" "${out}" "not a linked worktree"
rm -rf "${WORK}/beta"

new_origin_and_clone beta_wrong_branch >/dev/null
WB_MAIN="${WORK}/beta_wrong_branch"
/usr/bin/git -C "${WB_MAIN}" worktree add --quiet -b not-beta "${WORK}/beta" origin/main 2>/dev/null \
    || /usr/bin/git -C "${WB_MAIN}" worktree add --quiet -b not-beta "${WB_MAIN}/../beta" origin/main
WB_BETA="$(cd "${WB_MAIN}/../beta" && pwd)"
out="$(assert_release_origin cli "${WB_BETA}" "${WB_MAIN}" strict beta 2>&1)"
check "beta: wrong-branch beta worktree refused" "$?" "1"
check_contains "beta: wrong-branch refusal names 'not on beta'" "${out}" "not on beta"
/usr/bin/git -C "${WB_MAIN}" worktree remove --force "${WB_BETA}"

out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict Beta 2>&1)"
check "channel: wrong case ('Beta') is refused" "$?" "1"
check_contains "channel: wrong-case refusal names it" "${out}" "unknown channel: Beta"
out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict betaa 2>&1)"
check "channel: misspelled ('betaa') is refused" "$?" "1"
check_contains "channel: misspelling refusal names it" "${out}" "unknown channel: betaa"
out="$(assert_release_origin cli "${BETA}" "${MAIN}" strict "" 2>&1)"
check "channel: empty string is refused" "$?" "1"
check_contains "channel: empty-string refusal names it" "${out}" "unknown channel:"
out="$(assert_release_origin cli "${BETA}" "${MAIN}" report Beta 2>&1)"
check "channel: wrong case is refused even under mode=report" "$?" "1"
out="$(assert_release_origin umbree "${COMP}" "${COMP}" strict versions/umbree 2>&1)"
check "channel: a path-shaped 5th positional is treated as tolerance, not a channel" "$?" "0"

check "beta_branch_for: literal beta by default" "$(beta_branch_for "${RELEASE_ORIGIN_REPO_ROOT}")" "beta"
mkdir -p "${RELEASE_ORIGIN_REPO_ROOT}/config"
printf 'beta-x\n' > "${RELEASE_ORIGIN_REPO_ROOT}/config/beta-branch"
check "beta_branch_for: config/beta-branch wins over the literal" "$(beta_branch_for "${RELEASE_ORIGIN_REPO_ROOT}")" "beta-x"
check "beta_branch_for: BETA_BRANCH wins over config/beta-branch" "$(BETA_BRANCH=v0.3.0-beta beta_branch_for "${RELEASE_ORIGIN_REPO_ROOT}")" "v0.3.0-beta"

new_origin_and_clone beta_branch >/dev/null
BB_MAIN="${WORK}/beta_branch"
/usr/bin/git -C "${BB_MAIN}" worktree add --quiet -b beta-x "${WORK}/beta" origin/main 2>/dev/null \
    || /usr/bin/git -C "${BB_MAIN}" worktree add --quiet -b beta-x "${BB_MAIN}/../beta" origin/main
BB_BETA="$(cd "${BB_MAIN}/../beta" && pwd)"
/usr/bin/git -C "${BB_BETA}" push --quiet -u origin beta-x
out="$(assert_release_origin umbree "${BB_BETA}" "${BB_MAIN}" strict beta 2>&1)"; check "beta branch: config/beta-branch=beta-x accepts a beta-x worktree" "$?" "0"
rm -f "${RELEASE_ORIGIN_REPO_ROOT}/config/beta-branch"
out="$(assert_release_origin umbree "${BB_BETA}" "${BB_MAIN}" strict beta 2>&1)"; check "beta branch: without the file the same worktree is not on beta" "$?" "1"
check_contains "beta branch: refusal names the resolved branch" "${out}" "not on beta (on beta-x)"
out="$(BETA_BRANCH=beta-x assert_release_origin umbree "${BB_BETA}" "${BB_MAIN}" strict beta 2>&1)"; check "beta branch: BETA_BRANCH=beta-x from the request accepts it" "$?" "0"
/usr/bin/git -C "${BB_MAIN}" worktree remove --force "${BB_BETA}"

new_origin_and_clone beta_behind >/dev/null
BH_MAIN="${WORK}/beta_behind"
/usr/bin/git -C "${BH_MAIN}" worktree add --quiet -b beta "${BH_MAIN}/../beta" origin/main
BH_BETA="$(cd "${BH_MAIN}/../beta" && pwd)"
/usr/bin/git -C "${BH_BETA}" push --quiet -u origin beta
BH_OTHER="${WORK}/beta_behind-other"
/usr/bin/git clone --quiet "${WORK}/beta_behind.git" "${BH_OTHER}" 2>/dev/null
/usr/bin/git -C "${BH_OTHER}" checkout --quiet beta
echo newer > "${BH_OTHER}/newer.txt"; /usr/bin/git -C "${BH_OTHER}" add newer.txt
/usr/bin/git -C "${BH_OTHER}" commit --quiet -m newer
/usr/bin/git -C "${BH_OTHER}" push --quiet origin beta
out="$(assert_release_origin umbree "${BH_BETA}" "${BH_MAIN}" strict beta 2>&1)"; check "beta: behind origin/beta refused" "$?" "1"
check_contains "beta: behind message names origin/beta" "${out}" "behind origin/beta"
/usr/bin/git -C "${BH_MAIN}" worktree remove --force "${BH_BETA}"

new_origin_and_clone beta_plain >/dev/null
BP_MAIN="${WORK}/beta_plain"
mkdir -p "${WORK}/beta"; /usr/bin/git -C "${WORK}/beta" init --quiet -b beta
out="$(assert_release_origin umbree "${WORK}/beta" "${BP_MAIN}" strict beta 2>&1)"; check "beta: a plain repo at the beta path is refused" "$?" "1"
check_contains "beta: plain-repo refusal names 'not a linked worktree'" "${out}" "not a linked worktree of"
rm -rf "${WORK}/beta"

out="$(assert_release_origin umbree "${BP_MAIN}/../beta" "${BP_MAIN}" report beta 2>&1)"; check "beta: report mode returns 0 on a missing worktree" "$?" "0"
check_contains "beta: report mode marks the finding ⚠" "${out}" "⚠"

check "tolerance: beta names versions/<comp>.beta and its stamp" \
    "$(staged_tolerance_for 1 umbree beta)" "$(printf 'versions/umbree.beta\nversions/umbree.beta.stamp')"
check "tolerance: an explicit stable names versions/<comp> only" \
    "$(staged_tolerance_for 1 umbree stable)" "versions/umbree"
check "tolerance: beta with distribute_only=0 yields nothing" "$(staged_tolerance_for 0 umbree beta)" ""

spine() {
    local c
    c="$(new_origin_and_clone "$1")"
    /usr/bin/git -C "${c}" push --quiet origin main:refs/heads/dev
    printf '%s' "${c}"
}
advance() {
    local o="${WORK}/$1-advance-$2"
    rm -rf "${o}"
    /usr/bin/git clone --quiet "${WORK}/$1.git" "${o}" 2>/dev/null
    /usr/bin/git -C "${o}" checkout --quiet "$2"
    /usr/bin/git -C "${o}" commit --quiet --allow-empty -m "advance $2"
    /usr/bin/git -C "${o}" push --quiet origin "$2"
}

SB="$(spine syncback)"
out="$(check_sync_back umbree "${SB}" strict 2>&1)" && r=0 || r=1
check "sync-back: dev == main passes" "${r}" "0"
check "sync-back: the pass is silent" "${out}" ""

advance syncback dev
out="$(check_sync_back umbree "${SB}" strict 2>&1)" && r=0 || r=1
check "sync-back: dev ahead of main (unreleased work) passes" "${r}" "0"
check "sync-back: strict fetched the moved dev through an explicit refspec" \
    "$(/usr/bin/git -C "${SB}" rev-parse refs/remotes/origin/dev)" \
    "$(/usr/bin/git -C "${WORK}/syncback.git" rev-parse refs/heads/dev)"

advance syncback main
out="$(check_sync_back umbree "${SB}" report 2>&1)" && r=0 || r=1
check "sync-back: report mode is offline (stale refs, no finding)" "${r}:${out}" "0:"
out="$(check_sync_back umbree "${SB}" strict 2>&1)" && r=0 || r=1
check "sync-back: main not in dev is refused under strict" "${r}" "1"
check_contains "sync-back: refusal names the tree and the gap" "${out}" "✗ umbree: origin/main is not an ancestor of dev"
check_contains "sync-back: refusal names the fix, a merge" "${out}" "merge origin/main into dev (a merge, never a rebase)"
out="$(check_sync_back umbree "${SB}" report 2>&1)" && r=0 || r=1
check "sync-back: report mode returns 0 on the same (now fetched) state" "${r}" "0"
check_contains "sync-back: report mode marks it ⚠" "${out}" "⚠ umbree: origin/main is not an ancestor of dev"

SBG="$(spine syncback-gone)"
/usr/bin/git -C "${SBG}" remote set-url origin "${WORK}/no-such-repo.git"
out="$(check_sync_back "release repo" "${SBG}" strict 2>&1)" && r=0 || r=1
check "sync-back: an unreachable origin is refused under strict" "${r}" "1"
check_contains "sync-back: fetch refusal says so" "${out}" "✗ release repo: could not fetch origin/main and origin/dev"
out="$(check_sync_back "release repo" "${SBG}" report 2>&1)" && r=0 || r=1
check "sync-back: report mode does not fetch, so an unreachable origin is no finding" "${r}:${out}" "0:"

SBN="$(new_origin_and_clone syncback-nodev)"
out="$(check_sync_back umbreed "${SBN}" strict 2>&1)" && r=0 || r=1
check "sync-back: no dev on origin is refused under strict" "${r}" "1"
check_contains "sync-back: that refusal names creating dev" "${out}" "create dev from main"
out="$(check_sync_back umbreed "${SBN}" report 2>&1)" && r=0 || r=1
check "sync-back: report mode returns 0 with no origin/dev" "${r}" "0"
check_contains "sync-back: report names the missing ref, not a missing sync" "${out}" "⚠ umbreed: no origin/main or origin/dev"

echo
if [ "${fail}" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

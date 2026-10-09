#!/bin/bash
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)" || exit 1
cd "$REPO_ROOT" || exit 1

GIT=/usr/bin/git

LOG="${RELEASE_LOG:-$REPO_ROOT/.release.log}"
[ -e "$LOG" ] && mv -f "$LOG" "${LOG}.prev" 2>/dev/null
if ! : > "$LOG"; then
    echo "✗ cannot write log: $LOG" >&2
    exit 1
fi

say() { echo "$@" | tee -a "$LOG"; }
die() { say "✗ $*"; exit 1; }

LOCK=""
KEYFILE=""
on_exit() {
    rc=$?
    trap - EXIT
    [ -n "$KEYFILE" ] && rm -P "$KEYFILE" 2>/dev/null
    [ -n "$LOCK" ] && rmdir "$LOCK" 2>/dev/null
    say "RELEASE-EXIT:${rc}"
    exit "$rc"
}
trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

DOMAIN="$(launchctl managername 2>/dev/null || echo unknown)"
say "session-domain: ${DOMAIN}"
[ "${DOMAIN}" = "Aqua" ] || die "not a desktop session (need Aqua, got ${DOMAIN}) — 'open' this file, do not run it from a shell"
[ "$(id -u)" -ne 0 ] || die "running as root — notarization would use root's keychain; open this file as your own user"
[ -z "${SSH_CONNECTION:-}" ] || die "this is an SSH session — it has no console security session; open this file on the desktop"
[ -t 0 ] || die "stdin is not a terminal — this was not opened by LaunchServices"

LOCK_DIR="$REPO_ROOT/.release.lock"
mkdir "$LOCK_DIR" 2>/dev/null || die "a release is already running (lock: $LOCK_DIR) — remove it only if no cut is live"
LOCK="$LOCK_DIR"

ENV_FILE="${RELEASE_ENV:-$REPO_ROOT/.release-env}"
[ -r "${ENV_FILE}" ] || die "env file not readable: ${ENV_FILE} — create it (or set RELEASE_ENV); it must put the toolchain on PATH and export RELEASE_CONFIG and RELEASE_IDENTITY"
# shellcheck source=/dev/null
. "${ENV_FILE}"
IFS=$' \t\n'
say "env: ${ENV_FILE}"

REQUEST="${RELEASE_REQUEST:-$REPO_ROOT/.release-request}"
[ -r "${REQUEST}" ] || die "request file not readable: ${REQUEST}"
COMPONENTS=""; FLAGS=""; CHANNEL=""; BETA_BRANCH=""
# shellcheck source=/dev/null
. "${REQUEST}"
IFS=$' \t\n'
[ -n "${COMPONENTS}" ] || die "request names no COMPONENTS: ${REQUEST}"
CHANNEL="${CHANNEL:-stable}"
case "${CHANNEL}" in
    stable|beta) ;;
    *) die "channel must be stable or beta (got '${CHANNEL}')" ;;
esac
[ -z "${BETA_BRANCH}" ] || export BETA_BRANCH
[ "${CHANNEL}" = beta ] || BETA_BRANCH=""

set -f
for comp in ${COMPONENTS}; do
    case "${comp}" in
        umbree|umbreed) ;;
        all) die "COMPONENTS=\"all\" is not usable here — list them instead: COMPONENTS=\"umbreed umbree\"" ;;
        *)   die "unknown component: ${comp} (expected umbree or umbreed)" ;;
    esac
done
say "request: ${COMPONENTS} [${FLAGS}]"
say "channel: ${CHANNEL}${BETA_BRANCH:+ (beta branch: ${BETA_BRANCH})}"

DRY=0
case " ${FLAGS} " in *" --dry-run "*) DRY=1 ;; esac
[ "$DRY" -eq 0 ] || say "note: --dry-run — build and publish are both rehearsed, nothing is tagged, uploaded or pushed"

BRAND_ROOT="$(cd "$REPO_ROOT/../../.." && pwd)" || die "cannot resolve the brand root above this repo"
REG_UMBREE="$BRAND_ROOT/cli/code/main"
REG_UMBREED="$BRAND_ROOT/daemon/code/main"
if [ "${CHANNEL}" = beta ]; then
    export UMBREE_SRC_UMBREE="${UMBREE_SRC_UMBREE:-$BRAND_ROOT/cli/code/beta}"
    export UMBREE_SRC_UMBREED="${UMBREE_SRC_UMBREED:-$BRAND_ROOT/daemon/code/beta}"
else
    export UMBREE_SRC_UMBREE="${UMBREE_SRC_UMBREE:-$REG_UMBREE}"
    export UMBREE_SRC_UMBREED="${UMBREE_SRC_UMBREED:-$REG_UMBREED}"
fi

# shellcheck source=tools/release_origin.sh
. "$REPO_ROOT/tools/release_origin.sh"
origin_mode=strict; [ "$DRY" -eq 0 ] || origin_mode=report
for comp in ${COMPONENTS}; do
    case "${comp}" in
        umbree)  reg="${REG_UMBREE}";  src="${UMBREE_SRC_UMBREE}" ;;
        umbreed) reg="${REG_UMBREED}"; src="${UMBREE_SRC_UMBREED}" ;;
    esac
    if [ "${CHANNEL}" = beta ]; then
        assert_release_origin "${comp}" "${src}" "${reg}" "${origin_mode}" beta 2>&1 | tee -a "$LOG"
        [ "${PIPESTATUS[0]}" -eq 0 ] || die "${comp}: beta cut origin refused — nothing built"
    else
        assert_release_origin "${comp}" "${src}" "${reg}" "${origin_mode}" 2>&1 | tee -a "$LOG"
        [ "${PIPESTATUS[0]}" -eq 0 ] || die "${comp}: cut origin refused — nothing built"
    fi
    check_sync_back "${comp}" "${src}" "${origin_mode}" 2>&1 | tee -a "$LOG"
    [ "${PIPESTATUS[0]}" -eq 0 ] || die "${comp}: main is not merged back into dev — nothing built"
done
assert_release_origin "release repo" "$REPO_ROOT" "$REPO_ROOT" "${origin_mode}" 2>&1 | tee -a "$LOG"
[ "${PIPESTATUS[0]}" -eq 0 ] || die "release repo is not in sync with origin/main — push or pull before cutting"
check_sync_back "release repo" "$REPO_ROOT" "${origin_mode}" 2>&1 | tee -a "$LOG"
[ "${PIPESTATUS[0]}" -eq 0 ] || die "release repo: main is not merged back into dev — nothing built"
say "✓ cut origin: ${COMPONENTS} (${CHANNEL}) and this repo, main contained in dev"

DP_DIR="${RELEASE_CONFIG:?set RELEASE_CONFIG to the directory holding the sealed configuration for this channel}"
[ -d "${DP_DIR}" ] || die "RELEASE_CONFIG is not a directory: ${DP_DIR}"
AGE_ID="${RELEASE_IDENTITY:?set RELEASE_IDENTITY to the identity file that decrypts RELEASE_CONFIG}"
[ -r "${AGE_ID}" ] || die "RELEASE_IDENTITY is not readable: ${AGE_ID}"

eval "$(age -d -i "${AGE_ID}" "${DP_DIR}/server-config.env.age")" \
    || die "cannot decrypt ${DP_DIR}/server-config.env.age"
[ -n "${RELEASE_HOST:-}" ] && [ -n "${STATIC_DIR:-}" ] \
    || die "sealed server config set no RELEASE_HOST/STATIC_DIR"
say "server config: decrypted from ${DP_DIR}"

require_gated_config() {
    [ "${CHANNEL}" = beta ] && return 0
    [ -n "${UMBREE_R2_GATED_BUCKET:-}" ] \
        || die "sealed server config set no UMBREE_R2_GATED_BUCKET — a stable cut stages to the private gated store before any public act; nothing built"
    export UMBREE_R2_GATED_BUCKET
}
require_gated_config

KEYFILE="$(umask 077; mktemp -t umbree-rel-key)" || die "cannot create the key tmpfile"
if ! (umask 077; age -d -i "${AGE_ID}" "${DP_DIR}/umbree-release.key.age" > "${KEYFILE}"); then
    die "cannot decrypt ${DP_DIR}/umbree-release.key.age"
fi
[ -s "${KEYFILE}" ] || die "decrypted signing key is empty"

tree_state() { $GIT status --porcelain --untracked-files=all; }

unpushed_count() {
    $GIT fetch --quiet origin main || return 1
    $GIT rev-list --count FETCH_HEAD..HEAD
}

push_marker() {
    local comp="$1" branch ahead
    branch="$($GIT symbolic-ref --quiet --short HEAD)" \
        || die "HEAD is detached — refusing to push"
    [ "${branch}" = "main" ] \
        || die "on branch '${branch}', not main — refusing to push"
    ahead="$(unpushed_count)" \
        || die "cannot reach origin to verify what would be pushed"
    [ "${ahead}" = "1" ] \
        || die "expected exactly 1 unpushed commit (the ${comp} marker), found ${ahead} — inspect before pushing"
    $GIT push origin HEAD:refs/heads/main 2>&1 | tee -a "$LOG"
    [ "${PIPESTATUS[0]}" -eq 0 ] || die "marker push failed for ${comp}"
    say "✓ ${comp} marker pushed"
    sync_marker_into_dev "${comp}"
}

sync_marker_into_dev() {
    local comp="$1" new
    new="$($GIT rev-parse HEAD)" \
        || die "${comp}: cannot read the marker commit just pushed — $(dev_sync_fix "${comp}")"
    $GIT fetch --quiet origin \
            "+refs/heads/main:refs/remotes/origin/main" \
            "+refs/heads/dev:refs/remotes/origin/dev" \
        || die "${comp}: marker pushed to main, but cannot fetch origin/main and origin/dev to sync it into dev — $(dev_sync_fix "${comp}")"
    if $GIT merge-base --is-ancestor "${new}" refs/remotes/origin/dev; then
        say "→ ${comp} marker already in dev — nothing to sync"
        return 0
    fi
    if ! $GIT merge-base --is-ancestor refs/remotes/origin/dev "${new}"; then
        merge_marker_into_dev "${comp}" "${new}"
        return 0
    fi
    $GIT push origin "${new}:refs/heads/dev" 2>&1 | tee -a "$LOG"
    [ "${PIPESTATUS[0]}" -eq 0 ] \
        || die "${comp}: marker pushed to main, but the fast-forward push to dev was refused (did dev move during the cut? it is never forced) — $(dev_sync_fix "${comp}")"
    say "✓ ${comp} marker synced into dev"
}

merge_marker_into_dev() {
    local comp="$1" new="$2" gv gmaj gmin dev out mrc merge
    gv="$($GIT --version 2>/dev/null)"
    gmaj="$(printf '%s' "${gv}" | sed -n 's/^git version \([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1/p')"
    gmin="$(printf '%s' "${gv}" | sed -n 's/^git version \([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\2/p')"
    if [ -z "${gmaj}" ] || [ "${gmaj}" -lt 2 ] || { [ "${gmaj}" -eq 2 ] && [ "${gmin}" -lt 38 ]; }; then
        die "${comp}: marker pushed to main and dev has diverged; merging it without a checkout needs git 2.38 or newer (merge-tree --write-tree), and ${GIT} reports '${gv:-no version}' — nothing was pushed to dev; $(dev_sync_fix "${comp}")"
    fi
    dev="$($GIT rev-parse refs/remotes/origin/dev)" \
        || die "${comp}: cannot read origin/dev to merge the marker into — $(dev_sync_fix "${comp}")"
    out="$($GIT merge-tree --write-tree --name-only --no-messages "${dev}" "${new}")"; mrc=$?
    if [ "${mrc}" -eq 1 ]; then
        die "${comp}: marker pushed to main, but merging main into dev conflicts in: $(printf '%s\n' "${out}" | sed 1d | sort -u | tr '\n' ' ')— nothing was pushed to dev; $(dev_sync_fix "${comp}")"
    fi
    [ "${mrc}" -eq 0 ] \
        || die "${comp}: git merge-tree failed (exit ${mrc}) merging main into dev — nothing was pushed to dev; $(dev_sync_fix "${comp}")"
    merge="$($GIT commit-tree "$(printf '%s\n' "${out}" | sed -n 1p)" -p "${dev}" -p "${new}" -m "Merge branch 'main' into dev")" \
        || die "${comp}: cannot record the merge of main into dev — nothing was pushed to dev; $(dev_sync_fix "${comp}")"
    $GIT push origin "${merge}:refs/heads/dev" 2>&1 | tee -a "$LOG"
    [ "${PIPESTATUS[0]}" -eq 0 ] \
        || die "${comp}: marker pushed to main, but the merge push to dev was refused — dev moved during the cut, and it is never forced; $(dev_sync_fix "${comp}")"
    say "✓ ${comp} marker merged into dev ($($GIT rev-parse --short "${merge}"))"
}

dev_sync_fix() {
    printf '%s' "merge origin/main into dev by hand (git merge — never a rebase, which drops the merge commits and takes main back out of dev), push dev, then, if components remain, re-run with ${CUT_DONE:-$1} dropped from COMPONENTS"
}

CUT_DONE=""
for comp in ${COMPONENTS}; do
    say ""
    say "── cut: ${comp} ──"

    say "→ build (rkit: cross-compile, sign, notarize, CVE gate as FLAGS direct; channel ${CHANNEL})"
    # shellcheck disable=SC2086
    go run ./cmd/rkit build --component "${comp}" --channel "${CHANNEL}" ${FLAGS} --sign-key "${KEYFILE}" 2>&1 | tee -a "$LOG"
    rc="${PIPESTATUS[0]}"
    [ "${rc}" -eq 0 ] || { say "✗ ${comp} build failed (exit ${rc}) — later components NOT cut"; exit "${rc}"; }

    src_var="UMBREE_SRC_$(printf '%s' "${comp}" | tr '[:lower:]' '[:upper:]')"
    stamp="$(SRC_DIR="${!src_var}" bash tools/version.sh "${comp}" --channel "${CHANNEL}" --stamp)" \
        || die "${comp}: cannot resolve the built stamp"

    if [ "${CHANNEL}" = beta ]; then verb="--channel beta"; else verb="--distribute-only"; fi
    say "→ stamp: ${stamp}"

    if [ "$DRY" -eq 1 ]; then
        say "→ publish (rehearsal)"
        # shellcheck disable=SC2086
        bash tools/release.sh ${verb} "${comp}" "${stamp}" --dry-run 2>&1 | tee -a "$LOG"
        [ "${PIPESTATUS[0]}" -eq 0 ] || die "${comp}: publish rehearsal failed"
        say "→ ${comp}: --dry-run, nothing published and nothing to push"
        continue
    fi

    if [ "${CHANNEL}" = beta ]; then
        say "→ publish (tag, R2 beta layout, beta twins, marker commit — no GitHub Release)"
    else
        say "→ publish (tag, GitHub Release, static surface, marker commit)"
    fi
    # shellcheck disable=SC2086
    bash tools/release.sh ${verb} "${comp}" "${stamp}" 2>&1 | tee -a "$LOG"
    rc="${PIPESTATUS[0]}"
    [ "${rc}" -eq 0 ] || { say "✗ ${comp} publish failed (exit ${rc}) — later components NOT cut"; say "   already-published components above are PUBLISHED: drop them from COMPONENTS before re-running"; exit "${rc}"; }
    CUT_DONE="${CUT_DONE:+${CUT_DONE} }${comp}"

    state="$(tree_state)" || die "cannot read git status — refusing to push (is git reachable on PATH set by ${ENV_FILE}?)"
    [ -z "${state}" ] || die "${comp} cut left an unclean tree — refusing to push; inspect before continuing"

    subject="$($GIT log -1 --format=%s)" || die "cannot read HEAD subject — refusing to push"
    case "${subject}" in
        "[RELEASED: ${comp}]"*|"[RELEASED: ${comp} beta]"*)
            push_marker "${comp}"
            ;;
        *)
            ahead="$(unpushed_count)" \
                || die "cannot reach origin to check for unpushed work after ${comp}"
            if [ "${ahead}" = "0" ]; then
                say "→ ${comp}: no marker and nothing unpushed — re-cut at an identical stamp; the marker for it is already in history"
            else
                die "${comp}: HEAD is not a [RELEASED: ${comp}] marker (got: ${subject}) yet ${ahead} commit(s) are unpushed — the cut published something it did not record; inspect before continuing"
            fi
            ;;
    esac
done

say ""
exit 0

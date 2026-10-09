#!/bin/bash
# release.command — run this repo's release cut in a DESKTOP session.
#
# Ported from clawee-git/release (#25), which ported it from burrowee-git/release.
# The guards below are theirs and are deliberately kept identical; what differs is
# the CUT ITSELF, because this repo's release tooling has a different shape (see
# "Two phases" below).
#
# Not a release step. It launches rkit and tools/release.sh unmodified; every
# decision about what a cut does still lives there. This exists for one reason:
#
#   Signing and notarizing are different capabilities. rcodesign is pure
#   userspace and signs in any session. notarytool reaches Apple through
#   CFNetwork/AppSSO, which needs a per-user bootstrap namespace — in a
#   background/daemon-hosted shell it does not crash politely, it SIGTRAPs with
#   no submission id, and the cut can only report `status: unknown`. That reads
#   like a vendor outage and is not one.
#
# LaunchServices opens a .command in the desktop's own terminal, which IS such a
# session — no Apple Events, no TCC prompt, no sudo. Hence the extension: this
# file must be openable, not merely executable.
#
#   open tools/release.command        # committed 100755; no chmod needed
#
# Two phases, unlike clawee/burrowee. This repo has NO shell build path:
# `rkit build` produces, signs, notarizes and CVE-gates into dist/<stamp>/, and
# `tools/release.sh --distribute-only <comp> <stamp>` publishes that staged
# directory. So each component here is build -> resolve stamp -> distribute ->
# push marker, where the siblings have a single release.sh invocation. FLAGS are
# passed to the BUILD half, which is the half that takes --public/--apple.
#
# Inputs live OUTSIDE this repo or are ignored by it. This repo is public, so it
# names none of them: no host, no credential, no machine path, and no location
# where an operator keeps either. Every one is a variable you set, with no
# default pointing anywhere:
#
#   .release-env      a gitignored file you create in this repo (a symlink to
#                     your real one is fine), sourced before anything else. It
#                     puts the build and signing toolchain on PATH, selects the
#                     signing backends, and exports the two below. Override the
#                     location with RELEASE_ENV.
#   RELEASE_CONFIG    exported by that file. A directory holding this channel's
#                     sealed configuration: the signing key and the publish
#                     destination release.sh demands and never defaults.
#   RELEASE_IDENTITY  exported by that file. The identity that decrypts what is
#                     in RELEASE_CONFIG.
#   .release-request  what to cut, written per run. Override with
#                     RELEASE_REQUEST. Sourced as shell. Shape:
#                         COMPONENTS="umbreed"
#                         FLAGS="--public"
#                         CHANNEL="stable"        # or beta; default stable
#                         BETA_BRANCH=""          # beta only; see .release-request.example
#
# Channel. CHANNEL="beta" cuts from each component's code/beta sibling
# worktree (a LINKED worktree of the registry main repo, on the beta branch,
# == origin/<beta branch>), stamps v<X.Y.Z>.beta.<date>.<sha>, and publishes
# with `release.sh --channel beta` — R2-only, no GitHub Release. The
# cut-origin guard (tools/release_origin.sh) runs BEFORE `rkit build` for the
# component source and for this repo, so a stale tree or an unpushed main is
# refused before anything is bumped or built. It never creates a worktree.
# Beside it runs the sync-back check (origin/main must be contained in
# origin/dev, dev.md check 4), and after each marker push this launcher
# carries the marker into dev — a fast-forward, or a merge when dev has
# diverged — so the next component passes that check.
#
# Output: .release.log, ending in RELEASE-EXIT:<code> so a watcher can block on it
# rather than guess when the run finished. Exactly one run per log — the previous
# run is rotated to .release.log.prev, so a refusal never destroys the record of
# the last real cut. Override the log with RELEASE_LOG.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)" || exit 1
cd "$REPO_ROOT" || exit 1

# The rest of tools/ calls git by absolute path: the per-directory PATH hook on
# this tree strips Homebrew, and the operator environment file rewrites PATH further down. A guard
# that silently loses its git is a guard that passes.
GIT=/usr/bin/git

LOG="${RELEASE_LOG:-$REPO_ROOT/.release.log}"
[ -e "$LOG" ] && mv -f "$LOG" "${LOG}.prev" 2>/dev/null
if ! : > "$LOG"; then
    echo "✗ cannot write log: $LOG" >&2
    exit 1
fi

say() { echo "$@" | tee -a "$LOG"; }
die() { say "✗ $*"; exit 1; }

# ONE emitter for the sentinel, and one place the decrypted key is destroyed.
# Hand-written sentinels covered only the paths someone remembered: closing the
# Terminal window (SIGHUP — the expected way an operator abandons a .command),
# Ctrl-C, and `set -u` tripping inside a sourced file all left a watcher blocked
# forever AND, here, would have left a plaintext signing key in /tmp.
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

# 1. Session. Checked FIRST and refused loudly: the whole point of this file is
#    that the wrong session builds and signs for minutes before dying at notarize.
#
#    managername alone is necessary, not sufficient. A daemon shell that re-execs
#    through `launchctl asuser <uid>` lands in the user's GUI domain and reports
#    Aqua while still lacking the console security session AppSSO needs, and a
#    sudo'd run inherits Aqua but notarizes against root's keychain. Each
#    condition refuses separately so the operator learns which one it was.
DOMAIN="$(launchctl managername 2>/dev/null || echo unknown)"
say "session-domain: ${DOMAIN}"
[ "${DOMAIN}" = "Aqua" ] || die "not a desktop session (need Aqua, got ${DOMAIN}) — 'open' this file, do not run it from a shell"
[ "$(id -u)" -ne 0 ] || die "running as root — notarization would use root's keychain; open this file as your own user"
[ -z "${SSH_CONNECTION:-}" ] || die "this is an SSH session — it has no console security session; open this file on the desktop"
[ -t 0 ] || die "stdin is not a terminal — this was not opened by LaunchServices"

# 2. One release at a time. Two `open`s (an agent racing an operator, or a
#    double-click) would otherwise interleave into one log and race each other's
#    marker commits and pushes.
LOCK_DIR="$REPO_ROOT/.release.lock"
mkdir "$LOCK_DIR" 2>/dev/null || die "a release is already running (lock: $LOCK_DIR) — remove it only if no cut is live"
LOCK="$LOCK_DIR"

# 3. Environment. Loaded, never embedded. Restore IFS afterwards: everything
#    below splits COMPONENTS and FLAGS on whitespace, and a sourced file that
#    leaves IFS changed would silently re-split them.
# Defaults to a gitignored file in this repo, NOT to a path in anyone's home:
# a public file may name its own repo-relative filenames, never where an
# operator keeps things. Point it at your real environment file however you
# like — a symlink is fine.
#
# The default matters because LaunchServices starts this with the GUI session's
# environment, which carries none of your shell exports. Requiring RELEASE_ENV
# to be pre-set would mean `open` could never work, which is the one way this
# file is meant to be run.
ENV_FILE="${RELEASE_ENV:-$REPO_ROOT/.release-env}"
[ -r "${ENV_FILE}" ] || die "env file not readable: ${ENV_FILE} — create it (or set RELEASE_ENV); it must put the toolchain on PATH and export RELEASE_CONFIG and RELEASE_IDENTITY"
# shellcheck source=/dev/null
. "${ENV_FILE}"
IFS=$' \t\n'
say "env: ${ENV_FILE}"

# 4. Request.
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
# BETA_BRANCH is read by tools/release_origin.sh (beta_branch_for) in this
# process and in the release.sh it spawns — exported once, here, only when
# the request set it, so a stale value cannot leak in from the environment.
[ -z "${BETA_BRANCH}" ] || export BETA_BRANCH
[ "${CHANNEL}" = beta ] || BETA_BRANCH=""

# Unknown names are refused before anything is built. `all` is not a thing this
# repo's release.sh accepts either, but it is spelled out because an operator
# arriving from the clawee/burrowee launchers will try it: batching without a
# push between components leaves markers unpushed under a HEAD that reads
# [RELEASED: <last>], which is the wedge this file exists to prevent.
set -f   # COMPONENTS/FLAGS are split on whitespace below; they must not glob
for comp in ${COMPONENTS}; do
    case "${comp}" in
        umbree|umbreed) ;;
        all) die "COMPONENTS=\"all\" is not usable here — list them instead: COMPONENTS=\"umbreed umbree\"" ;;
        *)   die "unknown component: ${comp} (expected umbree or umbreed)" ;;
    esac
done
say "request: ${COMPONENTS} [${FLAGS}]"
say "channel: ${CHANNEL}${BETA_BRANCH:+ (beta branch: ${BETA_BRANCH})}"

# A dry run must not publish. rkit's --dry-run builds without bumping the version
# or needing a real key; the publish half has its own --dry-run that validates the
# staged dir and prints "would: ..." without a single network write. Run BOTH so a
# rehearsal covers the whole chain, and skip the push path entirely.
DRY=0
case " ${FLAGS} " in *" --dry-run "*) DRY=1 ;; esac
[ "$DRY" -eq 0 ] || say "note: --dry-run — build and publish are both rehearsed, nothing is tagged, uploaded or pushed"

# 5. Component sources. Derived from the committed workspace layout (siblings of
#    this repo), never an absolute machine path in a public file. The operator
#    environment or .release-request may override either one.
#    On beta the source is the code/beta sibling worktree of each registry main
#    folder — derived, never configured on its own, so it cannot drift from the
#    registry entry (tools/release_origin.sh beta_worktree_for).
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

# 5b. Cut origin — BEFORE the sealed inputs are decrypted and before anything
#     is built: the cheap refusal the README promises. Each component's source
#     tree (registry main on stable, its code/beta linked worktree on beta) and
#     this repo (main, clean, == origin/main — with no staged tolerance here,
#     since nothing has been staged yet) are asserted; a dry run reports (⚠)
#     rather than refuses. tools/release_origin.sh calls git by absolute path,
#     so the PATH hook does not matter.
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
    # dev.md check 4: origin/main contained in origin/dev (tools/release_origin.sh
    # check_sync_back) — refuses a cut whose predecessor skipped the sync-back.
    check_sync_back "${comp}" "${src}" "${origin_mode}" 2>&1 | tee -a "$LOG"
    [ "${PIPESTATUS[0]}" -eq 0 ] || die "${comp}: main is not merged back into dev — nothing built"
done
assert_release_origin "release repo" "$REPO_ROOT" "$REPO_ROOT" "${origin_mode}" 2>&1 | tee -a "$LOG"
[ "${PIPESTATUS[0]}" -eq 0 ] || die "release repo is not in sync with origin/main — push or pull before cutting"
check_sync_back "release repo" "$REPO_ROOT" "${origin_mode}" 2>&1 | tee -a "$LOG"
[ "${PIPESTATUS[0]}" -eq 0 ] || die "release repo: main is not merged back into dev — nothing built"
say "✓ cut origin: ${COMPONENTS} (${CHANNEL}) and this repo, main contained in dev"

# 6. Sealed inputs: this channel's publish destination and its signing key.
#    release.sh REQUIRES them and refuses to invent them, because this repo is
#    public and any default would have to be the real thing.
#
#    Both locations are REQUIRED with no default. A default would name where an
#    operator keeps their secrets, which is exactly the kind of fact a public
#    file must not carry — and a wrong default is worse than none, because it
#    fails late instead of here.
# No apostrophes inside ${VAR:?word}: bash parses the word with quote handling,
# so a lone ' opens a quoted region that swallows the rest of the file and fails
# with a syntax error hundreds of lines later.
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

# The signing secret only ever exists as a chmod-600 tmpfile, destroyed by
# on_exit above on EVERY path including SIGHUP. Created with a private umask so
# it is never briefly world-readable between open and chmod.
KEYFILE="$(umask 077; mktemp -t umbree-rel-key)" || die "cannot create the key tmpfile"
if ! (umask 077; age -d -i "${AGE_ID}" "${DP_DIR}/umbree-release.key.age" > "${KEYFILE}"); then
    die "cannot decrypt ${DP_DIR}/umbree-release.key.age"
fi
[ -s "${KEYFILE}" ] || die "decrypted signing key is empty"

# tree_state — echoes porcelain output, non-zero if git itself failed.
#
# A bare `[ -n "$(git status --porcelain)" ]` fails OPEN: git's errors go to
# stderr and stdout is left empty, so a missing git, a held index.lock or an
# unreadable object store all read as "tree is clean" and the push proceeds.
# --untracked-files=all because a repo-local status.showUntrackedFiles=no would
# otherwise retire the untracked half of the check.
tree_state() { $GIT status --porcelain --untracked-files=all; }

# unpushed_count — commits on HEAD that origin/main does not have. Fetches
# first: nothing else re-verifies in-sync at the moment of the push, and a
# multi-minute cut is long enough for the remote to have moved.
unpushed_count() {
    $GIT fetch --quiet origin main || return 1
    $GIT rev-list --count FETCH_HEAD..HEAD
}

# push_marker <comp> — publish exactly the marker this component just wrote.
#
# `git push origin HEAD` would publish HEAD's whole unpushed ancestry to whatever
# branch HEAD is on. Assert branch, attachment and ahead-count here, at the
# moment of the push, and name the destination explicitly.
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

# sync_marker_into_dev <comp> — carry the marker push_marker just put on main
# down into dev, before the next component starts.
#
# Every push to main is merged straight back down into dev (dev.md, "main
# advances, dev follows"), and the cut is the work that advanced main. It also
# has to happen HERE, between components, not after the batch: release.sh
# re-runs the sync-back guard (tools/release_origin.sh check_sync_back) per
# component, so an unsynced marker from component 1 refuses component 2. That
# is Clawee 2026-09-14 exactly — the clawee cut pushed its marker, then claweed
# refused on the missing sync-back — and why this ships with the guard.
#
# Called only from push_marker, i.e. only after a marker really reached main:
# never on --dry-run (which pushes nothing) and never on the identical-stamp
# path (no marker). push_marker refuses any branch but main, so a beta-branch
# push never gets here; a beta-CHANNEL marker does, because this repo has no
# beta branch and records beta markers on main too — which advances main just
# the same.
#
# Three outcomes. Every write is a push; no local branch, index or working
# tree is touched, so the checkout this cut runs from is left exactly as the
# cut left it:
#   - the marker is already in dev          → say so, nothing to do
#   - dev is an ancestor of the marker      → fast-forward dev to it
#   - dev has commits main does not         → merge the marker into dev
#     (merge_marker_into_dev), which is dev's NORMAL state: it carries
#     unreleased work between releases.
# Every push is non-force: the remote refuses a non-fast-forward, so a dev that
# moved between this fetch and the push is refused rather than rewound.
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

# merge_marker_into_dev <comp> <new> — the sync-back as a real merge, for a dev
# that has diverged from main.
#
# Merging rather than stopping is an operator decision (2026-09-14): dev
# normally carries unreleased work, so a launcher that refused whenever dev
# could not fast-forward would halt most stable cuts after their first
# component. This is the merge dev.md prescribes for the sync-back — a merge,
# never a rebase — with dev as first parent and main second, exactly what
# `git merge origin/main` on dev would record; and no content is decided here,
# because a conflict stops the launcher for a human instead.
#
# Built with no checkout: `git merge-tree --write-tree` computes the merged
# tree in the object store (exit 1 = conflicts, with the paths named after the
# tree id; needs git 2.38 or newer), `git commit-tree` records it, and one
# non-force push publishes it. The message is fixed and carries no attribution.
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

# dev_sync_fix <comp> — the manual step every dev-sync refusal names, spelled
# once so the refusals cannot drift apart. CUT_DONE is every component already
# published in this run, this one included: a re-run must drop them all.
dev_sync_fix() {
    printf '%s' "merge origin/main into dev by hand (git merge — never a rebase, which drops the merge commits and takes main back out of dev), push dev, then, if components remain, re-run with ${CUT_DONE:-$1} dropped from COMPONENTS"
}

# 7. Cut each component: build, resolve its stamp, publish it, push its marker
#    before the next one starts.
#
#    release.sh publishes the release and then records a [RELEASED: <comp>]
#    marker commit — but it deliberately never pushes. Leave that marker sitting
#    while the next component cuts and the repo carries two unrecorded releases
#    at once, with the ahead-count assertion above no longer able to tell which
#    marker belongs to which cut. Worse here than in the siblings: this repo's
#    own pre-flight refuses to cut while the release repo is ahead of its remote,
#    so an unpushed marker does not merely confuse the next component, it aborts it.
#    The same holds for dev: push_marker carries the marker into dev
#    (sync_marker_into_dev), or the next component's sync-back check refuses.
CUT_DONE=""
for comp in ${COMPONENTS}; do
    say ""
    say "── cut: ${comp} ──"

    say "→ build (rkit: cross-compile, sign, notarize, CVE gate as FLAGS direct; channel ${CHANNEL})"
    # shellcheck disable=SC2086
    go run ./cmd/rkit build --component "${comp}" --channel "${CHANNEL}" ${FLAGS} --sign-key "${KEYFILE}" 2>&1 | tee -a "$LOG"
    rc="${PIPESTATUS[0]}"
    [ "${rc}" -eq 0 ] || { say "✗ ${comp} build failed (exit ${rc}) — later components NOT cut"; exit "${rc}"; }

    # The stamp is resolved from the version file AFTER the build, so it reflects
    # any bump the build just applied. Same call the e2e harness makes.
    src_var="UMBREE_SRC_$(printf '%s' "${comp}" | tr '[:lower:]' '[:upper:]')"
    stamp="$(SRC_DIR="${!src_var}" bash tools/version.sh "${comp}" --channel "${CHANNEL}" --stamp)" \
        || die "${comp}: cannot resolve the built stamp"

    # The publish verb is the channel's: --distribute-only is the GitHub
    # Release path (stable); --channel beta is the R2-only path.
    if [ "${CHANNEL}" = beta ]; then verb="--channel beta"; else verb="--distribute-only"; fi
    say "→ stamp: ${stamp}"

    if [ "$DRY" -eq 1 ]; then
        say "→ publish (rehearsal)"
        # shellcheck disable=SC2086  # verb is one or two words, split on purpose
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
    # shellcheck disable=SC2086  # verb is one or two words, split on purpose
    bash tools/release.sh ${verb} "${comp}" "${stamp}" 2>&1 | tee -a "$LOG"
    rc="${PIPESTATUS[0]}"
    [ "${rc}" -eq 0 ] || { say "✗ ${comp} publish failed (exit ${rc}) — later components NOT cut"; say "   already-published components above are PUBLISHED: drop them from COMPONENTS before re-running"; exit "${rc}"; }
    # Published from here on: a refusal below (push, dev sync) must tell the
    # operator to drop this one too when re-running.
    CUT_DONE="${CUT_DONE:+${CUT_DONE} }${comp}"

    state="$(tree_state)" || die "cannot read git status — refusing to push (is git reachable on PATH set by ${ENV_FILE}?)"
    [ -z "${state}" ] || die "${comp} cut left an unclean tree — refusing to push; inspect before continuing"

    subject="$($GIT log -1 --format=%s)" || die "cannot read HEAD subject — refusing to push"
    case "${subject}" in
        "[RELEASED: ${comp}]"*|"[RELEASED: ${comp} beta]"*)
            push_marker "${comp}"
            ;;
        *)
            # One legitimate reason HEAD is not a marker: a re-cut at an
            # identical stamp produces a byte-identical tree, the marker commit
            # records nothing, and the repo is left IN SYNC. That is the only
            # shape allowed to pass silently. Anything unpushed here means the
            # cut published something it did not record.
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

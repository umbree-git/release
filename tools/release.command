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
COMPONENTS=""; FLAGS=""
# shellcheck source=/dev/null
. "${REQUEST}"
IFS=$' \t\n'
[ -n "${COMPONENTS}" ] || die "request names no COMPONENTS: ${REQUEST}"

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
BRAND_ROOT="$(cd "$REPO_ROOT/../../.." && pwd)" || die "cannot resolve the brand root above this repo"
export UMBREE_SRC_UMBREE="${UMBREE_SRC_UMBREE:-$BRAND_ROOT/cli/code/main}"
export UMBREE_SRC_UMBREED="${UMBREE_SRC_UMBREED:-$BRAND_ROOT/daemon/code/main}"

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
for comp in ${COMPONENTS}; do
    say ""
    say "── cut: ${comp} ──"

    say "→ build (rkit: cross-compile, sign, notarize, CVE gate as FLAGS direct)"
    # shellcheck disable=SC2086
    go run ./cmd/rkit build --component "${comp}" ${FLAGS} --sign-key "${KEYFILE}" 2>&1 | tee -a "$LOG"
    rc="${PIPESTATUS[0]}"
    [ "${rc}" -eq 0 ] || { say "✗ ${comp} build failed (exit ${rc}) — later components NOT cut"; exit "${rc}"; }

    # The stamp is resolved from the version file AFTER the build, so it reflects
    # any bump the build just applied. Same call the e2e harness makes.
    src_var="UMBREE_SRC_$(printf '%s' "${comp}" | tr '[:lower:]' '[:upper:]')"
    stamp="$(SRC_DIR="${!src_var}" bash tools/version.sh "${comp}" --stamp)" \
        || die "${comp}: cannot resolve the built stamp"
    say "→ stamp: ${stamp}"

    if [ "$DRY" -eq 1 ]; then
        say "→ publish (rehearsal)"
        bash tools/release.sh --distribute-only "${comp}" "${stamp}" --dry-run 2>&1 | tee -a "$LOG"
        [ "${PIPESTATUS[0]}" -eq 0 ] || die "${comp}: publish rehearsal failed"
        say "→ ${comp}: --dry-run, nothing published and nothing to push"
        continue
    fi

    say "→ publish (tag, GitHub Release, static surface, marker commit)"
    bash tools/release.sh --distribute-only "${comp}" "${stamp}" 2>&1 | tee -a "$LOG"
    rc="${PIPESTATUS[0]}"
    [ "${rc}" -eq 0 ] || { say "✗ ${comp} publish failed (exit ${rc}) — later components NOT cut"; say "   already-published components above are PUBLISHED: drop them from COMPONENTS before re-running"; exit "${rc}"; }

    state="$(tree_state)" || die "cannot read git status — refusing to push (is git reachable on PATH set by ${ENV_FILE}?)"
    [ -z "${state}" ] || die "${comp} cut left an unclean tree — refusing to push; inspect before continuing"

    subject="$($GIT log -1 --format=%s)" || die "cannot read HEAD subject — refusing to push"
    case "${subject}" in
        "[RELEASED: ${comp}]"*)
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

#!/usr/bin/env bash
# release.sh — PUBLISH an already-staged umbree release (distribute-only).
#
# Usage:
#   bash tools/release.sh --distribute-only umbree <stamp> [--dry-run]
#
# This repo has NO shell build path. Building, signing, and (with --apple)
# notarizing the artifact set live entirely in `rkit build` (the produce half),
# which stamps + cross-compiles umbree for darwin/{arm64,amd64} + linux/{arm64,
# amd64}, writes SHA256SUMS.txt, and minisign-signs it into dist/<stamp>/.
#
# --distribute-only publishes THAT already-staged dist/<stamp>/ WITHOUT building,
# signing, notarizing, or bumping a version — it runs only the publish half:
#   1. git-tags umbree/<stamp> + publishes a GitHub Release on umbree-git/release.
#   2. regenerates the outer bootstrap + version JSONP and scp's the static
#      surface (install.sh, version.js, umbree-release.pub, site/index.html) to
#      the release host.
#   3. records a [RELEASED: umbree] marker commit.
# umbree ships a single component and hosts its zips on GitHub Releases — there
# is no R2 mirror, no console/dispatcher, and nothing to skip there.
#
# On --dry-run: validates the staged dir + component, then prints "would: ..."
# for every publish action and returns — no ghp/git/ssh/scp/network writes.
#
# Env (all optional — sane defaults below):
#   RELEASE_HOST           ssh alias for the nginx static host (default nsm.renative.com)
#   STATIC_DIR             absolute static dir on that host
#   UMBREE_SRC_UMBREE      umbree component source worktree (default: cli main worktree)
#   UMBREE_RELEASE_REPO    GitHub repo for releases (default umbree-git/release)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# --distribute-only <umbree> <stamp> [--dry-run]: takes its component + stamp as
# positional args right after the flag. This repo has no build path, so this is
# the ONLY entry action — anything else is a usage error.
DIST_COMP=""; DIST_STAMP=""
if [ "${1:-}" = "--distribute-only" ]; then
    shift
    DIST_COMP="${1:-}"; DIST_STAMP="${2:-}"
    [ -n "${DIST_COMP}" ] && [ -n "${DIST_STAMP}" ] \
        || { echo "✗ usage: release.sh --distribute-only umbree <stamp> [--dry-run]" >&2; exit 2; }
    shift 2
else
    case "${1:-}" in
        # Print the whole header comment (line 2 → the first non-# line), so
        # added doc lines are never silently truncated by a hardcoded range.
        -h|--help) awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; exit 0 ;;
        *) echo "✗ usage: release.sh --distribute-only umbree <stamp> [--dry-run]" >&2; exit 2 ;;
    esac
fi

# ---- args: --distribute-only accepts ONLY --dry-run -------------------------
# Publishing an already-staged dist/ takes no build/sign/notarize/bump flags —
# those already ran in `rkit build`. Accepting them would silently set unused
# vars and imply behavior that never happens under this mode.
DRY_RUN=0
for arg in "$@"; do
    case "${arg}" in
        --dry-run) DRY_RUN=1 ;;
        -h|--help) awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; exit 0 ;;
        *) echo "✗ --distribute-only accepts only --dry-run (got '${arg}')" >&2; exit 2 ;;
    esac
done

# ---- config / defaults ------------------------------------------------------
RELEASE_HOST="${RELEASE_HOST:-nsm.renative.com}"
STATIC_DIR="${STATIC_DIR:-/ebs_storage/apps/release.umbree.org/static}"
RELEASE_REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"

# component source worktree (default: the umbree MAIN worktree — the cli repo,
# which ships cmd/umbree).
SRC_UMBREE="${UMBREE_SRC_UMBREE:-/Volumes/MacintoshED/Workstation/Coding/Umbree/cli/code/cli}"

src_for() {
    case "$1" in
        umbree)  printf '%s' "${SRC_UMBREE}" ;;
    esac
}

GHP="$(command -v ghp 2>/dev/null || echo "${HOME}/.claude/bin/ghp")"

# ---- distribute_only: distribution-only mode over an already-staged
# dist/<stamp>/ (produced by `rkit build` — the produce half lives there now).
# Runs ONLY: tag + GitHub Release -> gen-bootstraps.sh -> gen-version-jsonp.sh ->
# scp install.sh/version.js/pubkey/site to the release host -> [RELEASED] marker
# commit. No build, no sign, no notarize, no version bump — all of that already
# happened upstream in `rkit build`. umbree has no R2 mirror and no console/
# register step — nothing to skip there.
#
# On --dry-run: validates the staged dir + component, then prints "would: ..."
# for every publish action and returns — no ghp/git/ssh/scp/network writes.
distribute_only() {
    local comp="$1" stamp="$2"
    case "${comp}" in
        umbree) ;;
        *) echo "✗ unknown component: ${comp}" >&2; exit 1 ;;
    esac

    local stage="${REPO_ROOT}/dist/${stamp}"
    [ -d "${stage}" ] || { echo "✗ staged dir missing: ${stage} (run rkit build first)" >&2; exit 1; }
    for f in SHA256SUMS.txt SHA256SUMS.txt.minisig; do
        [ -f "${stage}/${f}" ] || { echo "✗ missing ${f} in ${stage} (rkit build must produce it)" >&2; exit 1; }
    done
    compgen -G "${stage}/${comp}-*.zip" >/dev/null \
        || { echo "✗ no ${comp}-*.zip found in ${stage} (rkit build must produce it)" >&2; exit 1; }

    local src
    src="$(src_for "${comp}")"
    [ -d "${src}" ] || { echo "✗ ${comp} source worktree missing: ${src}" >&2; exit 1; }

    if [ "${DRY_RUN}" = 1 ]; then
        echo "would: gh release create ${comp}/${stamp} (GitHub Release, public) via ghp"
        echo "would: gen-bootstraps.sh (regenerate ${comp}/install.sh)"
        echo "would: gen-version-jsonp.sh ${comp} (regenerate ${comp}/version.js)"
        echo "would: scp install.sh/version.js/umbree-release.pub/site/index.html to ${RELEASE_HOST}:${STATIC_DIR}/${comp}/"
        echo "would: marker commit [RELEASED: ${comp}] ${stamp}"
        echo "✓ dry-run distribute-only: no real writes"
        return 0
    fi

    command -v ghp >/dev/null 2>&1 || { echo "✗ required tool not found: ghp" >&2; exit 1; }
    [ -x "${GHP}" ] || { echo "✗ ghp wrapper not found at ${GHP}" >&2; exit 1; }
    "${GHP}" repo view "${RELEASE_REPO}" --json name >/dev/null 2>&1 \
        || { echo "✗ ghp cannot access ${RELEASE_REPO} — check gh.account + auth" >&2; exit 1; }
    # Upfront reachability check — without it a down host fails fast only at the
    # scp below, AFTER the tag + GitHub Release already published.
    ssh -o BatchMode=yes -o ConnectTimeout=5 "${RELEASE_HOST}" 'true' 2>/dev/null \
        || { echo "✗ cannot ssh to ${RELEASE_HOST}" >&2; exit 1; }

    # (1) tag + GitHub Release.
    # Change summary: component commits since the previous release's source sha.
    # The stamp's trailing field IS the 8-char source sha, so the previous
    # release's sha is the suffix of the highest existing umbree/v… tag.
    local prev_tag prev_sha changes
    prev_tag="$(/usr/bin/git tag -l "${comp}/v*" --sort=version:refname | tail -n1)"
    prev_sha="${prev_tag##*.}"
    if [ -n "${prev_sha}" ] && git -C "${src}" cat-file -e "${prev_sha}^{commit}" 2>/dev/null; then
        changes="$(git -C "${src}" log --oneline --no-merges "${prev_sha}..HEAD" 2>/dev/null)"
        [ -n "${changes}" ] || changes="No code changes since ${prev_tag} (re-release)."
    else
        changes="Initial release."
    fi

    local tag="${comp}/${stamp}"
    if git rev-parse "refs/tags/${tag}" >/dev/null 2>&1; then
        echo "✗ tag ${tag} already exists locally" >&2
        exit 1
    fi
    git tag -a "${tag}" -m "umbree ${stamp}"

    local notes; notes="${stage}/release-notes.md"
    cat > "${notes}" <<NOTES
umbree ${stamp} — $(date -u +%Y-%m-%d)

## Changes
${changes}

Install:
  curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/${comp}/install.sh | sh

Pin this version:
  UMBREE_VERSION=${tag} \\
    curl -fsSL https://release.umbree.org/${comp}/install.sh | sh

Verify by hand:
  minisign -Vm SHA256SUMS.txt -P "\$(cat umbree-release.pub | tail -n1)"
  shasum -a 256 -c SHA256SUMS.txt
NOTES

    ( cd "${stage}" && "${GHP}" -R "${RELEASE_REPO}" release create "${tag}" \
        --title "${comp} ${stamp}" --notes-file "${notes}" \
        "${comp}"-*.zip SHA256SUMS.txt SHA256SUMS.txt.minisig )

    # (2) regenerate bootstraps + version JSONP, then scp the static surface.
    bash "${REPO_ROOT}/tools/gen-bootstraps.sh" >&2
    bash "${REPO_ROOT}/tools/gen-version-jsonp.sh" "${comp}" >&2

    # shellcheck disable=SC2029  # ${STATIC_DIR}/${comp} are local, controlled values — expanding client-side into the remote command is intended.
    ssh "${RELEASE_HOST}" "mkdir -p '${STATIC_DIR}/${comp}'"
    scp -q "${REPO_ROOT}/${comp}/install.sh" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/install.sh"
    scp -q "${REPO_ROOT}/${comp}/version.js" "${RELEASE_HOST}:${STATIC_DIR}/${comp}/version.js"
    if [ -f "${REPO_ROOT}/umbree-release.pub" ]; then
        scp -q "${REPO_ROOT}/umbree-release.pub" "${RELEASE_HOST}:${STATIC_DIR}/umbree-release.pub"
    fi
    # site/index.html is added in Task 8; guard so its absence isn't fatal.
    if [ -f "${REPO_ROOT}/site/index.html" ]; then
        scp -q "${REPO_ROOT}/site/index.html" "${RELEASE_HOST}:${STATIC_DIR}/index.html"
    fi

    # (3) marker commit.
    git add "versions/${comp}" "${comp}/install.sh" "${comp}/version.js"
    git commit -m "[RELEASED: ${comp}] $(date -u +%Y-%m-%d) ${stamp}"

    echo "✓ distributed ${tag}"
    echo "  Release: https://github.com/${RELEASE_REPO}/releases/tag/${tag}"
}

distribute_only "${DIST_COMP}" "${DIST_STAMP}"

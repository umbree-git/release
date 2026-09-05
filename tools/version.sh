#!/usr/bin/env bash
# version.sh — per-component version + deploy stamp for the Umbree release repo.
#
# Each component (umbree|umbreed) has its own one-line MAJOR.MINOR.PATCH file
# under versions/<comp> — the single source of truth for that component's semver
# segment. This composes the full stamp used in ldflags, git tags, and marker
# commits:
#
#   v<X.Y.Z>.<YYYY>.<MM>.<DD>.<sha8>
#
# where <sha8> = the HEAD short hash of the COMPONENT SOURCE worktree
# (pass its path via SRC_DIR), and the date is today (UTC).
#
# A second channel, beta, tracks its own semver in versions/<comp>.beta —
# that file's PRESENCE is the open-beta-cycle marker (beta.md §1) — and its
# stamp carries an extra .beta. segment between the semver and the date:
#
#   v<X.Y.Z>.beta.<YYYY>.<MM>.<DD>.<sha8>
#
# so a whole-string `sort -V` still orders by semver first, and one anchored
# regex tells the two channels apart. (Copied from burrowee-git/release
# tools/version.sh; --seed is umbree's.)
#
# Usage:
#   tools/version.sh <comp> [--channel stable|beta] --semver       # just X.Y.Z
#   tools/version.sh <comp> [--channel stable|beta] --stamp        # full stamp (needs SRC_DIR)
#   tools/version.sh <comp> [--channel stable|beta] --bump-patch   # X.Y.(Z+1)  + git add versions/<comp>[.beta]
#   tools/version.sh <comp> [--channel stable|beta] --bump-minor   # X.(Y+1).0  + git add versions/<comp>[.beta] (gated)
#   tools/version.sh <comp> [--channel stable|beta] --bump-major   # (X+1).0.0  + git add versions/<comp>[.beta] (gated)
#   tools/version.sh <comp> --channel beta --seed
#                       # OPEN a cycle: write versions/<comp>.beta = <stable-major>.<stable-minor+1>.0
#                       # (0.1.8 → 0.2.0). Refuses (exit 1) while the file exists — a cycle is
#                       # open; close it (remove the file) first. Operator step, never run by a cut.
#   tools/version.sh <comp> --channel beta --assert-beta-above-stable
#                       # refuses (exit 1) unless versions/<comp>.beta > versions/<comp>
#
# --channel defaults to stable. Minor/major prompt unless UMBREE_RELEASE_YES=1
# (or non-TTY → refuse).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

COMP="${1:-}"
case "${COMP}" in
    umbree|umbreed) ;;
    "")  echo "✗ usage: version.sh <umbree|umbreed> [--channel stable|beta] <action>" >&2; exit 2 ;;
    *)   echo "✗ unknown component: ${COMP}" >&2; exit 2 ;;
esac

CHANNEL=stable
if [ "${2:-}" = "--channel" ]; then
    CHANNEL="${3:-}"; shift 2
    case "${CHANNEL}" in
        stable|beta) ;;
        *) echo "✗ --channel must be stable or beta (got '${CHANNEL}')" >&2; exit 2 ;;
    esac
fi

VERSION_FILE="${REPO_ROOT}/versions/${COMP}"
[ "${CHANNEL}" = beta ] && VERSION_FILE="${REPO_ROOT}/versions/${COMP}.beta"
VERSION_REL="${VERSION_FILE#"${REPO_ROOT}"/}"

# require_version_file — every action except --seed reads VERSION_FILE, so
# its absence is that action's failure; --seed is the one action that CREATES
# it, so the guard is a function each action calls rather than a top-level
# check that would refuse the seed too.
require_version_file() {
    [ -f "${VERSION_FILE}" ] || {
        if [ "${CHANNEL}" = beta ]; then
            echo "✗ ${VERSION_REL} not found at ${VERSION_FILE} — no beta cycle is open; open one with: tools/version.sh ${COMP} --channel beta --seed" >&2
        else
            echo "✗ ${VERSION_REL} not found at ${VERSION_FILE}" >&2
        fi
        exit 1
    }
}

read_semver_file() { # <file> <rel> — the X.Y.Z in <file>, refused unless well-formed
    local raw; raw="$(tr -d '\r\n[:space:]' < "$1")"
    [[ "${raw}" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "✗ $2 '${raw}' not MAJOR.MINOR.PATCH" >&2; exit 1; }
    printf '%s' "${raw}"
}
read_semver() { require_version_file; read_semver_file "${VERSION_FILE}" "${VERSION_REL}"; }
# Side-effect: stages versions/<comp>[.beta] so the caller (rkit build / release.sh) can commit/revert it as one unit.
write_semver() { printf '%s\n' "$1" > "${VERSION_FILE}"; ( cd "${REPO_ROOT}" && git add "${VERSION_REL}" ); }

src_sha() {
    [ -n "${SRC_DIR:-}" ] || { echo "✗ --stamp needs SRC_DIR (the component source worktree)" >&2; exit 2; }
    [ -d "${SRC_DIR}" ]   || { echo "✗ SRC_DIR '${SRC_DIR}' not a directory" >&2; exit 1; }
    git -C "${SRC_DIR}" rev-parse --short=8 HEAD 2>/dev/null \
        || { echo "✗ SRC_DIR '${SRC_DIR}' is not a git worktree" >&2; exit 1; }
}
today_utc() { date -u +%Y.%m.%d; }
stamp() {
    if [ "${CHANNEL}" = beta ]; then
        printf 'v%s.beta.%s.%s' "$1" "$(today_utc)" "$2"
    else
        printf 'v%s.%s.%s' "$1" "$(today_utc)" "$2"
    fi
}

bump() {
    local kind="$1" cur major minor patch new
    cur="$(read_semver)"; IFS='.' read -r major minor patch <<<"${cur}"
    case "${kind}" in
        patch) new="${major}.${minor}.$((patch+1))" ;;
        minor) new="${major}.$((minor+1)).0" ;;
        major) new="$((major+1)).0.0" ;;
        *) echo "✗ unknown bump kind: ${kind}" >&2; exit 1 ;;
    esac
    if [ "${kind}" != "patch" ] && [ "${UMBREE_RELEASE_YES:-0}" != "1" ]; then
        [ -t 0 ] || { echo "✗ ${kind} bump ${cur}→${new} needs a TTY or UMBREE_RELEASE_YES=1" >&2; exit 1; }
        printf '%s %s bump %s → %s. Continue? [y/N] ' "${COMP}" "${kind}" "${cur}" "${new}" >&2
        local r; read -r r; case "${r}" in y|Y|yes|YES) ;; *) echo "✗ aborted" >&2; exit 1 ;; esac
    fi
    write_semver "${new}"; printf '%s\n' "${new}"
}

# seed — open a beta cycle: versions/<comp>.beta = stable minor + 1, patch 0
# (beta.md §8: every participating component is seeded at the same X.Y.0).
# Reads ONLY versions/<comp>: the release repo is the version authority; an
# embedded floor in a component tree (daemon/VERSION) does not participate.
seed() {
    [ "${CHANNEL}" = beta ] || { echo "✗ --seed needs --channel beta" >&2; exit 2; }
    if [ -e "${VERSION_FILE}" ]; then
        echo "✗ ${VERSION_REL} already exists ($(tr -d '[:space:]' < "${VERSION_FILE}")) — a beta cycle is open; close it (remove the file) before seeding again" >&2
        exit 1
    fi
    local stable_file="${REPO_ROOT}/versions/${COMP}" s major minor patch new
    [ -f "${stable_file}" ] || { echo "✗ versions/${COMP} not found at ${stable_file}" >&2; exit 1; }
    s="$(read_semver_file "${stable_file}" "versions/${COMP}")"
    IFS='.' read -r major minor patch <<<"${s}"
    new="${major}.$((minor+1)).0"
    write_semver "${new}"; printf '%s\n' "${new}"
}

assert_beta_above_stable() {
    [ "${CHANNEL}" = beta ] || { echo "✗ --assert-beta-above-stable needs --channel beta" >&2; exit 2; }
    local stable_file="${REPO_ROOT}/versions/${COMP}" b s
    [ -f "${stable_file}" ] || { echo "✗ versions/${COMP} not found at ${stable_file}" >&2; exit 1; }
    b="$(read_semver)"
    s="$(tr -d '\r\n[:space:]' < "${stable_file}")"
    if [ "$(printf '%s\n%s\n' "${s}" "${b}" | sort -V | tail -n1)" != "${b}" ] || [ "${s}" = "${b}" ]; then
        echo "✗ versions/${COMP}.beta (${b}) must sort above versions/${COMP} (${s}) — a beta must read newer than every stable so a beta host's \`version\` output and the follow-on stable cut both land right; fix with: tools/version.sh ${COMP} --channel beta --bump-minor (or --bump-patch)" >&2
        exit 1
    fi
}

case "${2:-}" in
    --semver)      read_semver; printf '\n' ;;
    --stamp)       _sv="$(read_semver)"; _sha="$(src_sha)"; stamp "${_sv}" "${_sha}"; printf '\n' ;;
    --bump-patch)  bump patch ;;
    --bump-minor)  bump minor ;;
    --bump-major)  bump major ;;
    --seed)        seed ;;
    --assert-beta-above-stable) assert_beta_above_stable ;;
    # Print the whole header comment (line 2 → the first non-# line), so added
    # doc lines are never silently truncated by a hardcoded range.
    -h|--help)     awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0" ;;
    "")            echo "✗ usage: version.sh ${COMP} [--channel stable|beta] <--semver|--stamp|--bump-patch|--bump-minor|--bump-major|--seed|--assert-beta-above-stable>" >&2; exit 2 ;;
    *)             echo "✗ unknown action: ${2}" >&2; exit 2 ;;
esac

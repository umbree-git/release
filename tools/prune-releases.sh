#!/usr/bin/env bash
# prune-releases.sh — keep only the newest N releases per component on the
# Umbree release repo, ON ONE CHANNEL; delete the older GitHub Releases AND
# their git tags (stable), or the older beta tags (beta — a beta is never a
# GitHub Release, so on that channel only the tag exists to delete).
#
# Copied from burrowee-git/release tools/prune-releases.sh; umbree differences:
# the GitHub CLI is ${UMBREE_GH:-gh} (this repo is public and names no internal
# wrapper), components come from `rkit components`, and the beta pass deletes
# tags rather than Releases.
#
# ORDERING: run this (the GitHub side) BEFORE tools/r2-mirror/cmd/r2-prune.
#
# Usage:
#   tools/prune-releases.sh            # DRY-RUN (default): list what would be deleted
#   tools/prune-releases.sh --execute  # actually delete
#
# Env (optional):
#   CHANNEL                 stable|beta (default stable)
#   KEEP                    newest versions to retain per component (default
#                           10 on stable, 1 on beta — beta is disposable, so
#                           cutting a new beta expires the previous one and
#                           prunes it now; no artifact-level rollback to it)
#   COMPONENTS              space-separated set (default: `go run ./cmd/rkit
#                           components` — the one list rkit builds from)
#   UMBREE_RELEASE_REPO     GitHub repo (default umbree-git/release)
#   UMBREE_GH               GitHub CLI to use (default `gh`)
#
# Per component it lists the release tags matching CHANNEL's anchored pattern
# (spec §4.1 — stable never contains ".beta.", beta always does, so the same
# tag can never be counted on both channels), version-sorts them with
# `sort -V` (so v0.1.12 > v0.1.9), keeps the highest KEEP, and deletes the rest
# via `gh release delete --cleanup-tag` (removes the Release AND the tag) on
# stable, or `git push --delete` of the tag on beta. A stable prune never
# counts or deletes a beta tag, and a beta prune never counts or deletes a
# stable one. A tag matching NEITHER shape is ignored on both passes.
set -euo pipefail
# The per-dir PATH hook on this tree strips /opt/homebrew/bin; re-add a sane
# PATH so grep/sort/sed/tr + gh resolve.
export PATH="/usr/bin:/bin:/opt/homebrew/bin:${PATH}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/.." && pwd)"

REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"
CHANNEL="${CHANNEL:-stable}"
case "${CHANNEL}" in
  stable|beta) ;;
  *) echo "✗ CHANNEL must be stable or beta (got '${CHANNEL}')" >&2; exit 2 ;;
esac
if [ "${CHANNEL}" = beta ]; then
  KEEP="${KEEP:-1}"
else
  KEEP="${KEEP:-10}"
fi
if [ -z "${COMPONENTS:-}" ]; then
  COMPONENTS="$(cd "${REPO_ROOT}" && go run ./cmd/rkit components | tr '\n' ' ')" \
    || { echo "✗ could not read the component list from rkit" >&2; exit 1; }
fi

EXECUTE=0
for a in "$@"; do
  case "$a" in
    --execute|--yes) EXECUTE=1 ;;
    -h|--help) awk 'NR==1{next} !/^#/{exit} {sub(/^# ?/,""); print}' "$0"; exit 0 ;;
    *) echo "✗ unknown argument: $a" >&2; exit 2 ;;
  esac
done

GH_CLI="${UMBREE_GH:-gh}"
command -v "${GH_CLI}" >/dev/null 2>&1 || { echo "✗ GitHub CLI not found: ${GH_CLI} (set UMBREE_GH to override)" >&2; exit 1; }

mode="DRY-RUN"; [ "$EXECUTE" = 1 ] && mode="EXECUTE"
echo "repo=${REPO}  channel=${CHANNEL}  keep=${KEEP}  components=[${COMPONENTS}]  mode=${mode}"
echo

# One API pass each. Stable: the Releases (a stable cut is a GitHub Release,
# and deleting one with --cleanup-tag removes its tag). Beta: the TAGS — a beta
# cut pushes a tag and creates no Release, so the Releases listing would never
# show one. --paginate walks every page so nothing is missed past page 1.
if [ "${CHANNEL}" = beta ]; then
  tags="$("${GH_CLI}" api "repos/${REPO}/git/matching-refs/tags/" --paginate --jq '.[].ref' | sed 's#^refs/tags/##')"
else
  tags="$("${GH_CLI}" api "repos/${REPO}/releases" --paginate --jq '.[].tag_name')"
fi

planned=0
for comp in ${COMPONENTS}; do
  # Anchored per spec §4.1 — a tag matching neither channel's pattern is
  # ignored here, same as everywhere else that consumes these tags.
  if [ "${CHANNEL}" = beta ]; then
    pattern="^${comp}/v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}\$"
  else
    pattern="^${comp}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}\$"
  fi
  sorted="$(printf '%s\n' "${tags}" | grep -E "${pattern}" | sort -V || true)"
  if [ -z "${sorted}" ]; then
    echo "[${comp}] no releases"
    continue
  fi
  n="$(printf '%s\n' "${sorted}" | grep -c . || true)"
  if [ "${n}" -le "${KEEP}" ]; then
    echo "[${comp}] ${n} release(s) ≤ keep=${KEEP} — nothing to prune"
    continue
  fi
  drop="$(( n - KEEP ))"
  echo "[${comp}] ${n} releases → keep newest ${KEEP}, remove ${drop}"
  echo "  keep:   $(printf '%s\n' "${sorted}" | tail -n "${KEEP}" | tr '\n' ' ')"
  printf '%s\n' "${sorted}" | head -n "${drop}" | while IFS= read -r tag; do
    [ -n "${tag}" ] || continue
    if [ "${EXECUTE}" = 1 ]; then
      if [ "${CHANNEL}" = beta ]; then
        # No Release to delete: remove the remote tag (and the local one, if
        # this checkout has it — prune runs from the release repo).
        if "${GH_CLI}" api -X DELETE "repos/${REPO}/git/refs/tags/${tag}" >/dev/null 2>&1; then
          /usr/bin/git -C "${REPO_ROOT}" tag -d "${tag}" >/dev/null 2>&1 || true
          echo "  ✓ deleted ${tag}"
        else
          echo "  ✗ FAILED to delete ${tag}"
        fi
      elif "${GH_CLI}" release delete "${tag}" -R "${REPO}" --yes --cleanup-tag >/dev/null 2>&1; then
        echo "  ✓ deleted ${tag}"
      else
        echo "  ✗ FAILED to delete ${tag}"
      fi
    else
      echo "  - would delete ${tag}"
    fi
  done
  planned="$(( planned + drop ))"
done

echo
if [ "${EXECUTE}" = 1 ]; then
  echo "✓ done — removed up to ${planned} release(s); kept newest ${KEEP} per component."
else
  echo "DRY-RUN: ${planned} release(s) would be removed. Re-run with --execute to apply."
fi

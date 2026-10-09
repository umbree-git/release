#!/usr/bin/env bash
set -euo pipefail
export PATH="/usr/bin:/bin:/opt/homebrew/bin:${PATH}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/.." && pwd)"

KEEP_STABLE_DEFAULT=5
KEEP_BETA_DEFAULT=1

usage() {
  cat <<EOF
Usage: tools/prune-releases.sh [--execute]

Keep the newest KEEP release tags per component on one channel and delete the
rest: GitHub Releases with their tags on stable, bare tags on beta. Without
--execute it lists what it would delete and deletes nothing.

Environment:
  CHANNEL               stable | beta (default stable)
  KEEP                  newest versions kept per component (default ${KEEP_STABLE_DEFAULT} on
                        stable, ${KEEP_BETA_DEFAULT} on beta); tools/retain-permanent pins are kept
                        in addition
  COMPONENTS            space-separated components (default: rkit components)
  UMBREE_RELEASE_REPO   GitHub repo (default umbree-git/release)
  UMBREE_GH             GitHub CLI to run (default gh)

Run it before tools/r2-mirror/cmd/r2-prune.
EOF
}

for a in "$@"; do
  case "$a" in
    -h|--help) usage; exit 0 ;;
  esac
done

REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"
CHANNEL="${CHANNEL:-stable}"
case "${CHANNEL}" in
  stable|beta) ;;
  *) echo "✗ CHANNEL must be stable or beta (got '${CHANNEL}')" >&2; exit 2 ;;
esac
if [ "${CHANNEL}" = beta ]; then
  KEEP="${KEEP:-${KEEP_BETA_DEFAULT}}"
else
  KEEP="${KEEP:-${KEEP_STABLE_DEFAULT}}"
fi
PERMANENT_FILE="${HERE}/retain-permanent"

is_permanent() {
  local tag="$1" stamp="${1##*/}" line
  [ -r "${PERMANENT_FILE}" ] || return 1
  while IFS= read -r line || [ -n "${line}" ]; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    case "${line}" in
      ''|'#'*) continue ;;
    esac
    if [ "${line}" = "${tag}" ] || [ "${line}" = "${stamp}" ]; then
      return 0
    fi
  done < "${PERMANENT_FILE}"
  return 1
}
if [ -z "${COMPONENTS:-}" ]; then
  COMPONENTS="$(cd "${REPO_ROOT}" && go run ./cmd/rkit components | tr '\n' ' ')" \
    || { echo "✗ could not read the component list from rkit" >&2; exit 1; }
fi

EXECUTE=0
for a in "$@"; do
  case "$a" in
    --execute|--yes) EXECUTE=1 ;;
    *) { echo "✗ unknown argument: $a"; echo; usage; } >&2; exit 2 ;;
  esac
done

GH_CLI="${UMBREE_GH:-gh}"
command -v "${GH_CLI}" >/dev/null 2>&1 || { echo "✗ GitHub CLI not found: ${GH_CLI} (set UMBREE_GH to override)" >&2; exit 1; }

mode="DRY-RUN"; [ "$EXECUTE" = 1 ] && mode="EXECUTE"
echo "repo=${REPO}  channel=${CHANNEL}  keep=${KEEP}  components=[${COMPONENTS}]  mode=${mode}"
echo

if [ "${CHANNEL}" = beta ]; then
  tags="$("${GH_CLI}" api "repos/${REPO}/git/matching-refs/tags/" --paginate --jq '.[].ref' | sed 's#^refs/tags/##')"
else
  tags="$("${GH_CLI}" api "repos/${REPO}/releases" --paginate --jq '.[].tag_name')"
fi

planned=0
for comp in ${COMPONENTS}; do
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
    if is_permanent "${tag}"; then
      echo "  keep permanent ${tag}"
      continue
    fi
    if [ "${EXECUTE}" = 1 ]; then
      if [ "${CHANNEL}" = beta ]; then
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

#!/bin/sh
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
GIT=/usr/bin/git

help() {
    cat <<'HELP'
Usage: tools/record-promoted.sh <component> <stamp>

Record, in this repo, that a stable release has gone public: the installers'
version floor follows the promote, never the cut.

It asks tools/promote-check.sh <component> stable --expect <stamp> once. Only a
live answer (exit 0) whose one output line names this component, the stable
channel and exactly <stamp> is accepted; not yet (1), cannot tell or a newer
version live (3) and anything else refuse, write nothing and exit with the
check's code (or 1). The stamp written comes from that verified line, never
from a second fetch.

On a live answer it writes versions/<component>.stamp, regenerates the
committed bootstraps with tools/gen-bootstraps.sh, and makes one marker commit
[PROMOTED: <component>] <stamp> carrying the floor and <component>/install.sh.
The served bootstrap was already republished by the manage service at the end
of the promote; this keeps the committed copy byte-identical to it. It pushes
nothing: push main and merge it into dev as after any marker.

It refuses, asking nothing, unless HEAD is the main branch and the tracked
tree is clean. A second run for the same stamp is a no-op.

  -h, --help  print this text
HELP
}

usage() { echo "✗ usage: tools/record-promoted.sh <component> <stamp>   (-h for help)" >&2; exit 2; }

case "${1:-}" in -h|--help) help; exit 0 ;; esac
[ $# -eq 2 ] || usage
COMP="$1"
STAMP="$2"
case "${COMP}" in ""|*/*|.*) usage ;; esac
[ -f "${ROOT}/versions/${COMP}" ] || { echo "✗ unknown component: ${COMP} (no versions/${COMP})" >&2; usage; }
printf '%s\n' "${STAMP}" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$' \
    || { echo "✗ not a stable stamp: ${STAMP} (want v<X.Y.Z>.<YYYY>.<MM>.<DD>.<sha8>)" >&2; usage; }

branch="$("${GIT}" -C "${ROOT}" symbolic-ref --quiet --short HEAD)" || branch="(detached HEAD)"
[ "${branch}" = main ] || { echo "✗ on ${branch}, not main — the [PROMOTED] marker belongs on main; nothing asked, nothing written" >&2; exit 1; }
dirty="$("${GIT}" -C "${ROOT}" status --porcelain --untracked-files=no)" \
    || { echo "✗ cannot read git status in ${ROOT}" >&2; exit 1; }
[ -z "${dirty}" ] || { echo "✗ the tree has uncommitted changes — commit or stash them first; nothing asked, nothing written" >&2; exit 1; }

rc=0
LINE="$(sh "${ROOT}/tools/promote-check.sh" "${COMP}" stable --expect "${STAMP}")" || rc=$?
case "${rc}" in
    0) ;;
    1) echo "✗ ${COMP} ${STAMP} is not live yet — record it after the promote; nothing written" >&2; exit 1 ;;
    3) echo "✗ promote-check cannot tell, or a newer version is live — ${COMP} ${STAMP} is not recorded; nothing written" >&2; exit 3 ;;
    *) echo "✗ promote-check exited ${rc} — nothing written" >&2; exit "${rc}" ;;
esac

set -f
set -- ${LINE}
set +f
if [ "$#" -lt 4 ] || [ "$1" != "${COMP}" ] || [ "$2" != stable ] || [ "$4" != "${STAMP}" ]; then
    echo "✗ the live answer names '${LINE}', not ${COMP} stable ${STAMP} — nothing written" >&2
    exit 1
fi
LIVE="$4"

printf '%s\n' "${LIVE}" > "${ROOT}/versions/${COMP}.stamp"
( cd "${ROOT}" && sh tools/gen-bootstraps.sh ) >&2 || {
    "${GIT}" -C "${ROOT}" checkout -q -- "versions/${COMP}.stamp" "${COMP}/install.sh"
    echo "✗ gen-bootstraps.sh failed — the floor and the bootstrap were restored; nothing recorded" >&2
    exit 1
}
"${GIT}" -C "${ROOT}" add -- "versions/${COMP}.stamp" "${COMP}/install.sh"
if "${GIT}" -C "${ROOT}" diff --cached --quiet; then
    echo "✓ ${COMP} ${LIVE} is already recorded — nothing to commit"
    exit 0
fi
"${GIT}" -C "${ROOT}" commit -q -m "[PROMOTED: ${COMP}] ${LIVE}"
echo "✓ recorded ${COMP} ${LIVE}: versions/${COMP}.stamp and ${COMP}/install.sh in one marker commit"
left="$("${GIT}" -C "${ROOT}" status --porcelain --untracked-files=no)"
[ -z "${left}" ] || echo "⚠ gen-bootstraps.sh also changed files this marker does not carry (the committed bootstraps were stale): ${left}" >&2

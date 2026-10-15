#!/bin/sh
set -eu
UMBREE_LADDER_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${UMBREE_LADDER_DIR}/lib.sh"

RUNNER="${UMBREE_LADDER_DIR}/run.sh"
[ $# -le 1 ] || { warn "usage: upgrade.sh [<version>]"; exit 64; }
[ -f "${RUNNER}" ] || fail "upgrade: ${RUNNER} is missing — refusing"

ROWS="$(ledger_rows "${RUNNER}")"
[ -n "${ROWS}" ] || fail "upgrade: ${RUNNER} carries an empty ledger — refusing"
TOP=""
while read -r target script; do
    if [ -z "${TOP}" ] || version_lt "${TOP}" "${target}"; then TOP="${target}"; fi
done <<EOT
${ROWS}
EOT

FLOOR="${TOP}"
[ $# -eq 0 ] || FLOOR="$1"
version_ok "${FLOOR}" || { warn "upgrade: '${FLOOR}' is not a numeric version"; warn "usage: upgrade.sh [<version>]"; exit 64; }
version_lt "${TOP}" "${FLOOR}" && fail "upgrade: floor ${FLOOR} is above the ladder's newest target ${TOP} — nothing would run; refusing"

say "upgrade: forcing the ladder from floor ${FLOOR}; selected rungs:"
while read -r target script; do
    version_lt "${target}" "${FLOOR}" || say "  ${script} (target ${target})"
done <<EOT
${ROWS}
EOT

set +e
sh "${RUNNER}" --assume-below "${FLOOR}" --rerun-recorded
rc=$?
set -e
exit "${rc}"

#!/usr/bin/env bash
set -euo pipefail

[ "$#" -ge 1 ] || { echo "usage: verify-no-env.sh <binary> [<binary> ...]" >&2; exit 2; }
command -v strings >/dev/null 2>&1 || { echo "✗ 'strings' not found" >&2; exit 2; }

FORBIDDEN='UMBREE_DATA_DIR|UMBREE_SOCKET|UMBREE_CONFIG|mustEnv'
rc=0
for bin in "$@"; do
    [ -f "${bin}" ] || { echo "✗ not a file: ${bin}" >&2; exit 2; }
    hits="$(strings "${bin}" | grep -E -c "${FORBIDDEN}" || true)"
    if [ "${hits}" -ne 0 ]; then
        echo "✗ ${bin}: ${hits} forbidden env literal(s):" >&2
        strings "${bin}" | grep -E -n "${FORBIDDEN}" | sed 's/^/    /' >&2
        rc=1
    else
        echo "✓ ${bin}: no forbidden env literals"
    fi
done
exit "${rc}"

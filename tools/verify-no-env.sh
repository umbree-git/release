#!/usr/bin/env bash
# verify-no-env.sh — fail if a built Umbree binary embeds a forbidden config-env
# runtime literal. umbree takes ALL runtime config from `~/.umbree/cli/config.json`
# and command-line flags — never from env, other than the one legitimate,
# standard exception below. This is the release-channel guard: run it on every
# freshly built component binary before publishing.
#
# Usage: verify-no-env.sh <binary> [<binary> ...]
# Forbidden literals (config-env names that must NOT drive runtime config):
#   UMBREE_DATA_DIR   the data dir must come from config.json/flags, not env
#   UMBREE_SOCKET     the transport socket must come from config.json/flags, not env
#   UMBREE_CONFIG     the config path itself must not be re-derived from env
#   mustEnv           the helper pattern that fatals on a missing required env
#
# NOTE: legitimate read-only env knobs are NOT forbidden — e.g. umbree honors
# the standard $XDG_CONFIG_HOME to relocate its config directory, and the
# build-time-only $UMBREE_CODESIGN_IDENTITY (consumed by the release tooling,
# never embedded at runtime) is also allowed; only the UMBREE_* config-as-env
# anti-pattern is rejected.
#
# Exit 0 = clean; 1 = a forbidden literal is present; 2 = usage/strings error.
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

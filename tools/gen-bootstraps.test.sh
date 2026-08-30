#!/usr/bin/env bash
# gen-bootstraps.test.sh — asserts a bootstrap is generated per component.
#
# gen-bootstraps.sh writes $ROOT/<comp>/install.sh, and those files are
# COMMITTED (only /dist/ and /build/ are ignored). So this runs the real
# generator against the real destination and asserts every component got one.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

"$ROOT/tools/gen-bootstraps.sh" >/dev/null

fail=0
for comp in umbree umbreed; do
    if [ ! -f "$ROOT/$comp/install.sh" ]; then
        echo "MISSING: $comp/install.sh" >&2
        fail=1
    fi
done
[ "$fail" -eq 0 ] && echo "ok: a bootstrap per component"
exit "$fail"

#!/usr/bin/env bash
# test-e2e.test.sh — proves tools/test-e2e.sh cannot install a system service
# on the machine that runs it.
#
# The regression this exists for: test-e2e.sh runs the REAL outer bootstrap,
# and for the umbreed component that bootstrap execs the daemon's canonical
# installer, which escalates with sudo and writes+loads a boot unit. The
# harness then rm -rf's the prefix on its next run, orphaning a loaded system
# service that points at a deleted binary. The harness shipped that way; only
# passing UMBREED_NO_SERVICE=1 through to the bootstrap stops it.
#
# This is a STATIC check by necessity — exercising it for real would mean
# building and serving a full release, which is what test-e2e.sh itself is
# for. It asserts the two properties that the fix consists of.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E="${HERE}/test-e2e.sh"

[ -r "${E2E}" ] || { echo "FAIL: ${E2E} not readable"; exit 1; }

fails=0

echo "# expect: the bootstrap invocation suppresses the service install"
# The assignment must sit in run_install's env prefix — the block that runs
# from `run_install()` to its closing brace.
run_install_body="$(awk '/^[[:space:]]*run_install\(\)/,/^[[:space:]]*}/' "${E2E}")"
if [ -z "${run_install_body}" ]; then
    echo "FAIL: could not find run_install() in ${E2E}"
    fails=1
elif ! printf '%s' "${run_install_body}" | grep -q 'UMBREED_NO_SERVICE=1'; then
    echo "FAIL: run_install() does not pass UMBREED_NO_SERVICE=1 — running this"
    echo "      harness for the umbreed component would install and load a real"
    echo "      system boot unit on the host, then orphan it."
    fails=1
else
    echo "run_install passes UMBREED_NO_SERVICE=1"
fi

echo "# expect: the harness never invokes a privileged service command itself"
# Comments legitimately discuss sudo/launchctl by name, so strip them before
# looking. Anchored on a word boundary so 'no-sudo' prose in a name doesn't hit.
if sed 's/#.*//' "${E2E}" | grep -nE '(^|[^[:alnum:]_-])(sudo|launchctl|systemctl)([^[:alnum:]_-]|$)'; then
    echo "FAIL: test-e2e.sh invokes a privileged service command directly (above)"
    fails=1
else
    echo "no direct sudo/launchctl/systemctl call"
fi

[ "${fails}" -eq 0 ] || exit 1
echo "ALL OK"

#!/usr/bin/env bash

module_gate() {
    local suite rc log
    for suite in test-modules.sh test-checksum-verify.sh test-install-minisign.sh sync-modules.test.sh public-hygiene.sh; do
        [ -f "${REPO_ROOT}/tools/${suite}" ] \
            || { echo "✗ module gate: ${suite} is missing from tools/" >&2; exit 1; }
        echo "→ module gate: ${suite}" >&2
        rc=0
        log="$(bash "${REPO_ROOT}/tools/${suite}" 2>&1)" || rc=$?
        if [ "${rc}" != 0 ]; then
            echo "${log}" >&2
            echo "✗ module gate: ${suite} failed (exit ${rc}) — release aborted" >&2
            if [ "${suite}" = test-modules.sh ]; then
                echo "  If this was the GENERATOR check: the regenerated bootstraps are in your" >&2
                echo "  working tree now. Run 'git diff' to see the drift, commit it, then re-cut." >&2
            fi
            exit 1
        fi
        echo "✓ module gate: ${suite} clean" >&2
    done
}

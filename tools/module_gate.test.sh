#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

fail=0
check() {
    if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi
}
check_contains() {
    case "$2" in
        *"$3"*) echo "ok: $1" ;;
        *) echo "FAIL: $1 — output does not contain '$3'"; fail=1 ;;
    esac
}
check_lacks() {
    case "$2" in
        *"$3"*) echo "FAIL: $1 — output unexpectedly contains '$3'"; fail=1 ;;
        *) echo "ok: $1" ;;
    esac
}

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

fake_repo() {
    local root="${WORK}/repo.$$.${RANDOM}" spec name code
    mkdir -p "${root}/tools"
    for spec in "$@"; do
        name="${spec%%:*}"; code="${spec##*:}"
        { echo '#!/bin/sh'
          echo "echo 'ran ${name}'"
          echo "echo 'detail line from ${name}' >&2"
          echo "exit ${code}"
        } > "${root}/tools/${name}"
        chmod 0755 "${root}/tools/${name}"
    done
    printf '%s\n' "${root}"
}

ALL_GREEN=(test-modules.sh:0 test-checksum-verify.sh:0 test-install-minisign.sh:0 sync-modules.test.sh:0 public-hygiene.sh:0)

root="$(fake_repo "${ALL_GREEN[@]}")"
out="$( REPO_ROOT="${root}"; source "${HERE}/module_gate.sh"; module_gate 2>&1 )"; rc=$?
check "all green: exit 0" "${rc}" "0"
check_contains "all green: test-modules.sh ran and passed"        "${out}" "✓ module gate: test-modules.sh clean"
check_contains "all green: test-checksum-verify.sh ran and passed" "${out}" "✓ module gate: test-checksum-verify.sh clean"
check_contains "all green: sync-modules.test.sh ran and passed"   "${out}" "✓ module gate: sync-modules.test.sh clean"
check_contains "all green: test-install-minisign.sh ran and passed" "${out}" "✓ module gate: test-install-minisign.sh clean"
check_contains "all green: public-hygiene.sh ran and passed"      "${out}" "✓ module gate: public-hygiene.sh clean"
check_lacks "all green: a passing suite's output is not echoed"   "${out}" "detail line from test-modules.sh"

root="$(fake_repo test-modules.sh:1 test-checksum-verify.sh:0 test-install-minisign.sh:0 sync-modules.test.sh:0 public-hygiene.sh:0)"
out="$( REPO_ROOT="${root}"; source "${HERE}/module_gate.sh"; module_gate 2>&1 )"; rc=$?
check "red test-modules.sh: exit 1"           "${rc}" "1"
check_contains "red test-modules.sh: says which suite" "${out}" "✗ module gate: test-modules.sh failed"
check_contains "red test-modules.sh: carries the suite's own output" "${out}" "detail line from test-modules.sh"

check_lacks "red test-modules.sh: later suite did not run" "${out}" "ran test-checksum-verify.sh"

check_contains "red test-modules.sh: points at the dirty tree" "${out}" "git diff"
root="$(fake_repo test-modules.sh:0 test-checksum-verify.sh:1 test-install-minisign.sh:0 sync-modules.test.sh:0 public-hygiene.sh:0)"
out="$( REPO_ROOT="${root}"; source "${HERE}/module_gate.sh"; module_gate 2>&1 )"; rc=$?
check "red checksum suite: exit 1" "${rc}" "1"
check_contains "red checksum suite: names itself" "${out}" "✗ module gate: test-checksum-verify.sh failed"
check_lacks "red checksum suite: no GENERATOR hint" "${out}" "git diff"

root="$(fake_repo test-modules.sh:0 test-checksum-verify.sh:0 test-install-minisign.sh:0 sync-modules.test.sh:1 public-hygiene.sh:0)"
out="$( REPO_ROOT="${root}"; source "${HERE}/module_gate.sh"; module_gate 2>&1 )"; rc=$?
check "red sync-modules.test.sh: exit 1" "${rc}" "1"
check_contains "red sync-modules.test.sh: names itself" "${out}" "✗ module gate: sync-modules.test.sh failed"

root="$(fake_repo test-checksum-verify.sh:0 test-install-minisign.sh:0 sync-modules.test.sh:0 public-hygiene.sh:0)"
out="$( REPO_ROOT="${root}"; source "${HERE}/module_gate.sh"; module_gate 2>&1 )"; rc=$?
check "missing suite: exit 1" "${rc}" "1"
check_contains "missing suite: names the missing file" "${out}" "test-modules.sh is missing"

gate_list="$(sed -n 's/.*for suite in \(.*\); do.*/\1/p' "${HERE}/module_gate.sh")"
check "wired set is the green set" "${gate_list}" "test-modules.sh test-checksum-verify.sh test-install-minisign.sh sync-modules.test.sh public-hygiene.sh"
for red in test-e2e.sh sync-modules.sh; do
    check_lacks "not wired: ${red}" " ${gate_list} " " ${red} "
done

rel="${HERE}/release.sh"
if [ -f "${rel}" ]; then
    check_contains "release.sh sources the gate" "$(cat "${rel}")" 'source "${REPO_ROOT}/tools/module_gate.sh"'
    check "release.sh calls module_gate exactly once" "$(grep -c '^ *module_gate$' "${rel}")" "1"
    gate_line="$(grep -n '^ *module_gate$' "${rel}" | cut -d: -f1)"
    dry_line="$(grep -n 'if \[ "\${DRY_RUN}" = 1 \]; then' "${rel}" | head -1 | cut -d: -f1)"
    if [ -n "${gate_line}" ] && [ -n "${dry_line}" ]; then
        if [ "${gate_line}" -lt "${dry_line}" ]; then
            echo "ok: module_gate runs before the --dry-run early return"
        else
            echo "FAIL: module_gate must run before the --dry-run early return"; fail=1
        fi
    else
        echo "FAIL: could not locate module_gate / DRY_RUN branch in release.sh"; fail=1
    fi
else
    echo "FAIL: release.sh not found beside module_gate.sh"; fail=1
fi

echo
[ "${fail}" = 0 ] && { echo "ALL OK"; exit 0; }
echo "FAILURES"; exit 1

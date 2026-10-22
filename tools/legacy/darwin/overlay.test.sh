#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GOROOT="$(go env GOROOT)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }

want_go="$(tr -d '[:space:]' < "${HERE}/GO_VERSION")"
have_go="$(go version | awk '{print $3}')"
check "pinned go minor" "${have_go}" "${want_go}"

anchor='SecTrustCopyCertificateChain|SecTrustGetCertificateCount|SecTrustGetCertificateAtIndex|chainRef|CFArrayGetCount|CFArrayGetValueAtIndex'

stray_hunks() {
    diff "$1" "$2" | awk -v anchor="${anchor}" '
        function flush() { if (hunk != "" && hunk !~ anchor) printf "%s", hunk }
        /^[0-9]+(,[0-9]+)?[acd][0-9]+(,[0-9]+)?$/ { flush(); hunk = ""; next }
        { hunk = hunk $0 "\n" }
        END { flush() }
    '
}

for f in root_darwin.go:crypto/x509/root_darwin.go security.go:crypto/x509/internal/macos/security.go security.s:crypto/x509/internal/macos/security.s; do
    ours="${HERE}/_src/${f%%:*}"; theirs="${GOROOT}/src/${f#*:}"
    if [ ! -f "${theirs}" ]; then
        echo "FAIL: GOROOT original missing, cannot diff: ${theirs}" >&2
        fail=1
        continue
    fi
    stray="$(stray_hunks "${theirs}" "${ours}")"
    check "only owned hunks differ: ${f%%:*}" "${stray}" ""
    check "overlay drops the 12+ import: ${f%%:*}" "$(grep -c 'SecTrustCopyCertificateChain' "${ours}")" "0"
done
check "root_darwin.go walks by index" "$(grep -c 'SecTrustGetCertificateAtIndex' "${HERE}/_src/root_darwin.go")" "1"
check "root_darwin.go keeps the distrust check" "$(grep -c 'containsDistrustedSPKIHash(chain\[0\])' "${HERE}/_src/root_darwin.go")" "1"
check "security.s has both trampolines" "$(grep -cE 'SecTrustGetCertificate(Count|AtIndex)_trampoline\(SB\)' "${HERE}/_src/security.s")" "2"
exit "${fail}"

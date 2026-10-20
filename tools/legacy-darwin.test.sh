#!/usr/bin/env bash
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
L="${REPO_ROOT}/tools/legacy/darwin"

fail=0
check() {
    if [ "$2" = "$3" ]; then echo "  ok: $1"; else echo "  FAIL: $1 — got '$2' want '$3'"; fail=1; fi
}
sha_of() {
    if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
    else sha256sum "$1" | awk '{print $1}'; fi
}

W="$(mktemp -d "${TMPDIR:-/tmp}/legacy-darwin-test-XXXXXX")"
trap 'rm -rf "${W}"' EXIT INT TERM

echo "=== provenance: the upstream files are the bytes UPSTREAM records ==="
listed=0
while read -r name want; do
    case "${name}" in ''|\#*) continue ;; esac
    listed=$((listed + 1))
    if [ -f "${L}/${name}" ]; then got="$(sha_of "${L}/${name}")"; else got="<missing>"; fi
    check "UPSTREAM sha256 matches: ${name}" "${got}" "${want}"
done < "${L}/UPSTREAM"
check "UPSTREAM lists all seven upstream files" "${listed}" "7"
for f in "${L}"/_src/*; do
    rel="_src/$(basename "${f}")"
    n="$(awk -v n="${rel}" '$1 == n { c++ } END { print c + 0 }' "${L}/UPSTREAM")"
    check "recorded in UPSTREAM: ${rel}" "${n}" "1"
done

PIN="$(tr -d '[:space:]' < "${L}/GO_VERSION")"
GO="${GO_BIN:-go}"
command -v "${GO}" >/dev/null 2>&1 || { echo "  FAIL: no go toolchain (set GO_BIN)"; exit 1; }
export GOTOOLCHAIN="${PIN}" GOWORK=off GOFLAGS=
[ "${RELEASE_TESTS_OFFLINE:-}" = 1 ] && export GOPROXY=off
mkdir -p "${W}/bin"
ln -s "$(command -v "${GO}")" "${W}/bin/go"
export PATH="${W}/bin:${PATH}"
have="$(go version 2>/dev/null | awk '{print $3}')"
if [ "${have}" != "${PIN}" ]; then
    if [ "${RELEASE_TESTS_OFFLINE:-}" = 1 ]; then
        echo "  SKIP: ${PIN} is not in the module cache and RELEASE_TESTS_OFFLINE=1 forbids fetching it — drift guard and symbol builds not run"
        exit "${fail}"
    fi
    check "the pinned toolchain ${PIN} is obtainable" "${have:-<none>}" "${PIN}"
    exit 1
fi
goroot="$(go env GOROOT)"
case "${goroot}/" in
    "$(go env GOMODCACHE)"/*)
        relocated="${XDG_CACHE_HOME:-${HOME}/.cache}/umbree-release/legacy-goroot/${PIN}"
        if [ ! -x "${relocated}/bin/go" ]; then
            mkdir -p "$(dirname "${relocated}")"
            [ -e "${relocated}.tmp" ] && { chmod -R u+w "${relocated}.tmp"; rm -rf "${relocated}.tmp"; }
            cp -al "${goroot}" "${relocated}.tmp" 2>/dev/null || cp -R "${goroot}" "${relocated}.tmp"
            mv "${relocated}.tmp" "${relocated}"
        fi
        ln -sf "${relocated}/bin/go" "${W}/bin/go"
        export GOTOOLCHAIN=local
        check "the relocated toolchain is still ${PIN}" "$(go version 2>/dev/null | awk '{print $3}')" "${PIN}"
        ;;
esac
echo "  (toolchain: $(go version), GOROOT $(go env GOROOT))"

echo "=== drift guard: passes on the pin, fails on both of its reasons ==="
bash "${L}/overlay.test.sh" > "${W}/guard.log" 2>&1
check "overlay.test.sh passes on ${PIN}" "$?" "0"
sed 's/^/      | /' "${W}/guard.log"

cp -R "${L}" "${W}/wrong-pin"
echo "go1.0.0" > "${W}/wrong-pin/GO_VERSION"
bash "${W}/wrong-pin/overlay.test.sh" > "${W}/wrong-pin.log" 2>&1
check "a GO_VERSION other than the toolchain fails the guard" "$?" "1"
check "…and says it is the pin" "$(grep -c '^FAIL: pinned go minor' "${W}/wrong-pin.log")" "1"

cp -R "${L}" "${W}/stray-hunk"
sed '1a\
// an edit no owned symbol explains
' "${L}/_src/security.go" > "${W}/stray-hunk/_src/security.go"
bash "${W}/stray-hunk/overlay.test.sh" > "${W}/stray-hunk.log" 2>&1
check "an unowned hunk fails the guard" "$?" "1"
check "…and names the file" "$(grep -c '^FAIL: only owned hunks differ: security.go' "${W}/stray-hunk.log")" "1"

echo "=== symbol rule: stock refused, overlay accepted, no-TLS accepted ==="
source "${L}/symbols.sh"
mkdir -p "${W}/tls" "${W}/plain"
cat > "${W}/tls/main.go" <<'EOF'
package main

import (
	"crypto/x509"
	"fmt"
)

func main() {
	c := &x509.Certificate{Raw: []byte{0x30, 0x00}}
	_, err := c.Verify(x509.VerifyOptions{})
	fmt.Println(err)
}
EOF
cat > "${W}/plain/main.go" <<'EOF'
package main

import "fmt"

func main() { fmt.Println("hi") }
EOF
printf 'module probe\n\ngo 1.25\n' > "${W}/tls/go.mod"
printf 'module probe\n\ngo 1.25\n' > "${W}/plain/go.mod"
bash "${L}/overlay-json.sh" "${W}/overlay.json"
build() { ( cd "$1" && CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags '-s -w' "${@:3}" -o "$2" . ); }
build "${W}/tls"   "${W}/tls-stock"                                  || { echo "  FAIL: stock TLS build"; fail=1; }
build "${W}/plain" "${W}/plain-bin"                                  || { echo "  FAIL: no-TLS build"; fail=1; }
build "${W}/tls"   "${W}/tls-legacy" -overlay "${W}/overlay.json"    || { echo "  FAIL: overlay TLS build"; fail=1; }
assert_legacy_symbols "${W}/tls-stock"  >/dev/null 2>&1; check "stock TLS binary refused (the overlay is scoped to its build)" "$?" "1"
assert_legacy_symbols "${W}/plain-bin"  >/dev/null 2>&1; check "no-TLS binary accepted" "$?" "0"
assert_legacy_symbols "${W}/tls-legacy" >/dev/null 2>&1; check "overlay TLS binary accepted" "$?" "0"
check "overlay TLS binary imports the 10.7 index walk" \
    "$(go tool nm "${W}/tls-legacy" 2>/dev/null | awk '$1 == "U" && $2 == "_SecTrustGetCertificateAtIndex"' | wc -l | tr -d ' ')" "1"
echo "junk" > "${W}/not-a-binary"
assert_legacy_symbols "${W}/not-a-binary" >/dev/null 2>&1; check "an unreadable file is refused, never passed" "$?" "1"

exit "${fail}"

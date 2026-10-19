# shellcheck shell=bash
legacy_fake_goroot() {
    local root="$1" src
    src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_src"
    mkdir -p "${root}/src/crypto/x509/internal/macos"
    cp "${src}/root_darwin.go" "${root}/src/crypto/x509/root_darwin.go"
    cp "${src}/security.go" "${src}/security.s" "${root}/src/crypto/x509/internal/macos/"
}

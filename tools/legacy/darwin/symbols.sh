undefined_symbols() {
    local listing
    listing="$(go tool nm "$1" 2>/dev/null)" || return 1
    printf '%s\n' "$listing" | awk '$1 == "U" { print $2 }' | LC_ALL=C sort
}

assert_legacy_symbols() {
    local f="$1" syms
    syms="$(undefined_symbols "$f")" || { echo "✗ go tool nm failed on $f" >&2; return 1; }
    if grep -q '^_SecTrustCopyCertificateChain$' <<<"$syms"; then
        echo "✗ $f still imports _SecTrustCopyCertificateChain (macOS 12+) — the legacy overlay did not apply" >&2
        return 1
    fi
    if grep -q '^_SecTrust' <<<"$syms" \
       && ! grep -q '^_SecTrustGetCertificateAtIndex$' <<<"$syms"; then
        echo "✗ $f verifies TLS but lacks _SecTrustGetCertificateAtIndex — unexpected x509 shape" >&2
        return 1
    fi
    return 0
}

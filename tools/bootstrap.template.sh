#!/bin/sh

set -eu

COMP="@COMP@"
CHANNEL="@CHANNEL@"
case "$CHANNEL" in
    beta) SELF="beta.install.sh" ;;
    *)    SELF="install.sh" ;;
esac
case "$CHANNEL" in
    beta)
        TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
        STABLE_TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$" ;;
    *)
        TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
        STABLE_TAG_RE="" ;;
esac
PUBKEY="@PUBKEY@"
MIN_VERSION="@MIN_VERSION@"
REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"
PREFIX="${PREFIX:-$HOME/.local}"
DL_BASE="${UMBREE_DL_BASE:-}"
GH_PROXIES="${UMBREE_GH_PROXY-https://gh-proxy.org https://cdn.gh-proxy.org https://v6.gh-proxy.org https://gh-proxy.com}"
DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-@DOWNLOADS_BASE@}"

if [ -n "$DL_BASE" ]; then
    CURL="curl -fsSL --connect-timeout 15 --max-time 300 --speed-limit 4096 --speed-time 20"
else
    CURL="curl -fsSL --proto =https --tlsv1.2 --connect-timeout 15 --max-time 300 --speed-limit 4096 --speed-time 20"
fi

@INCLUDE:helpers@

# BEGIN version-floor
semver_of() {
    printf '%s' "${1#v}" | cut -d. -f1-3
}

is_semver() {
    case "$1" in
        [0-9]*.[0-9]*.[0-9]*) ;;
        *) return 1 ;;
    esac
    case "$1" in
        *[!0-9.]*) return 1 ;;
    esac
    return 0
}

version_ge() {
    _vg_a="$(semver_of "$1")"
    _vg_b="$(semver_of "$2")"
    is_semver "$_vg_a" || return 1
    is_semver "$_vg_b" || return 1
    if [ "$_vg_a" = "$_vg_b" ]; then return 0; fi
    [ "$(printf '%s\n%s\n' "$_vg_a" "$_vg_b" | sort -V | head -n1)" = "$_vg_b" ]
}

assert_version_floor() {
    case "$MIN_VERSION" in
        ""|*@*|*PLACEHOLDER*|*TEMP*)
            fail "no version floor baked into this installer — refusing to accept a network-resolved version with nothing to check it against (regenerate with tools/gen-bootstraps.sh, or pin the version yourself via UMBREE_VERSION)" ;;
    esac
    version_ge "${1#*/}" "$MIN_VERSION" \
        || fail "version floor not met — resolved \"$1\", but this $SELF was published at \"$MIN_VERSION\" and will not go backwards.
    Refusing to install: this is what a mirror serving a stale, older (but
    genuinely signed) release looks like. Retry when the release channel is
    reachable, or pin the version you actually want via UMBREE_VERSION and
    install again."
    ok "version floor satisfied ($MIN_VERSION)"
}
# END version-floor

@INCLUDE:channel-pick@

latest_tag() {
    _lt_re="${1:-$TAG_RE}"
    if command -v jq >/dev/null 2>&1; then
        jq -r '.[].tag_name // empty' 2>/dev/null
    else
        grep -E '^[[:space:]]*"tag_name"[[:space:]]*:' \
            | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
    fi | grep -E "$_lt_re" | sort -V | tail -n1
}

latest_stamp() {
    if command -v jq >/dev/null 2>&1; then
        jq -r '.stamp // empty' 2>/dev/null
    else
        grep -E '^[[:space:]]*"stamp"[[:space:]]*:' \
            | sed -E 's/.*"stamp"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/' \
            | head -n1
    fi
}

@INCLUDE:platform-detect@

@INCLUDE:pubkey-guard@

@INCLUDE:tmp-workspace@

PIN="${UMBREE_VERSION:-}"
if [ -n "$PIN" ]; then
    TAG="$PIN"
    if [ "$CHANNEL" = beta ]; then
        printf '%s\n' "$TAG" | grep -Eq "$TAG_RE" || printf '%s\n' "$TAG" | grep -Eq "$STABLE_TAG_RE" \
            || fail "UMBREE_VERSION=$TAG is not a ${COMP} release tag (want ${COMP}/v<X.Y.Z>[.beta].<date>.<sha8>)"
    fi
    info "using pinned version: $TAG"
else
    info "resolving latest ${COMP} release ($CHANNEL)"
    api="https://api.github.com/repos/${REPO}/releases?per_page=100"
    if [ "$CHANNEL" = beta ]; then gh_re="$STABLE_TAG_RE"; else gh_re="$TAG_RE"; fi
    # shellcheck disable=SC2086
    body="$($CURL "$api" 2>/dev/null)" || true
    TAG="$(printf '%s' "$body" | latest_tag "$gh_re")" || true
    if [ -z "$TAG" ] && [ -z "$DL_BASE" ] && [ -n "$DOWNLOADS_BASE" ]; then
        info "GitHub API unreachable — trying $DOWNLOADS_BASE/$COMP/latest.json"
        # shellcheck disable=SC2086
        lj="$($CURL "$DOWNLOADS_BASE/$COMP/latest.json" 2>/dev/null)" || true
        st="$(printf '%s' "$lj" | latest_stamp)" || true
        case "$st" in
            v*) if printf '%s\n' "$COMP/$st" | grep -Eq "$gh_re"; then TAG="$COMP/$st"; info "downloads mirror: $TAG"; fi ;;
        esac
    fi
    if [ -z "$TAG" ] && [ -z "$DL_BASE" ] && [ -n "$GH_PROXIES" ]; then
        for _proxy in $GH_PROXIES; do
            info "GitHub API + downloads mirror unreachable — retrying via mirror $_proxy"
            # shellcheck disable=SC2086
            body="$($CURL "$_proxy/$api" 2>/dev/null)" || true
            TAG="$(printf '%s' "$body" | latest_tag "$gh_re")" || true
            if [ -n "$TAG" ]; then info "mirror resolved: $TAG"; break; fi
        done
    fi
    if [ "$CHANNEL" = beta ] && [ -z "$DL_BASE" ]; then
        [ -n "$DOWNLOADS_BASE" ] || fail "this is $SELF, the beta installer, and no downloads mirror is baked into it — regenerate with tools/gen-bootstraps.sh"
        STABLE_TAG="$TAG"
        info "resolving latest ${COMP} beta from $DOWNLOADS_BASE/$COMP/beta/latest.json"
        # shellcheck disable=SC2086
        lj="$($CURL "$DOWNLOADS_BASE/$COMP/beta/latest.json" 2>/dev/null)" || true
        st="$(printf '%s' "$lj" | latest_stamp)" || true
        BETA_TAG=""; case "$st" in v*) BETA_TAG="$COMP/$st" ;; esac
        printf '%s\n' "$BETA_TAG"   | grep -Eq "$TAG_RE"        || BETA_TAG=""
        printf '%s\n' "$STABLE_TAG" | grep -Eq "$STABLE_TAG_RE" || STABLE_TAG=""
        TAG="$(beta_channel_pick "$BETA_TAG" "$STABLE_TAG")"
        [ -n "$TAG" ] || fail "no beta release of ${COMP} on the downloads mirror and no stable release on ${REPO} — nothing to install"
        case "$TAG" in
            *.beta.*) info "beta: $TAG" ;;
            *)        info "beta cycle has graduated — installing the stable release $TAG" ;;
        esac
    fi
    [ -n "$TAG" ] || fail "no published release found for ${COMP} on ${REPO} (GitHub, ${DOWNLOADS_BASE:-the downloads mirror}, and the gh-proxy mirrors [$GH_PROXIES] were all unreachable)"
    info "latest: $TAG"
    assert_version_floor "$TAG"
fi

STAMP="${TAG#"$COMP/"}"
case "$STAMP" in
    *.beta.*) DOWNLOADS_FILE_BASE="$DOWNLOADS_BASE/$COMP/beta/$STAMP"; TAG_IS_BETA=1 ;;
    *)        DOWNLOADS_FILE_BASE="$DOWNLOADS_BASE/$COMP/$STAMP";      TAG_IS_BETA=0 ;;
esac
if [ -n "$DL_BASE" ]; then
    BASE="$DL_BASE"
elif [ "$TAG_IS_BETA" = 1 ]; then
    [ -n "$DOWNLOADS_BASE" ] || fail "beta release $TAG can only be downloaded from the downloads mirror, and it is disabled (UMBREE_DOWNLOADS_BASE is empty)"
    BASE="$DOWNLOADS_FILE_BASE"
else
    BASE="https://github.com/${REPO}/releases/download/${TAG}"
fi
ZIP="${COMP}-${OS}-${ARCH}.zip"
MIRROR_BASE="https://github.com/${REPO}/releases/download/$(printf '%s' "${TAG}" | sed 's#/#%2F#g')"

dl() {
    info "GET $BASE/$1"
    # shellcheck disable=SC2086
    if $CURL -o "$TMP/$2" "$BASE/$1" 2>/dev/null; then
        return 0
    fi
    if [ -z "$DL_BASE" ] && [ "$TAG_IS_BETA" = 0 ] && [ -n "$GH_PROXIES" ]; then
        for _proxy in $GH_PROXIES; do
            info "primary failed; trying mirror: $_proxy/$MIRROR_BASE/$1"
            # shellcheck disable=SC2086
            if $CURL -o "$TMP/$2" "$_proxy/$MIRROR_BASE/$1" 2>/dev/null; then
                ok "downloaded $1 via mirror $_proxy"
                return 0
            fi
        done
    fi
    if [ -z "$DL_BASE" ] && [ "$TAG_IS_BETA" = 0 ] && [ -n "$DOWNLOADS_BASE" ]; then
        info "mirrors failed; trying downloads mirror: $DOWNLOADS_FILE_BASE/$1"
        # shellcheck disable=SC2086
        if $CURL -o "$TMP/$2" "$DOWNLOADS_FILE_BASE/$1" 2>/dev/null; then
            ok "downloaded $1 via downloads mirror"
            return 0
        fi
    fi
    fail "download failed: $1 (from $BASE; mirrors: $GH_PROXIES; downloads: ${DOWNLOADS_BASE:-disabled}) — refusing to install unverified bytes"
}
info "downloading $ZIP"
dl "$ZIP" "$ZIP"
info "downloading SHA256SUMS.txt + signature"
dl "SHA256SUMS.txt"         "SHA256SUMS.txt"
dl "SHA256SUMS.txt.minisig" "SHA256SUMS.txt.minisig"

@INCLUDE:sha256@

@INCLUDE:install-minisign-common@
@INCLUDE:install-minisign-linux@
@INCLUDE:install-minisign-darwin@

@INCLUDE:require-minisign@

@INCLUDE:verify-signature@

info "verifying checksum"
@INCLUDE:verify-checksum@
ok "checksum verified"

command -v unzip >/dev/null 2>&1 \
    || fail "unzip not found — install it (\`brew install unzip\` / \`apt-get install unzip\`) and retry"
unzip -q -o "$TMP/$ZIP" -d "$TMP/x" || fail "zip extraction failed — corrupt download?"
[ -f "$TMP/x/install.sh" ] || fail "release zip missing inner install.sh — aborting"

ok "verified — running inner installer"
case "$COMP" in
    umbree)
        ( cd "$TMP/x" && PREFIX="$PREFIX" UMBREE_UNINSTALL="${UMBREE_UNINSTALL:-}" sh ./install.sh )
        ;;
    umbreed)
        ( cd "$TMP/x" && PREFIX="$PREFIX" UMBREED_UNINSTALL="${UMBREED_UNINSTALL:-}" UMBREED_NO_SERVICE="${UMBREED_NO_SERVICE:-}" sh ./install.sh )
        ;;
    *)
        fail "unknown component '$COMP' — no inner-exec contract"
        ;;
esac

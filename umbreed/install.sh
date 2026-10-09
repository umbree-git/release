#!/bin/sh

set -eu

COMP="umbreed"
CHANNEL="stable"
STABLE_TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
BETA_TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
case "$CHANNEL" in
    beta) SELF="beta.install.sh"; TAG_RE="$BETA_TAG_RE" ;;
    *)    SELF="install.sh";      TAG_RE="$STABLE_TAG_RE" ;;
esac
PUBKEY="RWQZyK0l3lgdSYfj8VXhoTWlVVVcRqfnuVROJzloNrw9NBFm11IeD3HN"
MIN_VERSION="v0.1.1.2026.08.31.fde2705e"
PREFIX="${PREFIX:-$HOME/.local}"
DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-https://downloads.umbree.org}"
CURL="curl -fsSL --proto =https --proto-redir =https --tlsv1.2 --connect-timeout 15 --max-time 300 --speed-limit 4096 --speed-time 20"
DL_BASE=""
GH_PROXIES=""
ALLOW_LOOPBACK_HTTP=0


# BEGIN helpers
fail() { printf '\n  ✗ %s\n\n' "$*" >&2; exit 1; }
info() { printf '  → %s\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }
# END helpers

case "$DOWNLOADS_BASE" in
    "")
        fail "no download source: UMBREE_DOWNLOADS_BASE is empty, and the release manifest on the downloads base is the only place ${COMP} installs from" ;;
    https://*) ;;
    http://127.0.0.1:*)
        [ "$ALLOW_LOOPBACK_HTTP" = 1 ] || fail "the downloads base must be https:// (got $DOWNLOADS_BASE)" ;;
    *)
        fail "the downloads base must be https:// (got $DOWNLOADS_BASE)" ;;
esac

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

# BEGIN channel-pick
# Copied VERBATIM below this header from burrowee-git/release, branch beta-channel-graduation
# (UNMERGED into burrowee main as of 2026-09-05), tools/modules/channel-pick.sh. Once burrowee
# merges it, drop these three lines so tools/sync-modules.sh compares shas as for any module.
# The beta channel installs the NEWEST of its own channel and stable, so a host
# that soaked a cycle graduates onto the release without ever changing channel —
# the channel flip is what caused a beta fleet to silently migrate to stable
# once already. The two channels keep SEPARATE sorts: `sort -V` does not
# implement semver pre-release ordering (it puts 0.3.0.beta.… ABOVE 0.3.0), so a
# mixed list would pin a beta host to a pre-release forever. Each shape is
# sorted alone, where sort -V is correct, and only the two winners are compared.

# latest_tag_matching <regex> <file> — highest tag in <file> whose shape matches
# <regex>. One shape per call: every tag in the result has the same number of
# dot-separated fields, which is the condition under which `sort -V` is right.
latest_tag_matching() {
    grep -E "$1" < "$2" | sort -V | tail -n1
}

# beta_channel_pick <beta_tag> <stable_tag> — the tag a BETA host installs.
# Compares X.Y.Z only: semver_of truncates at three fields, so it drops a
# .beta.<date>.<sha8> suffix and a beta tag compares equal to the stable tag
# it was cut from. A TIE therefore means "this stable release is the one this
# beta soaked", and it goes to stable — that single case is what graduates the
# fleet at cycle close.
#
# A malformed side must never win, and which side is malformed decides the
# outcome on its own — version_ge alone can't carry that: it fails CLOSED
# whenever EITHER side isn't a well-formed semver, so version_ge(stable, beta)
# returns false both when stable is malformed (beta should win) and when beta
# is malformed (stable should win), and "false" only ever routes to one
# branch. So malformed-ness is checked per side, explicitly, before falling
# back to version_ge for the case both sides are well-formed.
beta_channel_pick() {
    _bcp_beta="$1"
    _bcp_stable="$2"
    if [ -z "$_bcp_stable" ]; then printf '%s' "$_bcp_beta"; return 0; fi
    if [ -z "$_bcp_beta" ]; then printf '%s' "$_bcp_stable"; return 0; fi
    # Strip the "<comp>/" prefix before comparing — semver_of only strips a
    # leading "v", exactly as assert_version_floor does at its call site.
    _bcp_stable_v="${_bcp_stable#*/}"
    _bcp_beta_v="${_bcp_beta#*/}"
    if ! is_semver "$(semver_of "$_bcp_stable_v")"; then
        printf '%s' "$_bcp_beta"; return 0
    fi
    if ! is_semver "$(semver_of "$_bcp_beta_v")"; then
        printf '%s' "$_bcp_stable"; return 0
    fi
    if version_ge "$_bcp_stable_v" "$_bcp_beta_v"; then
        printf '%s' "$_bcp_stable"
    else
        printf '%s' "$_bcp_beta"
    fi
}
# END channel-pick

is_tag() {
    case "$1" in
        *"
"*) return 1 ;;
    esac
    printf '%s\n' "$1" | grep -Eq "$2"
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

# BEGIN platform-detect
case "$(uname -s)" in
    Darwin) OS=darwin ;;
    Linux)  OS=linux ;;
    *)      fail "unsupported OS: $(uname -s) (umbree ships darwin + linux only)" ;;
esac
case "$(uname -m)" in
    arm64|aarch64) ARCH=arm64 ;;
    x86_64|amd64)  ARCH=amd64 ;;
    *)             fail "unsupported arch: $(uname -m) (umbree ships arm64 + amd64 only)" ;;
esac

printf '\n  umbree %s installer  (%s/%s)\n\n' "$COMP" "$OS" "$ARCH"
# END platform-detect

# BEGIN pubkey-guard
case "$PUBKEY" in
    ""|*REPLACE*|*PLACEHOLDER*|*TEMP*)
        fail "this installer was built without a real signing key — refusing to verify against a placeholder (regenerate with tools/gen-bootstraps.sh)" ;;
esac
# END pubkey-guard

# BEGIN tmp-workspace
TMP="$(mktemp -d "${TMPDIR:-/tmp}/umbree-${COMP}-XXXXXX")" || fail "could not create temp dir"
trap 'rm -rf "$TMP"' EXIT INT TERM
# END tmp-workspace

manifest_tag() {
    _mt_url="$DOWNLOADS_BASE/$COMP/$1"
    # shellcheck disable=SC2086
    if ! _mt_body="$($CURL "$_mt_url" 2>/dev/null)"; then
        printf 'could not fetch the release manifest %s, and it is the only source; nothing to install' "$_mt_url"
        return 1
    fi
    _mt_stamp="$(printf '%s' "$_mt_body" | latest_stamp)" || _mt_stamp=""
    if is_tag "$COMP/$_mt_stamp" "$2"; then
        printf '%s' "$COMP/$_mt_stamp"
        return 0
    fi
    printf 'the release manifest %s names no %s release of the %s shape; refusing it' "$_mt_url" "$COMP" "$3"
    return 1
}

resolve_beta() {
    _rb_stable="$(manifest_tag latest.json "$STABLE_TAG_RE" stable)" || _rb_stable=""
    _rb_beta="$(manifest_tag beta/latest.json "$BETA_TAG_RE" beta)" || _rb_beta=""
    _rb_tag="$(beta_channel_pick "$_rb_beta" "$_rb_stable")"
    [ -n "$_rb_tag" ] || return 1
    printf '%s' "$_rb_tag"
}

PIN="${UMBREE_VERSION:-}"
if [ -n "$PIN" ]; then
    is_tag "$PIN" "$STABLE_TAG_RE" || is_tag "$PIN" "$BETA_TAG_RE" \
        || fail "UMBREE_VERSION=$PIN is not a ${COMP} release tag (want ${COMP}/v<X.Y.Z>[.beta].<date>.<sha8>)"
    TAG="$PIN"
    info "using pinned version: $TAG"
elif [ "$CHANNEL" = beta ]; then
    info "resolving latest ${COMP} release (beta) from $DOWNLOADS_BASE/$COMP/beta/latest.json and $DOWNLOADS_BASE/$COMP/latest.json"
    TAG="$(resolve_beta)" \
        || fail "neither $DOWNLOADS_BASE/$COMP/beta/latest.json nor $DOWNLOADS_BASE/$COMP/latest.json names a release of ${COMP}; nothing to install"
    case "$TAG" in
        *.beta.*) info "beta: $TAG" ;;
        *)        info "beta cycle has graduated; installing the stable release $TAG" ;;
    esac
    assert_version_floor "$TAG"
else
    info "resolving latest ${COMP} release from $DOWNLOADS_BASE/$COMP/latest.json"
    TAG="$(manifest_tag latest.json "$TAG_RE" stable)" || fail "$TAG"
    info "latest: $TAG"
    assert_version_floor "$TAG"
fi

STAMP="${TAG#"$COMP/"}"
case "$STAMP" in
    *.beta.*) BASE="$DOWNLOADS_BASE/$COMP/beta/$STAMP" ;;
    *)        BASE="$DOWNLOADS_BASE/$COMP/$STAMP" ;;
esac
[ -z "$DL_BASE" ] || BASE="$DL_BASE"
ZIP="${COMP}-${OS}-${ARCH}.zip"

dl() {
    info "GET $BASE/$1"
    # shellcheck disable=SC2086
    $CURL -o "$TMP/$2" "$BASE/$1" 2>/dev/null \
        || fail "download failed: $BASE/$1; refusing to install unverified bytes"
}
info "downloading $ZIP"
dl "$ZIP" "$ZIP"
info "downloading SHA256SUMS.txt + signature"
dl "SHA256SUMS.txt"         "SHA256SUMS.txt"
dl "SHA256SUMS.txt.minisig" "SHA256SUMS.txt.minisig"

# BEGIN sha256
# sha256 of a file, as a bare hex digest. shasum on macOS, sha256sum on stock
# Debian/Ubuntu (which ships no perl and therefore no shasum). Both spellings
# are pre-2016-safe: no --ignore-missing, no --check.
sha256_of() {
    if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else return 1; fi
}
# END sha256

# BEGIN install-minisign-common
# Provides minisign when the host has none. The per-platform modules that follow
# try the OS package manager first, then the official jedisct1/minisign release
# archive whose SHA-256 is PINNED here. This bootstrap is the install's trust
# root already — it is served from the release host over HTTPS and the operator
# runs it — so a hash carried inside it makes the fetched verifier exactly as
# trusted as the script that carries the hash. The mirror or CDN that served
# the bytes never enters that calculation: only bytes matching the pin survive
# minisign_fetch. A second seal, minisign_seal, then checks the archive's own
# .minisig against upstream's release key using the binary just installed.
#
# BUMPING THE PIN is a deliberate, reviewed change — never "latest":
#   1. download minisign-<v>-linux.tar.gz, minisign-<v>-macos.zip and both
#      .minisig files from https://github.com/jedisct1/minisign/releases
#   2. minisign -Vm <archive> -P "$MINISIGN_UPSTREAM_PUBKEY"     (each archive)
#   3. shasum -a 256 <archive>                                    (each archive)
#   4. update MINISIGN_VERSION and both sha256 constants, bump this module's vN,
#      sh tools/lock-modules.sh && sh tools/gen-bootstraps.sh &&
#      sh tools/test-modules.sh && sh tools/test-install-minisign.sh
#      (the suite reads MINISIGN_VERSION and the pins from the generated block —
#      nothing in it to edit)
#   5. sync-modules.sh from the other products (they carry this module too)
MINISIGN=""
MINISIGN_VERSION="0.12"
MINISIGN_LINUX_SHA256="9a599b48ba6eb7b1e80f12f36b94ceca7c00b7a5173c95c3efc88d9822957e73"
MINISIGN_MACOS_SHA256="89000b19535765f9cffc65a65d64a820f433ef6db8020667f7570e06bf6aac63"
MINISIGN_UPSTREAM_PUBKEY="RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3"
MINISIGN_UPSTREAM_BASE="https://github.com/jedisct1/minisign/releases/download/$MINISIGN_VERSION"
# Homebrew locations a daemon-hosted shell's bare PATH omits — the same two the
# product's `update` verb probes.
MINISIGN_KNOWN_PATHS="/opt/homebrew/bin/minisign /usr/local/bin/minisign"

# minisign_known — print the first executable minisign at a location PATH may
# not cover: the install destination itself (an earlier run, or the operator's
# own copy in $PREFIX/bin), then the Homebrew locations. Nothing here is ever
# overwritten; require-minisign uses whatever this finds.
minisign_known() {
    for _mk_p in "${PREFIX:-/usr/local}/bin/minisign" $MINISIGN_KNOWN_PATHS; do
        [ -x "$_mk_p" ] && { printf '%s' "$_mk_p"; return 0; }
    done
    return 1
}

# minisign_dest_dir — where a fetched minisign lands: beside the product, in
# $PREFIX/bin. The inner installer puts that directory on PATH, so the
# product's `update` verb finds it on later runs. PREFIX is resolved by the
# bootstrap before this point; empty means the root-only installers' /usr/local.
minisign_dest_dir() {
    _md="${PREFIX:-/usr/local}/bin"
    mkdir -p "$_md" 2>/dev/null || { info "minisign: cannot create $_md" >&2; return 1; }
    printf '%s' "$_md"
}

# minisign_fetch <name> [sha256] — download <name> into $TMP. With a pin,
# succeed only when the sha256 matches; a mismatch is deleted and the next
# source tried. Without a pin (the .minisig only — the seal proves it) the
# first successful download wins. Sources: $DL_BASE (the test hook), else
# upstream GitHub, then each GH_PROXIES mirror in the <mirror>/<full-url> form
# the download module uses. Every source exhausted -> 1.
minisign_fetch() {
    _mf_name="$1"; _mf_want="${2:-}"; _mf_out="$TMP/$_mf_name"
    if [ -n "${DL_BASE:-}" ]; then
        _mf_srcs="$DL_BASE/$_mf_name"
    else
        _mf_srcs="$MINISIGN_UPSTREAM_BASE/$_mf_name"
        for _mf_p in ${GH_PROXIES:-}; do
            _mf_srcs="$_mf_srcs $_mf_p/$MINISIGN_UPSTREAM_BASE/$_mf_name"
        done
    fi
    for _mf_src in $_mf_srcs; do
        rm -f "$_mf_out"
        # shellcheck disable=SC2086  # $CURL is a command plus its flags
        $CURL -o "$_mf_out" "$_mf_src" 2>/dev/null || continue
        [ -n "$_mf_want" ] || return 0
        _mf_got="$(sha256_of "$_mf_out")" || break
        [ "$_mf_got" = "$_mf_want" ] && return 0
        info "minisign: $_mf_name from $_mf_src does not match the pinned sha256 — discarded"
    done
    rm -f "$_mf_out"
    return 1
}

# minisign_install_file <src> — install <src> as minisign in the destination
# directory and print the absolute path. Never overwrites: a file already
# there belongs to the operator (or an earlier run) and minisign_known will
# have reported it — so minisign_seal's removal below only ever touches a
# file this run created.
minisign_install_file() {
    _mi_dir="$(minisign_dest_dir)" || return 1
    if [ -e "$_mi_dir/minisign" ]; then
        info "minisign: $_mi_dir/minisign already exists — not overwriting it" >&2
        return 1
    fi
    install -m 0755 "$1" "$_mi_dir/minisign" 2>/dev/null \
        || { info "minisign: cannot write $_mi_dir/minisign (not writable — re-run as root, or set PREFIX)" >&2; return 1; }
    printf '%s/minisign' "$_mi_dir"
}

# minisign_seal <archive> <bin> — the second seal: verify the archive's own
# upstream .minisig with the minisign just installed. Failure (including a
# .minisig that cannot be fetched) removes <bin> and returns 1.
minisign_seal() {
    _ms_arc="$1"; _ms_bin="$2"; _ms_name="$(basename "$_ms_arc")"
    if minisign_fetch "$_ms_name.minisig" \
       && "$_ms_bin" -Vm "$_ms_arc" -x "$TMP/$_ms_name.minisig" -P "$MINISIGN_UPSTREAM_PUBKEY" >/dev/null 2>&1; then
        return 0
    fi
    info "minisign: upstream signature on $_ms_name did not verify — removing $_ms_bin"
    rm -f "$_ms_bin"
    return 1
}
# END install-minisign-common
# BEGIN install-minisign-linux
# Linux: the package manager first — but only as root or with passwordless
# sudo, because a user-level install must never prompt for a password inside
# curl|sh — then the pinned static upstream build (x86_64 / aarch64, statically
# linked, so distro and libc do not matter). Every failure here is an info
# line and falls through; require-minisign is the one that decides.
# MINISIGN_SKIP_PM=1 says a preflight already made the package-manager attempt.
if [ "$OS" = linux ] && ! command -v minisign >/dev/null 2>&1 && ! minisign_known >/dev/null; then
    _ml_sudo=""; _ml_can_pm=0
    if [ "$(id -u)" = 0 ]; then
        _ml_can_pm=1
    elif sudo -n true 2>/dev/null; then
        _ml_sudo="sudo"; _ml_can_pm=1
    fi
    if [ -n "${MINISIGN_SKIP_PM:-}" ]; then
        :
    elif [ "$_ml_can_pm" = 1 ]; then
        info "minisign: not found — trying the package manager"
        # shellcheck disable=SC2086  # $_ml_sudo is an optional prefix word
        if command -v apt-get >/dev/null 2>&1; then
            { $_ml_sudo apt-get update && $_ml_sudo apt-get install -y minisign; } >/dev/null 2>&1 || true
        elif command -v dnf >/dev/null 2>&1; then
            $_ml_sudo dnf install -y minisign >/dev/null 2>&1 || true
        elif command -v yum >/dev/null 2>&1; then
            $_ml_sudo yum install -y minisign >/dev/null 2>&1 || true
        elif command -v apk >/dev/null 2>&1; then
            $_ml_sudo apk add minisign >/dev/null 2>&1 || true
        fi
    else
        info "minisign: not found, and no root or passwordless sudo — skipping the package manager"
    fi
    if command -v minisign >/dev/null 2>&1; then
        ok "minisign installed by the package manager"
    else
        if [ "$_ml_can_pm" = 1 ] && [ -z "${MINISIGN_SKIP_PM:-}" ]; then
            info "minisign: the package manager could not install it — trying the pinned upstream build"
        else
            info "minisign: trying the pinned upstream build"
        fi
        case "$ARCH" in
            amd64) _ml_sub=x86_64 ;;
            *)     _ml_sub=aarch64 ;;
        esac
        _ml_asset="minisign-$MINISIGN_VERSION-linux.tar.gz"
        if minisign_fetch "$_ml_asset" "$MINISIGN_LINUX_SHA256" \
           && tar xzf "$TMP/$_ml_asset" -C "$TMP" "minisign-linux/$_ml_sub/minisign" 2>/dev/null \
           && _ml_bin="$(minisign_install_file "$TMP/minisign-linux/$_ml_sub/minisign")" \
           && minisign_seal "$TMP/$_ml_asset" "$_ml_bin"; then
            MINISIGN="$_ml_bin"
            ok "minisign $MINISIGN_VERSION installed to $(dirname "$_ml_bin") (pinned upstream build)"
        else
            info "minisign: could not install the pinned upstream build (network, mirrors, its signature, or the destination is not writable)"
        fi
    fi
fi
# END install-minisign-linux
# BEGIN install-minisign-darwin
# macOS: Homebrew first when it is there (as this user, never via sudo), then
# the pinned upstream build — which upstream ships for arm64 only, so an Intel
# Mac without Homebrew gets a plain statement of the gap and require-minisign's
# brew recipe. A Homebrew minisign that a daemon-hosted shell's bare PATH cannot
# see, or one already at the install destination, is still an install:
# minisign_known counts it as present.
if [ "$OS" = darwin ] && ! command -v minisign >/dev/null 2>&1 && ! minisign_known >/dev/null; then
    if [ -z "${MINISIGN_SKIP_PM:-}" ] && command -v brew >/dev/null 2>&1; then
        info "minisign: not found — trying Homebrew"
        brew install minisign >/dev/null 2>&1 || true
    fi
    if command -v minisign >/dev/null 2>&1 || minisign_known >/dev/null; then
        ok "minisign installed by Homebrew"
    elif [ "$ARCH" = arm64 ]; then
        info "minisign: trying the pinned upstream build"
        _md_asset="minisign-$MINISIGN_VERSION-macos.zip"
        if minisign_fetch "$_md_asset" "$MINISIGN_MACOS_SHA256" \
           && unzip -oq "$TMP/$_md_asset" minisign -d "$TMP/minisign-macos" 2>/dev/null \
           && _md_bin="$(minisign_install_file "$TMP/minisign-macos/minisign")" \
           && minisign_seal "$TMP/$_md_asset" "$_md_bin"; then
            MINISIGN="$_md_bin"
            ok "minisign $MINISIGN_VERSION installed to $(dirname "$_md_bin") (pinned upstream build)"
        else
            info "minisign: could not install the pinned upstream build (network, mirrors, its signature, or the destination is not writable)"
        fi
    else
        info "minisign: upstream ships no Intel build — install Homebrew, then minisign"
    fi
fi
# END install-minisign-darwin

# BEGIN require-minisign
# minisign is the trust root of this install. The install-minisign-* modules
# above try to PROVIDE it: the OS package manager first, then the official
# upstream archive whose SHA-256 is pinned in this bootstrap — the bootstrap is
# the install's trust root already, so a hash it carries makes the fetched
# verifier exactly as trusted as the script itself (see install-minisign-common).
# This module only DECIDES: an executable $MINISIGN set by those modules, else
# PATH, else a copy at the install destination or the Homebrew locations a
# daemon-hosted shell cannot see (minisign_known), else refuse. Verification
# is mandatory and is never skipped.
if [ -n "$MINISIGN" ] && [ -x "$MINISIGN" ]; then
    :
elif command -v minisign >/dev/null 2>&1; then
    MINISIGN=minisign
else
    MINISIGN="$(minisign_known)" || MINISIGN=""
fi
if [ -z "$MINISIGN" ]; then
    case "$OS" in
        darwin) hint="install Homebrew if you don't have it, then minisign:
      /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\"
      brew install minisign" ;;
        *)      hint="the package manager and the pinned upstream download both failed —
      check the network and the UMBREE_GH_PROXY mirrors, or install it by hand:
      https://github.com/jedisct1/minisign/releases/tag/$MINISIGN_VERSION" ;;
    esac
    fail "minisign is required and could not be provided — install it and re-run.
    $hint
    upstream: https://github.com/jedisct1/minisign
    Verification is mandatory; this installer will NOT proceed without a verifier."
fi
# END require-minisign

# BEGIN verify-signature
info "verifying signature"
# 1) signature over the sums file, using the baked pubkey (inline, no key fetch).
# Capture stdout — minisign prints the SIGNED "Trusted comment:" line there, and
# that comment is the only version-bearing field in the whole verified set (the
# zip name and SHA256SUMS.txt are both version-independent). stderr is left
# attached so a verification failure still shows minisign's own diagnostics.
verify_out="$("$MINISIGN" -V -P "$PUBKEY" -m "$TMP/SHA256SUMS.txt" -x "$TMP/SHA256SUMS.txt.minisig")" \
    || fail "signature verification failed — aborting (refusing to install unverified bytes)"
ok "minisign signature valid"
# END verify-signature

info "verifying checksum"
# BEGIN verify-checksum
# v4: declares needs: helpers too — the block below calls fail(), which lives
# in the helpers module, not sha256. Under-declaring it was latent only because
# every current template happens to splice helpers before this module.
# Compare ONE hash directly instead of `-c --ignore-missing` over the whole
# sums file: --ignore-missing is a 2016-era addition (Digest::SHA 5.96 /
# coreutils 8.25) and the stock shasum on an older macOS rejects it outright
# ("Unknown option: ignore-missing"). That non-zero exit came back through the
# `||` as "checksum mismatch", so every install on such a host accused a
# perfectly good zip of tampering. Picking the line by EXACT filename (awk, both
# the "hash  name" and binary "hash *name" spellings) is also stricter than the
# substring grep this replaces.
want="$(awk -v f="$ZIP" '{ n = $2; sub(/^\*/, "", n); if (n == f) { print $1; exit } }' "$TMP/SHA256SUMS.txt")"
[ -n "$want" ] \
    || fail "no checksum entry for $ZIP — release incomplete or tampered; aborting"
got="$(sha256_of "$TMP/$ZIP")" \
    || fail "neither shasum nor sha256sum found — cannot verify; aborting"
[ -n "$got" ] && [ "$want" = "$got" ] \
    || fail "checksum mismatch — aborting (zip tampered or download corrupted)"
# END verify-checksum
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

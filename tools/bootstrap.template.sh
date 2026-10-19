#!/bin/sh

set -eu

COMP="@COMP@"
CHANNEL="@CHANNEL@"
MODE="@MODE@"
STABLE_TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
BETA_TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
case "$CHANNEL" in
    beta) SELF="beta.@MODE@.sh"; TAG_RE="$BETA_TAG_RE" ;;
    *)    SELF="@MODE@.sh";      TAG_RE="$STABLE_TAG_RE" ;;
esac
PUBKEY="@PUBKEY@"
MIN_VERSION="@MIN_VERSION@"
PREFIX="${PREFIX:-$HOME/.local}"
DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-@DOWNLOADS_BASE@}"
CURL="curl -fsSL --proto =https --proto-redir =https --tlsv1.2 --connect-timeout 15 --max-time 300 --speed-limit 4096 --speed-time 20"
DL_BASE=""
GH_PROXIES=""
ALLOW_LOOPBACK_HTTP=0
UMBREED_EXEC="/usr/local/umbree/bin/umbreed"
@TEST_SEAM@

@INCLUDE:helpers@

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

LINE=""
if [ "$MODE" = upgrade ]; then
    [ $# -le 1 ] || fail "$SELF takes at most one argument, the migration line (got: $*)"
    LINE="${1:-}"
    [ -z "$LINE" ] || is_semver "$LINE" || fail "$SELF: the migration line must be X.Y.Z (got: $LINE)"
else
    [ $# -eq 0 ] || fail "$SELF takes no arguments (got: $*)"
fi

@INCLUDE:channel-pick@

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

@INCLUDE:platform-detect@

@INCLUDE:pubkey-guard@

@INCLUDE:tmp-workspace@

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
if [ -n "$LINE" ] && [ ! -f "$TMP/x/migrations/upgrade.sh" ]; then
    fail "the $COMP kit $STAMP ships no migration ladder, so the line $LINE has nothing to run — nothing was installed"
fi
if [ "$MODE" = upgrade ] && [ -e "$TMP/x/migrations/upgrade.sh" ]; then
    { [ -f "$TMP/x/migrations/upgrade.sh" ] && [ -r "$TMP/x/migrations/upgrade.sh" ] && [ -s "$TMP/x/migrations/upgrade.sh" ]; } \
        || fail "the $COMP kit $STAMP ships an empty or unreadable migrations/upgrade.sh — refusing to run it; nothing was installed"
    { [ -f "$TMP/x/migrations/run.sh" ] && [ -r "$TMP/x/migrations/run.sh" ] && [ -s "$TMP/x/migrations/run.sh" ]; } \
        || fail "the $COMP kit $STAMP ships migrations/upgrade.sh but no migrations/run.sh — refusing a ladder with no runner; nothing was installed"
fi

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

restart_umbreed() {
    info "no migration ladder in this umbreed kit — restarting the service"
    if (exec </dev/tty) 2>/dev/null; then
        "$UMBREED_EXEC" restart </dev/tty && _ru=0 || _ru=$?
    else
        "$UMBREED_EXEC" restart </dev/null && _ru=0 || _ru=$?
    fi
    if [ "$_ru" -ne 0 ]; then
        printf '\n  ✗ installed %s, but the service was not restarted — run: sudo /usr/local/umbree/bin/umbreed service restart\n\n' "$STAMP" >&2
        exit 1
    fi
    ok "no migration ladder in this umbreed kit — restarted the service"
}

ladder_failed() {
    printf '\n  ✗ installed %s, but %s\n\n' "$STAMP" "$2" >&2
    exit "$1"
}

run_ladder() {
    info "running the kit's migration ladder"
    set +e
    if [ -n "$LINE" ]; then
        ( cd "$TMP/x" && sh ./migrations/upgrade.sh "$LINE" )
    else
        ( cd "$TMP/x" && sh ./migrations/upgrade.sh )
    fi
    _lr=$?
    set -e
    case "$_lr" in
        0) ok "$COMP $STAMP installed — the migration ladder had nothing to apply" ;;
        2) ok "$COMP $STAMP installed — the migration ladder ran its rungs" ;;
        3) ladder_failed 3 "the migration ladder ran and a receipt was lost — re-runnable: run upgrade.sh again" ;;
        64) ladder_failed 64 "the migration ladder rejected its command line (a defect in this bootstrap, not yours)" ;;
        1) ladder_failed 1 "the migration ladder refused or failed — read its output above" ;;
        *) ladder_failed 1 "the migration ladder exited $_lr, which it does not document — treated as a failure" ;;
    esac
}

run_upgrade_step() {
    if [ -f "$TMP/x/migrations/upgrade.sh" ]; then
        run_ladder
        return
    fi
    case "$COMP" in
        umbreed) restart_umbreed ;;
        *) fail "the $COMP kit $STAMP ships no migration ladder and $COMP has no restart fallback — $STAMP is installed; nothing more to run" ;;
    esac
}

[ "$MODE" = install ] || run_upgrade_step

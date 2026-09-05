#!/bin/sh
# Umbree outer bootstrap — THE TRUST ANCHOR (POSIX sh, macOS + Linux).
#
#   curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/@COMP@/install.sh | sh
#
# This is the stable, curl'd-alone entry point for the `@COMP@` component. It
# NEVER runs an unverified byte: it downloads the release zip + SHA256SUMS.txt +
# its minisig, verifies the minisign signature with a baked-in PUBLIC key,
# verifies the zip's sha256 against the now-trusted sums file, and ONLY THEN
# unzips and execs the verified inner per-release install.sh. Any failure aborts
# before anything is installed.
#
# One template renders TWO files per component: @COMP@/install.sh (the stable
# channel) and, while a beta cycle is open, its twin @COMP@/beta.install.sh
# (the beta channel — same URL base, prefixed name; `umbree update` on a beta
# host re-runs the twin, so its name and URL never move). Which one this is
# is baked in as $CHANNEL below.
#
# DO NOT EDIT generated copies (@COMP@/install.sh, @COMP@/beta.install.sh) by
# hand — they are produced from tools/bootstrap.template.sh by
# tools/gen-bootstraps.sh.
#
# Env vars:
#   UMBREE_VERSION          pin a release tag (e.g. umbree/v0.1.1.…); default: latest
#   PREFIX                  install root (default $HOME/.local; bins at PREFIX/bin)
#   UMBREE_UNINSTALL=1      umbree only — remove the installed bin
#   UMBREED_UNINSTALL=1     umbreed only — unload+remove the service, then the binary
#   UMBREED_NO_SERVICE=1    umbreed only — install the binary only, no service elevation
#   UMBREE_RELEASE_REPO     GitHub repo serving releases (default umbree-git/release)
#   UMBREE_DL_BASE          (test hook) download assets from this base instead of GitHub
#   UMBREE_GH_PROXY         Space-separated list of GitHub HTTP mirrors, tried in order
#                           ONLY when github.com / api.github.com are unreachable
#                           (default: gh-proxy.org cdn.gh-proxy.org v6.gh-proxy.org
#                           gh-proxy.com; set empty to disable). minisign + sha256
#                           verified, so an untrusted mirror cannot tamper undetected.
#                           For VERSION RESOLUTION they are only consulted after the
#                           operator-controlled downloads mirror (see below).
#   UMBREE_DOWNLOADS_BASE   Operator-controlled public mirror base (default: the
#                           value baked at render time — the live downloads mirror;
#                           set empty to disable). Serves <comp>/<stamp>/<file> +
#                           <comp>/latest.json, and on the beta channel
#                           <comp>/beta/<stamp>/<file> + <comp>/beta/latest.json —
#                           the ONLY place beta bytes exist (a beta is never a
#                           GitHub Release). On stable, when GitHub is
#                           unreachable, VERSION RESOLUTION prefers its latest.json
#                           BEFORE the third-party gh-proxy mirrors (anti-rollback:
#                           a stale/hostile mirror could otherwise pin fresh
#                           installs to an older, genuinely-signed release). Byte
#                           DOWNLOADS still use it last-resort; bytes from any
#                           source are minisign + sha256 verified.
#
# umbree's carrier delegates to the burrowee daemon; the inner installer ensures
# burrowee-cli is present (one cross-channel curl|sh step — see inner/umbree).
#
# umbreed note: the umbreed inner installer is the daemon repo's own canonical
# sudo-minimal service installer (install/install.sh.in). It escalates with
# sudo only for the one step that needs root — writing + loading the boot
# unit; the daemon process itself never runs as root. UMBREED_NO_SERVICE=1
# installs the binary only, skipping that step entirely.

set -eu

# ---- knobs --------------------------------------------------------------
COMP="@COMP@"
# "stable" or "beta" — which release channel this bootstrap resolves against.
# Baked at render time, never read from the environment: the channel is a
# property of WHICH URL was published (release.umbree.org/@COMP@/install.sh vs
# its .../beta.install.sh twin), and a runtime override would make one file
# behave as the other — the channel flip that silently migrated a beta fleet
# once already.
CHANNEL="@CHANNEL@"
# SELF — this bootstrap's own filename as the operator curl'd it: "install.sh"
# on stable, "beta.install.sh" on its beta twin. Used wherever this script
# names itself back to the operator, so a beta.install.sh does not point
# someone at plain install.sh.
case "$CHANNEL" in
    beta) SELF="beta.install.sh" ;;
    *)    SELF="install.sh" ;;
esac
# TAG_RE — the one tag shape this channel may ever accept, anchored on $COMP
# too so a component mismatch cannot slip through. EVERY tag consumer is held
# to this SAME regex: GitHub's answer and a GH_PROXY mirror's answer (both via
# latest_tag(), below) AND the downloads mirror's catalog answer — a consumer
# that is not is exactly how a channel leaks (a stable host reading a .beta.
# stamp, or the mirror image on a beta host). Set here, not inside
# latest_tag(): that function runs at the end of a pipeline, in a subshell, so
# a variable it set would not survive to the caller. STABLE_TAG_RE is set on
# the beta twin only: the twin resolves BOTH channels and picks the newest
# (beta_channel_pick, below), so it needs the stable shape too.
case "$CHANNEL" in
    beta)
        TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
        STABLE_TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$" ;;
    *)
        TAG_RE="^${COMP}/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
        STABLE_TAG_RE="" ;;
esac
PUBKEY="@PUBKEY@"
# The version floor: the stamp this component was at when THIS installer was
# generated and published (baked from versions/<comp>.stamp — or
# versions/<comp>.beta.stamp on the twin — by tools/gen-bootstraps.sh, which
# release.sh re-runs on every cut). A tag resolved from the network must be at
# least this version; see "version floor" below. It rides the same first-party
# static channel, over the same TLS fetch, that delivered $PUBKEY, so it costs
# no trust the installer did not already require; and no download source gets
# to choose it.
MIN_VERSION="@MIN_VERSION@"
REPO="${UMBREE_RELEASE_REPO:-umbree-git/release}"
PREFIX="${PREFIX:-$HOME/.local}"
DL_BASE="${UMBREE_DL_BASE:-}"           # test hook (undocumented to users)
# GitHub HTTP mirrors, tried in order ONLY as a fallback when github.com /
# api.github.com are unreachable (e.g. networks that block or throttle GitHub).
# Each is tried as <mirror>/<original-https-github-url> until one succeeds; the
# downloaded bytes are still minisign- + sha256-verified below, so an untrusted
# mirror cannot inject tampered bytes undetected. Space-separated list.
# ${VAR-default} (not :-) lets `UMBREE_GH_PROXY=` explicitly disable the mirrors
# while an unset value gets the default. Never used when DL_BASE is set.
GH_PROXIES="${UMBREE_GH_PROXY-https://gh-proxy.org https://cdn.gh-proxy.org https://v6.gh-proxy.org https://gh-proxy.com}"
# Operator-controlled public mirror — the downloads bucket release.sh mirrors
# every cut to (the SAME default gen-version-jsonp.sh reads the catalog from,
# baked here at render time by tools/gen-bootstraps.sh). Role differs by use
# and channel: on stable, for VERSION RESOLUTION it is preferred over the
# third-party gh-proxy mirrors when GitHub is unreachable (anti-rollback — see
# the resolution section below), and for byte DOWNLOADS it stays the
# last-resort fallback after GitHub and every gh-proxy mirror. On the beta
# twin it is the beta channel's ONLY source: beta bytes live under
# <comp>/beta/<stamp>/ and nowhere else. Bytes are minisign + sha256 verified
# below regardless of source. ${VAR-default} (not :-) lets
# `UMBREE_DOWNLOADS_BASE=` explicitly disable it. Never used when DL_BASE (the
# test hook) is set.
DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-@DOWNLOADS_BASE@}"

# Production downloads are pinned to HTTPS/TLS1.2 (--proto =https). The
# UMBREE_DL_BASE test hook points at a local plain-HTTP server, so when it is
# set we drop the TLS-only flags (they'd reject http://). That relaxed mode
# stays locked to the test base BY CONSTRUCTION (no separate guard check):
# whenever DL_BASE is set, every dl() fetch uses $BASE=$DL_BASE and the
# gh-proxy / downloads-mirror fallbacks (resolution AND download) are skipped.
#
# --speed-limit/--speed-time abort a STALLED transfer (< ~4 KB/s for 20s) instead
# of hanging until --max-time. This matters for the gh-proxy mirror loop: a mirror
# that streams a few MB then stalls is abandoned in ~20s so the NEXT mirror is
# tried, rather than the install appearing stuck for the full 5-minute max-time.
if [ -n "$DL_BASE" ]; then
    CURL="curl -fsSL --connect-timeout 15 --max-time 300 --speed-limit 4096 --speed-time 20"
else
    CURL="curl -fsSL --proto =https --tlsv1.2 --connect-timeout 15 --max-time 300 --speed-limit 4096 --speed-time 20"
fi

# ---- helpers ------------------------------------------------------------
@INCLUDE:helpers@

# ---- version floor helpers ----------------------------------------------
# BEGIN version-floor  (tools/test-version-floor.sh extracts this block verbatim
# and exercises it directly — keep it self-contained between the markers, and
# keep the markers. LOCAL copy of burrowee's version-floor block; the
# comparator is also what tools/modules/channel-pick.sh compares with.)
#
# semver_of <stamp> — the leading X.Y.Z of a release stamp (vX.Y.Z.<date>.<sha8>
# or vX.Y.Z.beta.<date>.<sha8>). ONLY the semver is compared: the release
# tooling bumps it monotonically on every cut, whereas the date and changeset
# suffixes are not ordered text (two stamps that differ only in changeset would
# compare arbitrarily) — and truncating at three fields is what lets a beta
# stamp compare EQUAL to the stable it graduated into.
semver_of() {
    printf '%s' "${1#v}" | cut -d. -f1-3
}

# is_semver <x> — true only for a bare numeric X.Y.Z. Everything else (empty,
# an unsubstituted @…@ placeholder, a tag shape we don't understand) is NOT a
# version and must never be compared as one.
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

# version_ge <a> <b> — true when semver(a) >= semver(b). Fails CLOSED: if either
# side is not a well-formed semver, the answer is "no".
version_ge() {
    _vg_a="$(semver_of "$1")"
    _vg_b="$(semver_of "$2")"
    is_semver "$_vg_a" || return 1
    is_semver "$_vg_b" || return 1
    if [ "$_vg_a" = "$_vg_b" ]; then return 0; fi
    [ "$(printf '%s\n%s\n' "$_vg_a" "$_vg_b" | sort -V | head -n1)" = "$_vg_b" ]
}

# assert_version_floor <tag> — abort unless <tag> is at least $MIN_VERSION.
#
# <tag> here was answered by the NETWORK — GitHub, a GH_PROXY mirror standing
# in for it, or the downloads mirror's catalog — the very party that then
# serves the artifacts. The signature gates further down compare the signed
# release against itself: an older, genuinely signed triple (zip + sums +
# minisig, all mutually consistent, all really ours) passes every remaining
# gate. That is a silent rollback onto a known-vulnerable build, and no amount
# of signature checking can catch it, because nothing about those bytes is
# wrong — only the choice of WHICH release was wrong.
#
# $MIN_VERSION is the floor the resolver does not get to choose: the stamp this
# component was at when this installer was generated and published. It reached
# this host over the first-party static channel, in the same fetch that
# delivered $PUBKEY — an attacker who can forge it can already forge the signing
# key, so it adds no trust assumption. A hostile resolver can at worst pin you
# to the version the first-party channel itself advertised when you fetched
# this installer. It cannot walk you back any further. An explicit
# UMBREE_VERSION pin is the operator's own choice and is not floored.
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

# ---- channel pick (newest of beta-or-stable, tie to stable) -------------
@INCLUDE:channel-pick@

# Extract the highest tag of THIS channel's shape from a GitHub /releases JSON
# body read on stdin. The /releases order is by tag-commit date, NOT publish
# order, so it is unreliable for "latest" — pick the highest tag via version
# sort. Match only the real "tag_name" FIELD (line-anchored) so release-notes/
# body text that merely contains the literal `"tag_name"` can't spoof the tag.
# Prefer jq (structural); fall back to grep/sed. Used for both the direct
# api.github.com fetch and the GH_PROXY mirror retry.
#
# $TAG_RE (set once, beside $CHANNEL in the knobs section above) narrows the
# match to the tag SHAPE that channel publishes: stable tags never carry a
# ".beta." segment and beta tags always do (tools/version.sh), so one anchored
# regex per channel keeps the two from ever seeing each other's tags. On the
# beta twin the GitHub side is the STABLE side of the pick (a beta is never a
# GitHub Release), so the twin passes STABLE_TAG_RE here explicitly.
latest_tag() {
    _lt_re="${1:-$TAG_RE}"
    if command -v jq >/dev/null 2>&1; then
        jq -r '.[].tag_name // empty' 2>/dev/null
    else
        grep -E '^[[:space:]]*"tag_name"[[:space:]]*:' \
            | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
    fi | grep -E "$_lt_re" | sort -V | tail -n1
}

# Extract the "stamp" field from a <downloads-mirror>/<comp>/[beta/]latest.json
# body read on stdin. Prefer jq (structural — reads only the top-level
# "stamp"); fall back to a line-anchored grep/sed so a "stamp":"…" buried in
# other text can't spoof it. The caller re-checks the value against the
# channel's TAG_RE.
latest_stamp() {
    if command -v jq >/dev/null 2>&1; then
        jq -r '.stamp // empty' 2>/dev/null
    else
        grep -E '^[[:space:]]*"stamp"[[:space:]]*:' \
            | sed -E 's/.*"stamp"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/' \
            | head -n1
    fi
}

# ---- platform detection -------------------------------------------------
@INCLUDE:platform-detect@

# ---- guard against a TEMP / unbaked pubkey ------------------------------
@INCLUDE:pubkey-guard@

# ---- temp workspace -----------------------------------------------------
@INCLUDE:tmp-workspace@

# ---- version resolution -------------------------------------------------
# LOCAL FORK — see docs/adoption-2026-08-25-bootstrap-modules.md: the shared
# version-resolve module's PIN case is hardcoded over Burrowee's four
# components (cli/gateway/edge/agent) and `fail`s on anything else — this
# bootstrap's own component, "@COMP@", isn't in that case, so adopting would
# abort EVERY install unconditionally, not merely lose a behaviour. It also
# resolves through Burrowee's console catalog, which Umbree has no console to
# reach. This local block bakes and asserts the same $MIN_VERSION floor the
# module does, and Umbree's own downloads-mirror anti-rollback ordering
# (below) covers the on-path-attacker concern. Keeping Umbree's own block.
# One pin var, no per-component switch: this generated bootstrap is scoped
# to "@COMP@" alone (baked in at generation time), so there is nothing to
# switch on.
#
# STABLE: resolve from GitHub /releases (TAG_RE), else the downloads
# mirror's <comp>/latest.json, else the gh-proxy mirrors; then the floor.
# BETA (the twin): resolve the beta side from the downloads mirror's
# <comp>/beta/latest.json (its only source) and the stable side exactly as
# stable does, then install the NEWEST of the two comparing X.Y.Z, tie to
# stable (beta_channel_pick) — that tie is graduation: a beta host installs
# the stable release its cycle became without ever changing channel.
# A pin is the operator's explicit choice: it is not floored, and on stable
# it is not shape-checked (unchanged from before the beta channel existed) —
# which means pinning a beta tag through plain install.sh now fetches that
# beta from the downloads mirror instead of 404ing on GitHub. Still
# signature-verified, still explicit; the twin below is stricter.
PIN="${UMBREE_VERSION:-}"
if [ -n "$PIN" ]; then
    TAG="$PIN"
    # The twin accepts a pin of EITHER shape (a beta host may pin the stable
    # release it graduated into); anything else is not a tag of this
    # component and is refused before a byte is fetched.
    if [ "$CHANNEL" = beta ]; then
        printf '%s\n' "$TAG" | grep -Eq "$TAG_RE" || printf '%s\n' "$TAG" | grep -Eq "$STABLE_TAG_RE" \
            || fail "UMBREE_VERSION=$TAG is not a ${COMP} release tag (want ${COMP}/v<X.Y.Z>[.beta].<date>.<sha8>)"
    fi
    info "using pinned version: $TAG"
else
    info "resolving latest ${COMP} release ($CHANNEL)"
    api="https://api.github.com/repos/${REPO}/releases?per_page=100"
    # The GitHub side is always the STABLE shape: on stable that is TAG_RE;
    # on the twin it is STABLE_TAG_RE (a beta never has a Release).
    if [ "$CHANNEL" = beta ]; then gh_re="$STABLE_TAG_RE"; else gh_re="$TAG_RE"; fi
    # shellcheck disable=SC2086  # $CURL is an intentional space-split command string (flags + binary); POSIX sh has no arrays.
    body="$($CURL "$api" 2>/dev/null)" || true
    TAG="$(printf '%s' "$body" | latest_tag "$gh_re")" || true
    # GitHub API unreachable/empty — resolve "latest" from the OPERATOR-CONTROLLED
    # downloads mirror's latest.json FIRST (no auth). Anti-rollback: which TAG is
    # "latest" decides which (genuinely-signed) release gets installed, so an
    # on-path attacker who blocks GitHub must not be able to steer resolution to a
    # stale/hostile third-party gh-proxy mirror serving an old /releases JSON and
    # freeze fresh installs on an older release. The downloads mirror is TLS to
    # an umbree-owned domain and its catalog is written by release.sh at cut
    # time. Skipped under the DL_BASE test hook and when the mirror is disabled.
    if [ -z "$TAG" ] && [ -z "$DL_BASE" ] && [ -n "$DOWNLOADS_BASE" ]; then
        info "GitHub API unreachable — trying $DOWNLOADS_BASE/$COMP/latest.json"
        # shellcheck disable=SC2086  # intentional word-split of $CURL flags
        lj="$($CURL "$DOWNLOADS_BASE/$COMP/latest.json" 2>/dev/null)" || true
        st="$(printf '%s' "$lj" | latest_stamp)" || true
        # Held to the stable shape like every other answer (bytes are still verified below).
        case "$st" in
            v*) if printf '%s\n' "$COMP/$st" | grep -Eq "$gh_re"; then TAG="$COMP/$st"; info "downloads mirror: $TAG"; fi ;;
        esac
    fi
    # Still unresolved — last resort: the third-party gh-proxy mirrors (no auth).
    # These only decide the tag when GitHub AND the downloads mirror are both
    # unreachable; the bytes they serve are minisign + sha256 verified either way.
    # Skipped under the DL_BASE test hook and when mirrors are disabled (empty).
    if [ -z "$TAG" ] && [ -z "$DL_BASE" ] && [ -n "$GH_PROXIES" ]; then
        for _proxy in $GH_PROXIES; do
            info "GitHub API + downloads mirror unreachable — retrying via mirror $_proxy"
            # shellcheck disable=SC2086  # intentional word-split of $CURL flags
            body="$($CURL "$_proxy/$api" 2>/dev/null)" || true
            TAG="$(printf '%s' "$body" | latest_tag "$gh_re")" || true
            if [ -n "$TAG" ]; then info "mirror resolved: $TAG"; break; fi
        done
    fi
    if [ "$CHANNEL" = beta ] && [ -z "$DL_BASE" ]; then
        # The beta side: <comp>/beta/latest.json on the downloads mirror is
        # the only catalog a beta has. Then the pick.
        [ -n "$DOWNLOADS_BASE" ] || fail "this is $SELF, the beta installer, and no downloads mirror is baked into it — regenerate with tools/gen-bootstraps.sh"
        STABLE_TAG="$TAG"
        info "resolving latest ${COMP} beta from $DOWNLOADS_BASE/$COMP/beta/latest.json"
        # shellcheck disable=SC2086  # intentional word-split of $CURL flags
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

# ---- download -----------------------------------------------------------
# LOCAL FORK — see docs/adoption-2026-08-25-bootstrap-modules.md: the shared
# download module builds ZIP="@brand@-${COMP}-${OS}-${ARCH}.zip". For this
# bootstrap's own component, that renders as "@brand@-@COMP@-${OS}-${ARCH}.zip"
# — not the asset name tools/release.sh actually publishes
# ("@COMP@-${OS}-${ARCH}.zip", i.e. "${comp}-*.zip") — and for the "umbree"
# component specifically, where COMP equals the brand, it doubles the prefix
# outright ("umbree-umbree-darwin-arm64.zip"). Adopting as-is would 404 on
# every real release, before even reaching its other difference: the shared
# module's exhausted-fallback is a grant-gated `umbree download-url` R2
# lookup (Burrowee's console/device-grant mechanism), which would also
# REPLACE Umbree's own operator-controlled $UMBREE_DOWNLOADS_BASE mirror
# fallback rather than add to it. Keeping Umbree's own block.
#
# The download base follows the TAG, not the channel: a stable tag comes from
# its GitHub Release (with the mirror fallbacks below), a beta tag ONLY from
# the downloads mirror's <comp>/beta/<stamp>/ — there is no Release and no
# gh-proxy path for it, so the mirror loop is skipped for a beta base.
# Downloads-mirror per-stamp base: <downloads-mirror>/<comp>/[beta/]<stamp>.
# The tag is <comp>/<stamp>; strip the comp/ prefix to recover the stamp.
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
# gh-proxy mirrors route a release download by treating the release TAG as a
# SINGLE path segment. Our tags contain a slash (<comp>/v…), so a LITERAL slash
# splits the tag across two path segments and some mirror edges then fail to
# serve the asset (or return wrong bytes that later fail verification). Build a
# mirror-only base with the tag's slash percent-encoded (%2F) so the tag stays
# one segment. Direct GitHub ($BASE) keeps the literal slash (it 404s on %2F).
MIRROR_BASE="https://github.com/${REPO}/releases/download/$(printf '%s' "${TAG}" | sed 's#/#%2F#g')"

dl() {
    # dl <remote-name> <local-name>  (local goes under $TMP)
    #
    # Primary: $BASE (GitHub release, the downloads mirror for a beta tag, or
    # the $UMBREE_DL_BASE test hook). Mirror fallback (stable tags only): if
    # the primary fails, retry the %2F-encoded GitHub URL ($MIRROR_BASE) through
    # each GH_PROXIES HTTP mirror in turn (no auth, helps GitHub-blocked
    # networks), then the downloads mirror. minisign + sha256 verification
    # below is unchanged regardless of source, so an untrusted mirror cannot
    # inject tampered bytes undetected.
    info "GET $BASE/$1"
    # shellcheck disable=SC2086  # $CURL is an intentional space-split command string (flags + binary); POSIX sh has no arrays.
    if $CURL -o "$TMP/$2" "$BASE/$1" 2>/dev/null; then
        return 0
    fi
    # Each full mirror URL is printed so a stalled download is diagnosable from output.
    if [ -z "$DL_BASE" ] && [ "$TAG_IS_BETA" = 0 ] && [ -n "$GH_PROXIES" ]; then
        for _proxy in $GH_PROXIES; do
            info "primary failed; trying mirror: $_proxy/$MIRROR_BASE/$1"
            # shellcheck disable=SC2086  # intentional word-split of $CURL flags
            if $CURL -o "$TMP/$2" "$_proxy/$MIRROR_BASE/$1" 2>/dev/null; then
                ok "downloaded $1 via mirror $_proxy"
                return 0
            fi
        done
    fi
    # Last resort for a stable tag: the downloads mirror (when configured).
    # Untrusted — the minisign + sha256 verification below is unchanged, so it
    # cannot inject tampered bytes. Skipped under the DL_BASE test hook / disabled.
    if [ -z "$DL_BASE" ] && [ "$TAG_IS_BETA" = 0 ] && [ -n "$DOWNLOADS_BASE" ]; then
        info "mirrors failed; trying downloads mirror: $DOWNLOADS_FILE_BASE/$1"
        # shellcheck disable=SC2086  # intentional word-split of $CURL flags
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

# ---- provide minisign (package manager, then pinned upstream) ----------
@INCLUDE:install-minisign-common@
@INCLUDE:install-minisign-linux@
@INCLUDE:install-minisign-darwin@

# ---- require minisign ---------------------------------------------------
@INCLUDE:require-minisign@

# ---- VERIFY (the trust gate) --------------------------------------------
@INCLUDE:verify-signature@

info "verifying checksum"
# 2) the zip's checksum against the now-trusted sums file
@INCLUDE:verify-checksum@
ok "checksum verified"

# ---- unzip + exec the verified inner installer --------------------------
command -v unzip >/dev/null 2>&1 \
    || fail "unzip not found — install it (\`brew install unzip\` / \`apt-get install unzip\`) and retry"
unzip -q -o "$TMP/$ZIP" -d "$TMP/x" || fail "zip extraction failed — corrupt download?"
[ -f "$TMP/x/install.sh" ] || fail "release zip missing inner install.sh — aborting"

ok "verified — running inner installer"
# Run with cwd = the unzipped dir: the inner installer resolves its binary
# relative to its own location (./@COMP@).
#
# The two components have DIFFERENT inner-installer contracts, so the
# env vars passed through must match what EACH inner installer actually
# reads — a name that only matches one of the two produces exactly the
# "served UMBREE_UNINSTALL, read UMBREED_UNINSTALL" mismatch this block
# exists to prevent:
#   umbree   — simple bin-placer: reads PREFIX + UMBREE_UNINSTALL.
#   umbreed  — canonical sudo-minimal daemon installer: reads PREFIX +
#              UMBREED_UNINSTALL + UMBREED_NO_SERVICE (see the daemon
#              repo's install/install.sh.in).
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

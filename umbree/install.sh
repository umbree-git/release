#!/bin/sh
# Umbree outer bootstrap — THE TRUST ANCHOR (POSIX sh, macOS + Linux).
#
#   curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbree/install.sh | sh
#
# This is the stable, curl'd-alone entry point for the `umbree` component. It
# NEVER runs an unverified byte: it downloads the release zip + SHA256SUMS.txt +
# its minisig, verifies the minisign signature with a baked-in PUBLIC key,
# verifies the zip's sha256 against the now-trusted sums file, and ONLY THEN
# unzips and execs the verified inner per-release install.sh. Any failure aborts
# before anything is installed.
#
# One template renders TWO files per component: umbree/install.sh (the stable
# channel) and, while a beta cycle is open, its twin umbree/beta.install.sh
# (the beta channel — same URL base, prefixed name; `umbree update` on a beta
# host re-runs the twin, so its name and URL never move). Which one this is
# is baked in as $CHANNEL below.
#
# DO NOT EDIT generated copies (umbree/install.sh, umbree/beta.install.sh) by
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
COMP="umbree"
# "stable" or "beta" — which release channel this bootstrap resolves against.
# Baked at render time, never read from the environment: the channel is a
# property of WHICH URL was published (release.umbree.org/umbree/install.sh vs
# its .../beta.install.sh twin), and a runtime override would make one file
# behave as the other — the channel flip that silently migrated a beta fleet
# once already.
CHANNEL="stable"
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
PUBKEY="RWQZyK0l3lgdSYfj8VXhoTWlVVVcRqfnuVROJzloNrw9NBFm11IeD3HN"
# The version floor: the stamp this component was at when THIS installer was
# generated and published (baked from versions/<comp>.stamp — or
# versions/<comp>.beta.stamp on the twin — by tools/gen-bootstraps.sh, which
# release.sh re-runs on every cut). A tag resolved from the network must be at
# least this version; see "version floor" below. It rides the same first-party
# static channel, over the same TLS fetch, that delivered $PUBKEY, so it costs
# no trust the installer did not already require; and no download source gets
# to choose it.
MIN_VERSION="v0.1.8.2026.08.31.46b36734"
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
DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-https://downloads.umbree.org}"

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
# BEGIN helpers
fail() { printf '\n  ✗ %s\n\n' "$*" >&2; exit 1; }
info() { printf '  → %s\n' "$*"; }
ok()   { printf '  ✓ %s\n' "$*"; }
# END helpers

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

# ---- guard against a TEMP / unbaked pubkey ------------------------------
# BEGIN pubkey-guard
case "$PUBKEY" in
    ""|*REPLACE*|*PLACEHOLDER*|*TEMP*)
        fail "this installer was built without a real signing key — refusing to verify against a placeholder (regenerate with tools/gen-bootstraps.sh)" ;;
esac
# END pubkey-guard

# ---- temp workspace -----------------------------------------------------
# BEGIN tmp-workspace
TMP="$(mktemp -d "${TMPDIR:-/tmp}/umbree-${COMP}-XXXXXX")" || fail "could not create temp dir"
trap 'rm -rf "$TMP"' EXIT INT TERM
# END tmp-workspace

# ---- version resolution -------------------------------------------------
# LOCAL FORK — see docs/adoption-2026-08-25-bootstrap-modules.md: the shared
# version-resolve module's PIN case is hardcoded over Burrowee's four
# components (cli/gateway/edge/agent) and `fail`s on anything else — this
# bootstrap's own component, "umbree", isn't in that case, so adopting would
# abort EVERY install unconditionally, not merely lose a behaviour. It also
# resolves through Burrowee's console catalog, which Umbree has no console to
# reach. This local block bakes and asserts the same $MIN_VERSION floor the
# module does, and Umbree's own downloads-mirror anti-rollback ordering
# (below) covers the on-path-attacker concern. Keeping Umbree's own block.
# One pin var, no per-component switch: this generated bootstrap is scoped
# to "umbree" alone (baked in at generation time), so there is nothing to
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
# download module builds ZIP="umbree-${COMP}-${OS}-${ARCH}.zip". For this
# bootstrap's own component, that renders as "umbree-umbree-${OS}-${ARCH}.zip"
# — not the asset name tools/release.sh actually publishes
# ("umbree-${OS}-${ARCH}.zip", i.e. "${comp}-*.zip") — and for the "umbree"
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

# ---- provide minisign (package manager, then pinned upstream) ----------
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

# ---- require minisign ---------------------------------------------------
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

# ---- VERIFY (the trust gate) --------------------------------------------
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
# 2) the zip's checksum against the now-trusted sums file
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

# ---- unzip + exec the verified inner installer --------------------------
command -v unzip >/dev/null 2>&1 \
    || fail "unzip not found — install it (\`brew install unzip\` / \`apt-get install unzip\`) and retry"
unzip -q -o "$TMP/$ZIP" -d "$TMP/x" || fail "zip extraction failed — corrupt download?"
[ -f "$TMP/x/install.sh" ] || fail "release zip missing inner install.sh — aborting"

ok "verified — running inner installer"
# Run with cwd = the unzipped dir: the inner installer resolves its binary
# relative to its own location (./umbree).
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

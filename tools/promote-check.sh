#!/bin/sh
set -eu

REPO="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
BASE="${UMBREE_R2_DOWNLOADS_BASE-https://downloads.umbree.org}"

help() {
    cat <<'HELP'
promote-check.sh — has this version actually gone public?

A promote is an operator action (release-management.md §5) and the cut chain
ends before it. So every session that must wait for a go-live faces one
question, and until this script existed the only way to answer it was to ask a
human — who can only report an INTENTION to promote. The failure worth
catching is the promote that was carried out and still did not land: bytes
copied, row flipped, manifest write failed. An assertion cannot see that, and
neither can an authenticated read of the catalog, which reaches the row rather
than the thing installers actually resolve.

So this asks the way the public does: an unauthenticated GET of the channel
manifest over the public downloads base, cache-defeating, reading nothing else.
No token, no bucket credential, no console session, and no write of any kind.

Usage:
  tools/promote-check.sh <component> <stable|beta> [--expect <version>]

The component SET is derived from versions/ — what release.sh actually bumps —
so a component added there is checkable here with no edit — the same source
gen-version-jsonp.sh derives its set from.

Prints ONE line — component, channel, the version the manifest names, its
stamp, its `updated` — so the answer is quotable as evidence without a second
command.

Exit:
  0  the expected version is live (or, with no --expect, the manifest was read)
  1  not yet — the manifest resolves and names an OLDER version
  2  usage error
  3  cannot determine — unreachable, absent, malformed, or a NEWER version
     than expected (someone promoted past this work)

1 and 3 are deliberately different exits. A caller that cannot tell them apart
treats an outage as patience, and waits for something that will never happen.

Env (optional):
  UMBREE_R2_DOWNLOADS_BASE   downloads-mirror base (default https://downloads.umbree.org;
                             empty is a refusal here — a check with no surface to read
                             is not a check, so it says so rather than passing)
  UMBREE_CHECK_ALLOW_HTTP  set to 1 to allow a plain-http base — the TEST fixture only
HELP
}

usage() {
    cat >&2 <<USAGE
usage: tools/promote-check.sh <component> <stable|beta> [--expect <version>]

  Reads the public channel manifest and reports what it names. Read-only, no
  credentials. Exit 0 live · 1 not yet · 2 usage · 3 cannot determine.
USAGE
    exit 2
}

COMP=""
CHANNEL=""
EXPECT=""
while [ $# -gt 0 ]; do
    case "$1" in
        --expect)
            [ $# -ge 2 ] || { echo "✗ --expect needs a version" >&2; usage; }
            EXPECT="$2"; shift 2 ;;
        --expect=*) EXPECT="${1#--expect=}"; shift ;;
        -h|--help) help; exit 0 ;;
        -*) echo "✗ unknown flag: $1" >&2; usage ;;
        *)
            if   [ -z "${COMP}" ];    then COMP="$1"
            elif [ -z "${CHANNEL}" ]; then CHANNEL="$1"
            else echo "✗ unexpected argument: $1" >&2; usage
            fi
            shift ;;
    esac
done

[ -n "${COMP}" ] && [ -n "${CHANNEL}" ] || usage
[ -f "${REPO}/versions/${COMP}" ] || {
    echo "✗ unknown component: ${COMP} (no versions/${COMP})" >&2
    usage
}
case "${CHANNEL}" in stable|beta) ;; *) echo "✗ unknown channel: ${CHANNEL}" >&2; usage ;; esac
[ -z "${EXPECT}" ] || case "${EXPECT}" in -*) echo "✗ --expect needs a version" >&2; usage ;; esac

case "${BASE}" in
    https://*) ;;
    http://*)
        if [ "${UMBREE_CHECK_ALLOW_HTTP:-}" != 1 ]; then
            echo "✗ ${BASE} is not https; a plaintext hop answers this question wrong" >&2
            exit 2
        fi ;;
    *) echo "✗ UMBREE_R2_DOWNLOADS_BASE is not a URL: ${BASE}" >&2; exit 2 ;;
esac
BASE="${BASE%/}"
command -v curl >/dev/null 2>&1 || { echo "✗ curl is required" >&2; exit 2; }

case "${CHANNEL}" in
    beta) REL="${COMP}/beta/latest.json" ;;
    *)    REL="${COMP}/latest.json" ;;
esac

URL="${BASE}/${REL}?nocache=$(date +%s)$$"
BODY="$(curl -fsSL --max-time 30 \
              -H 'Cache-Control: no-cache, no-store, max-age=0' \
              -H 'Pragma: no-cache' \
              "${URL}" 2>/dev/null)" || {
    echo "✗ no manifest at ${BASE}/${REL} — nothing has been promoted on this channel, or the component/channel is wrong" >&2
    exit 3
}

field() { printf '%s\n' "${BODY}" | sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1; }
VERSION="$(field version)"
STAMP="$(field stamp)"
UPDATED="$(field updated)"

[ -n "${VERSION}" ] || {
    echo "✗ malformed manifest at ${BASE}/${REL} — no \"version\"" >&2
    exit 3
}

semver() { printf '%s' "$1" | sed -n 's/^v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\)\(\..*\)\{0,1\}$/\1/p'; }
RAW="${VERSION}"
NORM="$(semver "${RAW}")"
if [ -n "${NORM}" ] && [ "${NORM}" != "${RAW}" ]; then
    [ -n "${STAMP}" ] || STAMP="${RAW}"
    VERSION="${NORM}"
fi

printf '%s %s %s %s %s\n' "${COMP}" "${CHANNEL}" "${VERSION}" "${STAMP:-—}" "${UPDATED:-—}"
[ -n "${EXPECT}" ] || exit 0

if [ "${EXPECT}" = "${RAW}" ] || { [ -n "${STAMP}" ] && [ "${EXPECT}" = "${STAMP}" ]; }; then
    exit 0
fi
EXPECT_N="$(semver "${EXPECT}")"
[ -z "${EXPECT_N}" ] || EXPECT="${EXPECT_N}"

CMP="$(awk -v a="${VERSION}" -v b="${EXPECT}" '
function norm(v) { sub(/^[vV]/, "", v); return v }
BEGIN {
    a = norm(a); b = norm(b);
    if (a == b) { print "0"; exit }
    if (a !~ /^[0-9]+(\.[0-9]+)*$/ || b !~ /^[0-9]+(\.[0-9]+)*$/) { print "x"; exit }
    na = split(a, A, "."); nb = split(b, B, ".");
    n = (na > nb) ? na : nb;
    for (i = 1; i <= n; i++) {
        x = (i <= na) ? A[i] + 0 : 0;
        y = (i <= nb) ? B[i] + 0 : 0;
        if (x < y) { print "-1"; exit }
        if (x > y) { print "1";  exit }
    }
    print "0"
}')"

case "${CMP}" in
    0)  exit 0 ;;
    -1) echo "✗ not yet — the manifest names ${VERSION}, expected ${EXPECT}" >&2; exit 1 ;;
    1)  echo "✗ the manifest names ${VERSION}, which is NEWER than the expected ${EXPECT} — someone promoted past this work" >&2; exit 3 ;;
    *)  echo "✗ cannot compare ${VERSION} with ${EXPECT} — resolve this by hand rather than by guessing" >&2; exit 3 ;;
esac

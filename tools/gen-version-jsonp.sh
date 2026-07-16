#!/bin/sh
# gen-version-jsonp.sh — write <comp>/version.js, a JSONP snippet reporting the
# current PUBLISHED version of the umbree component. Consumed by a status/
# marketing page to render a live version badge via a plain <script src>
# (JSONP — no CORS, works on the static release.umbree.org surface with no
# dynamic backend).
#
# Source of truth: the local versions/<comp> file (offline fallback), with an
# optional downloads-mirror catalog (<mirror>/<comp>/latest.json) preferred
# when UMBREE_R2_DOWNLOADS_BASE is configured — empty by default, since no
# mirror is stood up yet, so the local file is the PRIMARY source. The emitted
# file calls a FIXED global callback the page defines before injecting the
# script:
#
#   __umbreeVersion({"component":"umbree","version":"0.1.0","stamp":"v0.1.0.…"});
#
# Usage:
#   tools/gen-version-jsonp.sh                # the umbree component
#   tools/gen-version-jsonp.sh umbree         # same, explicit (release.sh passes it)
#
# Env:
#   UMBREE_VERSION_CALLBACK      global callback name (default __umbreeVersion)
#   UMBREE_R2_DOWNLOADS_BASE     downloads-mirror catalog base URL (default empty — disabled)
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
CALLBACK="${UMBREE_VERSION_CALLBACK:-__umbreeVersion}"
R2_BASE="${UMBREE_R2_DOWNLOADS_BASE:-}"

COMPS="$*"
[ -n "${COMPS}" ] || COMPS="umbree"

# json_str KEY < json  — extract a top-level string value from the pretty-printed
# latest.json (one "key": "value" pair per line). Portable sed, no jq dependency.
json_str() {
    sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
}

for comp in ${COMPS}; do
    case "${comp}" in
        umbree) ;;
        *) echo "✗ unknown component: ${comp}" >&2; exit 2 ;;
    esac

    version=""; stamp=""
    # Prefer the authoritative downloads-mirror catalog when one is configured
    # (empty by default — no mirror stood up yet, so this is skipped and the
    # offline versions/<comp> fallback below is the primary source).
    if [ -n "${R2_BASE}" ]; then
        json="$(curl -fsSL --max-time 10 "${R2_BASE}/${comp}/latest.json" 2>/dev/null || true)"
        if [ -n "${json}" ]; then
            version="$(printf '%s\n' "${json}" | json_str version)"
            stamp="$(printf '%s\n' "${json}" | json_str stamp)"
        fi
    fi
    # These remote-sourced values are embedded into version.js — served on
    # release.umbree.org and EXECUTED as JS wherever it's injected. The
    # [^"]* extraction above already prevents quote breakout, but validate the
    # shape too so a corrupted/hostile catalog value (spaces, backslashes,
    # garbage) can't propagate verbatim; on mismatch fall back as if the
    # catalog were absent.
    if [ -n "${version}" ] && ! printf '%s' "${version}" | grep -Eq '^[0-9][0-9.]*$'; then
        echo "⚠ ${comp}: malformed catalog version '${version}' — falling back to versions/${comp}" >&2
        version=""
    fi
    if [ -n "${stamp}" ] && ! printf '%s' "${stamp}" | grep -Eq '^v[0-9A-Za-z.]*$'; then
        echo "⚠ ${comp}: malformed catalog stamp '${stamp}' — omitting stamp" >&2
        stamp=""
    fi
    # Offline fallback: the local marketing version (no stamp).
    [ -n "${version}" ] || version="$(cat "${ROOT}/versions/${comp}" 2>/dev/null || true)"
    [ -n "${version}" ] || { echo "✗ no version for ${comp} (downloads-mirror catalog + versions/${comp} both empty)" >&2; exit 1; }

    out="${ROOT}/${comp}/version.js"
    mkdir -p "${ROOT}/${comp}"
    tmp="${out}.tmp.$$"
    printf '%s({"component":"%s","version":"%s","stamp":"%s"});\n' \
        "${CALLBACK}" "${comp}" "${version}" "${stamp}" > "${tmp}"
    mv -f "${tmp}" "${out}"
    echo "✓ wrote ${out} (${comp} ${version})"
done

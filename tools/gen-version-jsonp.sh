#!/bin/sh
# gen-version-jsonp.sh — write <comp>/version.js, a JSONP snippet reporting the
# current PUBLISHED version of a component. Consumed by a status/marketing
# page to render a live version badge via a plain <script src> (JSONP — no
# CORS, works on the static release.umbree.org surface with no dynamic
# backend).
#
# Source of truth: the downloads-mirror catalog (<mirror>/<comp>/latest.json)
# when UMBREE_R2_DOWNLOADS_BASE resolves (it defaults to the live mirror; set
# it empty to disable), else the local versions/<comp> file (offline fallback,
# no stamp). The emitted file calls a FIXED global callback the page defines
# before injecting the script:
#
#   __umbreeVersion({"component":"umbree","version":"0.1.0","stamp":"v0.1.0.…"});
#
# BETA: `--channel beta` writes <comp>/beta.version.js instead — the same
# callback and shape, read from <mirror>/<comp>/beta/latest.json, with the
# offline fallback versions/<comp>.beta + versions/<comp>.beta.stamp (never
# versions/<comp>). It is rendered ONLY while versions/<comp>.beta.stamp exists
# (a beta cycle is open and has been cut); otherwise the local beta.version.js
# is removed, the same sweep rule tools/gen-bootstraps.sh applies to the
# install twin. The two beta twins — beta.install.sh and beta.version.js — are
# what `umbree update` fetches on a beta host (feature 02 of the brand-root
# project); their names and URLs are a contract and never move. A stable run
# never writes or removes beta.version.js.
#
# Usage:
#   tools/gen-version-jsonp.sh                       # every component with a versions/<comp> file
#   tools/gen-version-jsonp.sh umbree                # one component, explicit (release.sh passes it)
#   tools/gen-version-jsonp.sh umbree umbreed        # several, explicit
#   tools/gen-version-jsonp.sh --channel beta umbree # the beta twin(s)
#
# Env:
#   UMBREE_VERSION_CALLBACK      global callback name (default __umbreeVersion)
#   UMBREE_R2_DOWNLOADS_BASE     downloads-mirror catalog base URL (default: the live mirror; empty = disabled)
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
CALLBACK="${UMBREE_VERSION_CALLBACK:-__umbreeVersion}"
R2_BASE="${UMBREE_R2_DOWNLOADS_BASE-https://downloads.umbree.org}"

CHANNEL=stable
if [ "${1:-}" = "--channel" ]; then
    CHANNEL="${2:-}"; shift 2
    case "$CHANNEL" in
        stable|beta) ;;
        *) echo "✗ --channel must be stable or beta (got '$CHANNEL')" >&2; exit 2 ;;
    esac
fi

COMPS="$*"
# A bare invocation is a human regenerating everything (release.sh always
# passes an explicit component), so default to every component that has a
# versions/<comp> file — the same file each iteration below validates a
# named component against — rather than hardcoding one component name here.
if [ -z "${COMPS}" ]; then
    for vf in "${ROOT}"/versions/*; do
        [ -f "${vf}" ] || continue
        case "$(basename "${vf}")" in *.*) continue ;; esac   # .beta / .stamp companions
        COMPS="${COMPS} $(basename "${vf}")"
    done
    COMPS="${COMPS# }"
fi

# json_str KEY < json  — extract a top-level string value from the pretty-printed
# latest.json (one "key": "value" pair per line). Portable sed, no jq dependency.
json_str() {
    sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
}

for comp in ${COMPS}; do
    # The real precondition isn't a fixed name allowlist — it's that
    # versions/<comp> exists, since that's what this script reads below (and
    # what every other real component-name check in this repo keys off).
    if [ ! -f "${ROOT}/versions/${comp}" ]; then
        echo "✗ unknown component: ${comp} (no versions/${comp})" >&2
        exit 2
    fi

    if [ "$CHANNEL" = beta ]; then
        out="${ROOT}/${comp}/beta.version.js"
        catalog="${R2_BASE}/${comp}/beta/latest.json"
        local_version="${ROOT}/versions/${comp}.beta"
        local_stamp="${ROOT}/versions/${comp}.beta.stamp"
        if [ ! -f "${local_stamp}" ]; then
            if [ -f "${out}" ]; then
                rm -f "${out}"
                echo "→ ${comp}: no beta cycle open (${local_stamp} absent) — removed stale: beta.version.js"
            else
                echo "→ ${comp}: no beta cycle open (${local_stamp} absent) — beta.version.js not rendered"
            fi
            continue
        fi
    else
        out="${ROOT}/${comp}/version.js"
        catalog="${R2_BASE}/${comp}/latest.json"
        local_version="${ROOT}/versions/${comp}"
        local_stamp="${ROOT}/versions/${comp}.stamp"
    fi

    version=""; stamp=""
    # Prefer the authoritative downloads-mirror catalog when one is configured.
    if [ -n "${R2_BASE}" ]; then
        json="$(curl -fsSL --max-time 10 "${catalog}" 2>/dev/null || true)"
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
        echo "⚠ ${comp}: malformed catalog version '${version}' — falling back to ${local_version#"${ROOT}"/}" >&2
        version=""
    fi
    if [ -n "${stamp}" ] && ! printf '%s' "${stamp}" | grep -Eq '^v[0-9A-Za-z.]*$'; then
        echo "⚠ ${comp}: malformed catalog stamp '${stamp}' — omitting stamp" >&2
        stamp=""
    fi
    # Offline fallback: the local version file, and the local stamp file when
    # it exists (stable's is written by release.sh; beta's presence gated above).
    [ -n "${version}" ] || version="$(tr -d '[:space:]' < "${local_version}" 2>/dev/null || true)"
    [ -n "${version}" ] || { echo "✗ no version for ${comp} (${catalog} + ${local_version#"${ROOT}"/} both empty)" >&2; exit 1; }
    [ -n "${stamp}" ] || [ ! -f "${local_stamp}" ] || stamp="$(tr -d '[:space:]' < "${local_stamp}")"

    mkdir -p "${ROOT}/${comp}"
    tmp="${out}.tmp.$$"
    printf '%s({"component":"%s","version":"%s","stamp":"%s"});\n' \
        "${CALLBACK}" "${comp}" "${version}" "${stamp}" > "${tmp}"
    mv -f "${tmp}" "${out}"
    echo "✓ wrote ${out} (${comp} ${version}${stamp:+ ${stamp}})"
done

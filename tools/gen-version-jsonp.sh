#!/bin/sh
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
if [ -z "${COMPS}" ]; then
    for vf in "${ROOT}"/versions/*; do
        [ -f "${vf}" ] || continue
        case "$(basename "${vf}")" in *.*) continue ;; esac
        COMPS="${COMPS} $(basename "${vf}")"
    done
    COMPS="${COMPS# }"
fi

json_str() {
    sed -n 's/.*"'"$1"'"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1
}

for comp in ${COMPS}; do
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
    if [ -n "${R2_BASE}" ]; then
        json="$(curl -fsSL --max-time 10 "${catalog}" 2>/dev/null || true)"
        if [ -n "${json}" ]; then
            version="$(printf '%s\n' "${json}" | json_str version)"
            stamp="$(printf '%s\n' "${json}" | json_str stamp)"
        fi
    fi
    if [ -n "${version}" ] && ! printf '%s' "${version}" | grep -Eq '^[0-9][0-9.]*$'; then
        echo "⚠ ${comp}: malformed catalog version '${version}' — falling back to ${local_version#"${ROOT}"/}" >&2
        version=""
    fi
    if [ -n "${stamp}" ] && ! printf '%s' "${stamp}" | grep -Eq '^v[0-9A-Za-z.]*$'; then
        echo "⚠ ${comp}: malformed catalog stamp '${stamp}' — omitting stamp" >&2
        stamp=""
    fi
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

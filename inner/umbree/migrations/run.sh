#!/bin/sh
set -eu
UMBREE_LADDER_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${UMBREE_LADDER_DIR}/lib.sh"

LEDGER='
0.1.4 v0_1_to_v0_1_cache_block.sh
'

usage() { warn "usage: run.sh [--assume-below <version> | --installed-version <version>] [--rerun-recorded]"; exit 64; }

FLOOR=""
STRICT=""
RERUN=0
while [ $# -gt 0 ]; do
    case "$1" in
        --assume-below|--installed-version)
            [ $# -ge 2 ] || usage
            [ -z "${FLOOR}${STRICT}" ] || { warn "run.sh: --assume-below and --installed-version are mutually exclusive"; usage; }
            version_ok "$2" || { warn "run.sh: $1 needs a numeric version, got '$2'"; usage; }
            if [ "$1" = --assume-below ]; then FLOOR="$2"; else STRICT="$2"; fi
            shift 2 ;;
        --rerun-recorded) RERUN=1; shift ;;
        *) warn "run.sh: unknown argument '$1'"; usage ;;
    esac
done

if [ "$(id -u)" = 0 ] && [ -n "${SUDO_USER:-}" ]; then
    fail "migrate: never under sudo — run as ${SUDO_USER}"
fi

ROOT="$(umbree_config_root)" || fail "migrate: HOME is empty or not absolute; refusing to guess the config root"
RECEIPTS="${ROOT}/migration-receipts"
ANCHOR_FILE="${ROOT}/.installed-version"

ROWS="$(printf '%s\n' "${LEDGER}" | awk 'NF')"
printf '%s\n' "${ROWS}" | while read -r target script; do
    [ -f "${UMBREE_LADDER_DIR}/${script}" ] || { fail_line "migrate: ledger row ${target} ${script} names a missing script — THIS RELEASE IS INCOMPLETE; nothing has been touched"; exit 1; }
done || exit 1

ANCHOR=""
if [ -z "${FLOOR}${STRICT}" ] && [ -f "${ANCHOR_FILE}" ]; then
    ANCHOR="$(version_core "$(head -n 1 "${ANCHOR_FILE}")")"
    [ -n "${ANCHOR}" ] || warn "migrate: ${ANCHOR_FILE} holds no version; each rung's probe decides"
fi
[ -n "${STRICT}" ] && ANCHOR="${STRICT}"
[ -n "${FLOOR}" ] && export UMBREE_MIGRATION_FORCED=1

SELECTED=""
while read -r target script; do
    [ -n "${target}" ] || continue
    receipt="${RECEIPTS}/${script}@${target}.done"
    if [ -n "${FLOOR}" ]; then
        version_lt "${target}" "${FLOOR}" && { say "skip ${script} (target ${target} below floor ${FLOOR})"; continue; }
    elif [ -n "${ANCHOR}" ]; then
        version_lt "${ANCHOR}" "${target}" || { say "skip ${script} (installed ${ANCHOR} >= ${target})"; continue; }
    fi
    if [ -f "${receipt}" ]; then
        if [ "$(head -n 1 "${receipt}")" = "tree=${ROOT} target=${target}" ]; then
            if [ "${RERUN}" = 1 ]; then
                say "reopening receipt ${receipt} (--rerun-recorded)"
            else
                say "skip ${script} (recorded ${receipt})"; continue
            fi
        else
            warn "migrate: ${receipt} is foreign or unprovenanced; re-evaluating"
        fi
    fi
    if [ -z "${FLOOR}" ] && [ -z "${ANCHOR}" ]; then
        set +e
        sh "${UMBREE_LADDER_DIR}/${script}" --applies
        rc=$?
        set -e
        case "${rc}" in
            0) ;;
            1) say "skip ${script} (probe: not needed)"; continue ;;
            *) fail "migrate: ${script} --applies answered ${rc}, not 0 or 1 — broken probe; refusing the whole run, nothing has been run" ;;
        esac
    fi
    SELECTED="${SELECTED}${target} ${script}
"
done <<ROWS_EOF
${ROWS}
ROWS_EOF

[ -n "${SELECTED}" ] || { say "migrate: nothing to apply"; exit 0; }

LOST=0
while read -r target script; do
    [ -n "${target}" ] || continue
    say "running ${script} (target ${target})"
    set +e
    sh "${UMBREE_LADDER_DIR}/${script}"
    rc=$?
    set -e
    [ "${rc}" = 0 ] || fail "migrate: ${script} exited ${rc} — stopping; later rungs not run"
    receipt="${RECEIPTS}/${script}@${target}.done"
    if (umask 077; mkdir -p "${RECEIPTS}" && printf 'tree=%s target=%s\n' "${ROOT}" "${target}" > "${receipt}.tmp" && mv -f "${receipt}.tmp" "${receipt}") 2>/dev/null; then
        :
    else
        rm -f "${receipt}.tmp" 2>/dev/null || true
        warn "migrate: could not write ${receipt}; the installed version will not be recorded"
        LOST=1
    fi
done <<SEL_EOF
${SELECTED}
SEL_EOF

[ "${LOST}" = 0 ] || exit 3
exit 2

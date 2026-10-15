#!/bin/sh
set -eu
UMBREE_LADDER_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=lib.sh
. "${UMBREE_LADDER_DIR}/lib.sh"

NAME=v0_1_to_v0_1_cache_block
TARGET=0.1.4

case "$#:${1:-}" in
    0:) MODE=perform ;;
    1:--applies) MODE=applies ;;
    *) warn "usage: ${NAME}.sh [--applies]"; exit 64 ;;
esac

ROOT="$(umbree_config_root)" || fail "${NAME}: HOME is empty or not absolute; refusing to guess the config root"
CFG="${ROOT}/config.json"

field() { printf '%s\n' "${REPORT}" | sed -n "s/^$1=//p" | head -n 1; }

scan_into_report() {
    REPORT="$(jsonc_scan "$1" scan)" || REPORT="status=error
reason=scanner failed
at=0"
}

if [ "${MODE}" = applies ]; then
    [ -f "${CFG}" ] || exit 1
    scan_into_report "${CFG}"
    [ "$(field status)" = ok ] || exit 0
    [ "$(field cache_dir_count)" = 0 ] && exit 1
    exit 0
fi

[ -f "${CFG}" ] || exit 0
scan_into_report "${CFG}"
if [ "$(field status)" != ok ]; then
    fail "${NAME}: ${CFG} is not valid JSON/JSONC ($(field reason) at byte $(field at)) — not rewritten; fix it by hand or run 'umbree doctor'"
fi
[ "$(field cache_dir_count)" = 0 ] && exit 0
[ "$(field cache_dir_count)" = 1 ] || fail "${NAME}: ${CFG} has $(field cache_dir_count) top-level cache_dir members — not rewritten"
[ "$(field cache_dir_type)" = string ] || fail "${NAME}: ${CFG}: cache_dir is a $(field cache_dir_type), not a string — not rewritten"
VALUE="$(field cache_dir_value)"
CACHE_COUNT="$(field cache_count)"
[ "${CACHE_COUNT}" -le 1 ] || fail "${NAME}: ${CFG} has ${CACHE_COUNT} top-level cache members — not rewritten"
if [ "${CACHE_COUNT}" = 1 ]; then
    [ "$(field cache_type)" = object ] || fail "${NAME}: ${CFG}: cache is a $(field cache_type), not an object — not rewritten"
    [ "$(field cache_dir_in_cache_count)" -le 1 ] || fail "${NAME}: ${CFG}: cache has several dir members — not rewritten"
    if [ "$(field cache_dir_in_cache_count)" = 1 ] && [ "$(field cache_dir_in_cache_value)" != "${VALUE}" ]; then
        fail "${NAME}: ${CFG}: cache_dir is ${VALUE} but cache.dir is $(field cache_dir_in_cache_value) — not rewritten; choose one by hand"
    fi
fi

DIR="$(dirname "${CFG}")"
TMP=""
TMPB=""
cleanup() { rm -f "${TMP}" "${TMPB}"; }
abort() { cleanup; fail "${NAME}: $* — ${CFG} left as found"; }

BAK="${CFG}.pre-${TARGET}.$(date -u +%Y%m%dT%H%M%SZ)"
TMPB="$(umask 077; mktemp "${DIR}/.config.json.bak.XXXXXX")" || abort "cannot create a backup temp in ${DIR}"
(umask 077; cat "${CFG}" > "${TMPB}") || abort "cannot copy ${CFG} for the backup"
ln "${TMPB}" "${BAK}" 2>/dev/null || abort "backup ${BAK} already exists or cannot be written"
rm -f "${TMPB}"; TMPB=""

TMP="$(umask 077; mktemp "${DIR}/.config.json.new.XXXXXX")" || abort "cannot create a temp in ${DIR}"
(umask 077; jsonc_scan "${CFG}" rewrite > "${TMP}") || abort "the rewrite failed"
chmod 0600 "${TMP}" || abort "cannot set mode 0600 on ${TMP}"
scan_into_report "${TMP}"
[ "$(field status)" = ok ] || abort "the rewritten body does not parse ($(field reason) at byte $(field at))"
[ "$(field cache_dir_count)" = 0 ] || abort "the rewritten body still has a top-level cache_dir"
[ "$(field cache_type)" = object ] || abort "the rewritten body has no cache object"
[ "$(field cache_dir_in_cache_value)" = "${VALUE}" ] || abort "the rewritten cache.dir does not equal ${VALUE}"
mv -f "${TMP}" "${CFG}" || abort "cannot move the rewritten body over ${CFG}"
TMP=""
ok "moved cache_dir -> cache.dir (${VALUE}); backup ${BAK}"

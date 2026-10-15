#!/bin/sh
set -eu

KIT_DIR="$(cd "$(dirname "$0")" && pwd)"
[ -f "${KIT_DIR}/migrations/lib.sh" ] || { printf '%s %s\n' '!!' "install: ${KIT_DIR}/migrations/lib.sh is missing — this kit is incomplete; nothing was changed" >&2; exit 1; }
UMBREE_LADDER_DIR="${KIT_DIR}/migrations"
# shellcheck source=migrations/lib.sh
. "${KIT_DIR}/migrations/lib.sh"

BIN_DIR="${PREFIX:-$HOME/.local}/bin"
BINS="umbree"
CONFIG_ROOT="$(umbree_config_root)" || fail "install: HOME is empty or not absolute; refusing to guess ~/.umbree/cli — nothing was changed"
LADDER_DIR="${CONFIG_ROOT}/ladder"

if [ -n "${UMBREE_UNINSTALL:-}" ]; then
    for b in $BINS; do rm -f "$BIN_DIR/$b"; done
    rm -rf "${LADDER_DIR}"
    ok "removed from $BIN_DIR: $BINS; removed ${LADDER_DIR}"
    exit 0
fi

mkdir -p "$BIN_DIR" || fail "install: cannot create $BIN_DIR — nothing was changed"
for b in $BINS; do
    [ -f "${KIT_DIR}/$b" ] || fail "install: missing $b in archive — nothing was changed"
    install -m 0755 "${KIT_DIR}/$b" "$BIN_DIR/$b" || fail "install: cannot place $BIN_DIR/$b"
    if [ "$(uname -s)" = "Darwin" ]; then
        xattr -d com.apple.quarantine "$BIN_DIR/$b" 2>/dev/null || true
    fi
done
ok "installed to $BIN_DIR: $BINS"

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) say "note: $BIN_DIR is not on PATH — add: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

VERSION_LINE="$("$BIN_DIR/umbree" --version 2>/dev/null | head -n 1)" || VERSION_LINE=""
[ -z "${VERSION_LINE}" ] || say "${VERSION_LINE}"

install_ladder() {
    (umask 077; mkdir -p "${CONFIG_ROOT}") || return 1
    new="${CONFIG_ROOT}/.ladder.new.$$"
    old="${CONFIG_ROOT}/.ladder.old.$$"
    rm -rf "${new}" "${old}"
    (umask 077; mkdir "${new}") || return 1
    chmod 0700 "${new}" || { rm -rf "${new}"; return 1; }
    for f in "${UMBREE_LADDER_DIR}"/*.sh; do
        n="$(basename "$f")"
        case "$n" in *.test.sh|*_test.sh) continue ;; esac
        mode=0755
        [ "$n" != lib.sh ] || mode=0644
        install -m "${mode}" "$f" "${new}/$n" || { rm -rf "${new}"; return 1; }
    done
    if [ -e "${LADDER_DIR}" ]; then
        mv "${LADDER_DIR}" "${old}" || { rm -rf "${new}"; return 1; }
    fi
    if ! mv "${new}" "${LADDER_DIR}"; then
        [ ! -e "${old}" ] || mv "${old}" "${LADDER_DIR}"
        rm -rf "${new}"
        return 1
    fi
    rm -rf "${old}"
}

install_ladder || fail "install: cannot install the migration ladder at ${LADDER_DIR} — the binary is placed, no migration ran"
say "migration ladder installed at ${LADDER_DIR}"

set +e
sh "${LADDER_DIR}/run.sh"
LADDER_RC=$?
set -e

write_anchor() {
    v="$(version_core "$(printf '%s\n' "${VERSION_LINE}" | sed -n 's/^umbree v//p' | head -n 1)")"
    if [ -z "$v" ]; then
        warn "install: cannot read a version from '$BIN_DIR/umbree --version'; ${CONFIG_ROOT}/.installed-version not written"
        return 0
    fi
    if (umask 077; printf '%s\n' "$v" > "${CONFIG_ROOT}/.installed-version.tmp" && mv -f "${CONFIG_ROOT}/.installed-version.tmp" "${CONFIG_ROOT}/.installed-version"); then
        ok "recorded installed version $v"
    else
        warn "install: could not write ${CONFIG_ROOT}/.installed-version"
    fi
}

case "${LADDER_RC}" in
    0|2) write_anchor ;;
    3) warn "install: migrations ran but a receipt was lost; the installed version is not recorded — the next install re-evaluates" ;;
    *) fail "install: migration ladder exited ${LADDER_RC} — $BIN_DIR/umbree is placed but ~/.umbree/cli was not migrated; fix the reported problem and re-run the installer" ;;
esac

dep_burrowee_cli() {
    if ! command -v curl >/dev/null 2>&1; then
        warn "curl not found — install burrowee-cli manually:"
        warn "  curl -fsSL https://release.burrowee.com/cli/install.sh | sh"
        return 0
    fi
    bootstrap="$(curl -fsSL --proto '=https' --tlsv1.2 \
        https://release.burrowee.com/cli/install.sh 2>/dev/null)" || {
        warn "cannot reach release.burrowee.com — skipping burrowee-cli check"
        return 0; }

    if command -v burrowee-cli >/dev/null 2>&1; then
        have="$(burrowee-cli --version 2>/dev/null | awk '$1=="cli"{print $2; exit}')"
        ok "burrowee-cli present (${have:-version unknown}) — dependency satisfied"
        return 0
    fi
    say "burrowee-cli not found — installing from burrowee's public channel"
    printf '%s' "$bootstrap" | sh || warn "burrowee-cli install reported an error"
}
dep_burrowee_cli

say "next: umbree --help   (then: umbree)"

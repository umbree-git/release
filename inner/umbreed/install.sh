#!/bin/sh
# Umbree inner installer — umbreed (POSIX sh).
#
# Ships at the ROOT of the verified release zip as `install.sh`. The outer
# bootstrap verifies the zip (minisign + sha256) and ONLY THEN execs this with
# cwd = the unzipped dir, so the `umbreed` binary sits alongside this script.
#
# Set UMBREED_UNINSTALL to remove instead. Set UMBREED_NO_SERVICE to install
# the binary only and skip the unit.
set -eu

BIN_DIR="${PREFIX:-$HOME/.local}/bin"
BIN="umbreed"

if [ -n "${UMBREED_UNINSTALL:-}" ]; then
    if [ -x "$BIN_DIR/$BIN" ]; then
        echo "removing the system unit (needs root)"
        sudo "$BIN_DIR/$BIN" service uninstall || \
            echo "note: unit removal failed or none was installed" >&2
    fi
    rm -f "$BIN_DIR/$BIN"
    echo "removed from $BIN_DIR: $BIN"
    exit 0
fi

mkdir -p "$BIN_DIR"
install -m 0755 "./$BIN" "$BIN_DIR/$BIN"
if [ "$(uname -s)" = "Darwin" ]; then
    xattr -d com.apple.quarantine "$BIN_DIR/$BIN" 2>/dev/null || true
fi
echo "installed to $BIN_DIR: $BIN"

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "note: $BIN_DIR is not on PATH — add: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

"$BIN_DIR/$BIN" --version 2>/dev/null || true

# =========================================================================
# SYSTEM UNIT
#   umbreed is an always-on exit, so it installs as a system unit that starts
#   at boot and survives logout. Installing a system daemon is one of the two
#   things elevation exists for; nothing else here is elevated, and the unit
#   runs as the human who installed it, not as root.
# =========================================================================
if [ -n "${UMBREED_NO_SERVICE:-}" ]; then
    echo "skipping the system unit (UMBREED_NO_SERVICE set)"
    echo "install it later with: sudo $BIN_DIR/$BIN service install"
    exit 0
fi

echo
echo "umbreed runs as a system service so the exit survives logout and reboot."
echo "This needs root ONCE, to write the unit file. The service itself runs as"
echo "$(id -un), not as root."
if sudo -n true 2>/dev/null || [ -t 0 ]; then
    if sudo "$BIN_DIR/$BIN" service install; then
        "$BIN_DIR/$BIN" service status || true
    else
        echo "note: umbreed itself IS installed at $BIN_DIR/$BIN — only the" >&2
        echo "system unit failed. Run it yourself with:" >&2
        echo "  sudo $BIN_DIR/$BIN service install" >&2
    fi
else
    echo "note: no interactive terminal and no cached sudo credentials, so the"
    echo "system unit was NOT installed — umbreed itself IS installed at"
    echo "$BIN_DIR/$BIN. Install the unit later with:"
    echo "  sudo $BIN_DIR/$BIN service install"
fi

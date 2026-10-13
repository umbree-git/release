#!/bin/sh
set -eu

BIN_DIR="${PREFIX:-$HOME/.local}/bin"
BINS="umbree"

if [ -n "${UMBREE_UNINSTALL:-}" ]; then
    for b in $BINS; do rm -f "$BIN_DIR/$b"; done
    echo "removed from $BIN_DIR: $BINS"
    exit 0
fi

mkdir -p "$BIN_DIR"
for b in $BINS; do
    [ -f "./$b" ] || { echo "missing $b in archive" >&2; exit 1; }
    install -m 0755 "./$b" "$BIN_DIR/$b"
    if [ "$(uname -s)" = "Darwin" ]; then
        xattr -d com.apple.quarantine "$BIN_DIR/$b" 2>/dev/null || true
    fi
done
echo "installed to $BIN_DIR: $BINS"

case ":$PATH:" in
    *":$BIN_DIR:"*) ;;
    *) echo "note: $BIN_DIR is not on PATH — add: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

"$BIN_DIR/umbree" --version 2>/dev/null || true

dep_burrowee_cli() {
    if ! command -v curl >/dev/null 2>&1; then
        echo "note: curl not found — install burrowee-cli manually:" >&2
        echo "  curl -fsSL https://release.burrowee.com/cli/install.sh | sh" >&2
        return 0
    fi
    bootstrap="$(curl -fsSL --proto '=https' --tlsv1.2 \
        https://release.burrowee.com/cli/install.sh 2>/dev/null)" || {
        echo "note: cannot reach release.burrowee.com — skipping burrowee-cli check" >&2
        return 0; }

    if command -v burrowee-cli >/dev/null 2>&1; then
        have="$(burrowee-cli --version 2>/dev/null | awk '$1=="cli"{print $2; exit}')"
        echo "  ✓ burrowee-cli present (${have:-version unknown}) — dependency satisfied"
        return 0
    fi
    echo "  → burrowee-cli not found — installing from burrowee's public channel"
    printf '%s' "$bootstrap" | sh || echo "warn: burrowee-cli install reported an error" >&2
}
dep_burrowee_cli

echo
echo "next: umbree --help   (then: umbree)"

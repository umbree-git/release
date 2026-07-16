#!/bin/sh
# Umbree inner installer — umbree (POSIX sh).
#
# Ships at the ROOT of the verified release zip as `install.sh`. The outer
# bootstrap verifies the zip (minisign + sha256) and ONLY THEN execs this with
# cwd = the unzipped dir, so the `umbree` binary sits alongside this script.
# It installs umbree into PREFIX/bin (default $HOME/.local/bin), then ensures
# the burrowee-cli transport dependency is present. Set UMBREE_UNINSTALL to
# remove umbree instead (the burrowee-cli dependency is left in place — it is
# managed by burrowee's own channel).
#
# umbree has no updater binary and no `umbree update` — this is a fresh/direct
# install + uninstall only (no update-plan/prompt machinery).
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

# =========================================================================
# DEPENDENCY: burrowee-cli (the client transport umbree dials through)
#   No sudo here — burrowee's own installer escalates as it needs to. Install
#   when missing or older than the latest published; never downgrades. This is
#   the ONE public cross-channel step.
#
#   Threat model for the curl|sh below (accepted, by design): we pin transport
#   with --proto '=https' --tlsv1.2 (no http downgrade, no SSLv3/TLS1.0/1.1), so
#   the fetch is authenticated to release.burrowee.com by its TLS cert. We do
#   NOT minisign-verify the fetched bootstrap here: the burrowee bootstrap is its
#   OWN minisign trust-anchor — it verifies the burrowee-cli payload it then
#   downloads against burrowee's release key. Re-verifying here would only couple
#   umbree to burrowee's signing keys. We fetch to a var and pipe so a
#   truncated/failed fetch never partially executes.
# =========================================================================
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

    # The burrowee bootstrap resolves the latest tag itself; we only need to
    # know whether we already have a burrowee-cli at all (and, if so, leave it —
    # the burrowee channel owns its upgrades). Run the burrowee installer only
    # when burrowee-cli is missing or unreadable; never downgrade an existing one.
    have="$(burrowee-cli --version 2>/dev/null | awk '{print $2}')"
    if [ -n "$have" ]; then
        echo "  ✓ burrowee-cli present ($have) — dependency satisfied"
        return 0
    fi
    echo "  → burrowee-cli not found — installing from burrowee's public channel"
    printf '%s' "$bootstrap" | sh || echo "warn: burrowee-cli install reported an error" >&2
}
dep_burrowee_cli

echo
echo "next: umbree --help   (then: umbree)"

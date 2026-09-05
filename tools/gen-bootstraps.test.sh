#!/usr/bin/env bash
# gen-bootstraps.test.sh — asserts a bootstrap is generated per component, and
# that the beta twin (<comp>/beta.install.sh) is rendered ONLY while
# versions/<comp>.beta.stamp exists and is swept when it goes.
#
# gen-bootstraps.sh writes $ROOT/<comp>/install.sh, and those files are
# COMMITTED (only /dist/ and /build/ are ignored). So this runs the real
# generator against the real destination and asserts every component got one.
# The twin cases fabricate versions/umbree.beta.stamp around a run; everything
# they touch is put back, and the suite ends by asserting the tree is clean.
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail=0
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3'"; fail=1;; esac; }
check_lacks() { case "$2" in *"$3"*) echo "FAIL: $1 — unwanted '$3'"; fail=1;; *) echo "ok: $1";; esac; }

FAKE_STAMP="$ROOT/versions/umbree.beta.stamp"
[ ! -e "$FAKE_STAMP" ] || { echo "SKIP-REFUSED: $FAKE_STAMP exists — a beta cycle is open; this suite fabricates that file and will not touch a real one"; exit 1; }
# Restore on every exit path: the committed stable renders and NO twin.
cleanup() {
    rm -f "$FAKE_STAMP" "$ROOT/umbree/beta.install.sh"
    ( cd "$ROOT" && git checkout -q -- umbree/install.sh umbreed/install.sh 2>/dev/null ) || true
}
trap cleanup EXIT

echo "# stable renders"
"$ROOT/tools/gen-bootstraps.sh" >/dev/null || { echo "FAIL: generator exited non-zero"; exit 1; }
for comp in umbree umbreed; do
    if [ ! -f "$ROOT/$comp/install.sh" ]; then
        echo "FAIL: MISSING $comp/install.sh"; fail=1
    fi
done
[ "$fail" -eq 0 ] && echo "ok: a bootstrap per component"
stable="$(cat "$ROOT/umbree/install.sh")"
check_contains "stable bakes CHANNEL=stable" "$stable" 'CHANNEL="stable"'
check_contains "stable bakes the floor from versions/umbree.stamp" "$stable" "MIN_VERSION=\"$(tr -d '[:space:]' < "$ROOT/versions/umbree.stamp")\""
check_lacks "stable TAG_RE has no beta" "$(printf '%s\n' "$stable" | grep -A1 '^    \*)$' | grep 'TAG_RE=' | head -1)" "beta"
check_lacks "no twin while no cycle is open" "$(ls "$ROOT/umbree" 2>/dev/null)" "beta.install.sh"

echo "# the twin appears with the beta stamp"
printf 'v0.2.0.beta.2026.09.05.deadbeef\n' > "$FAKE_STAMP"
out="$("$ROOT/tools/gen-bootstraps.sh" 2>&1)" || { echo "FAIL: generator exited non-zero with a beta stamp: $out"; fail=1; }
check_contains "generator wrote the twin" "$out" "wrote $ROOT/umbree/beta.install.sh"
if [ -f "$ROOT/umbree/beta.install.sh" ]; then
    echo "ok: umbree/beta.install.sh exists"
    twin="$(cat "$ROOT/umbree/beta.install.sh")"
    check_contains "twin bakes CHANNEL=beta" "$twin" 'CHANNEL="beta"'
    check_contains "twin names itself beta.install.sh" "$twin" 'SELF="beta.install.sh"'
    check_contains "twin bakes the beta stamp as its floor" "$twin" 'MIN_VERSION="v0.2.0.beta.2026.09.05.deadbeef"'
    check_contains "twin TAG_RE carries the beta infix" "$twin" '\.beta\.[0-9]{4}'
    check_contains "twin bakes the downloads base" "$twin" 'DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-https://'
    check_lacks "twin has no unsubstituted placeholder" "$twin" '@CHANNEL@'
    check_lacks "twin has no unsubstituted floor" "$twin" '@MIN_VERSION@'
else
    echo "FAIL: umbree/beta.install.sh not rendered"; fail=1
fi
check_lacks "umbreed (no stamp) got no twin" "$(ls "$ROOT/umbreed")" "beta.install.sh"
stable_again="$(cat "$ROOT/umbree/install.sh")"
[ "$stable_again" = "$stable" ] && echo "ok: the stable render is unchanged by an open cycle" || { echo "FAIL: stable render changed when the twin was rendered"; fail=1; }

echo "# the twin is swept when the stamp goes"
rm -f "$FAKE_STAMP"
out="$("$ROOT/tools/gen-bootstraps.sh" 2>&1)" || { echo "FAIL: generator exited non-zero on the sweep: $out"; fail=1; }
check_contains "sweep says what it removed" "$out" "removed stale: beta.install.sh"
check_lacks "twin is gone" "$(ls "$ROOT/umbree")" "beta.install.sh"

echo "# tree clean"
cleanup
dirty="$(cd "$ROOT" && git status --porcelain -- umbree umbreed versions)"
[ -z "$dirty" ] && echo "ok: tree clean after the suite" || { echo "FAIL: tree dirty after the suite:"; echo "$dirty"; fail=1; }

echo
[ "$fail" -eq 0 ] && echo "ALL OK"
exit "$fail"

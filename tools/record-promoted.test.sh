#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REAL_ROOT="$(cd "${HERE}/.." && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — output does not contain '$3':"; printf '%s\n' "$2" | sed 's/^/      /'; fail=1;; esac; }

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
export GIT_CONFIG_GLOBAL="$T/gitconfig"
/usr/bin/git config --file "$GIT_CONFIG_GLOBAL" user.name t
/usr/bin/git config --file "$GIT_CONFIG_GLOBAL" user.email t@t
/usr/bin/git config --file "$GIT_CONFIG_GLOBAL" init.defaultBranch main
unset UMBREE_R2_DOWNLOADS_BASE UMBREE_MIN_VERSION UMBREE_MIN_VERSION_FILE UMBREE_PUBKEY_FILE

STUB="$T/stub"; mkdir -p "$STUB"
cat > "$STUB/go" <<'EOF'
#!/bin/sh
if [ "$1" = run ] && [ "$2" = ./cmd/rkit ] && [ "$3" = components ]; then echo umbree; echo umbreed; exit 0; fi
echo "stub go: unexpected invocation: $*" >&2; exit 1
EOF
chmod +x "$STUB/go"
export PATH="$STUB:$PATH"
export CALLS="$T/calls.log"

copy_tree() {
    mkdir -p "$1"
    ( cd "$REAL_ROOT" && /usr/bin/git ls-files -z ) | ( cd "$REAL_ROOT" && tar --null -cf - -T - ) | ( cd "$1" && tar -xf - )
}

REPO="$T/repo"
copy_tree "$REPO"
cat > "$REPO/tools/promote-check.sh" <<'EOF'
#!/bin/sh
echo "promote-check $*" >> "$CALLS"
[ -z "${STUB_LINE:-}" ] || printf '%s\n' "$STUB_LINE"
[ -z "${STUB_ERR:-}" ] || printf '%s\n' "$STUB_ERR" >&2
exit "${STUB_RC:-0}"
EOF
chmod +x "$REPO/tools/promote-check.sh"
/usr/bin/git -C "$REPO" init -q
/usr/bin/git -C "$REPO" add -A && /usr/bin/git -C "$REPO" commit -q -m baseline

STAMP=v0.1.9.2026.10.09.0123abcd
NEWER=v0.1.10.2026.10.10.89abcdef
FLOOR_BEFORE="$(cat "$REPO/versions/umbree.stamp")"
HEAD_BEFORE="$(/usr/bin/git -C "$REPO" rev-parse HEAD)"

RP="$REPO/tools/record-promoted.sh"
run() { : > "$CALLS"; out="$(bash "$RP" "$@" 2>&1)"; rc=$?; }
nothing_written() {
    check "$1: versions/umbree.stamp unchanged" "$(cat "$REPO/versions/umbree.stamp")" "$FLOOR_BEFORE"
    check "$1: no commit" "$(/usr/bin/git -C "$REPO" rev-parse HEAD)" "$HEAD_BEFORE"
    check "$1: the tree is clean" "$(/usr/bin/git -C "$REPO" status --porcelain --untracked-files=no)" ""
}

echo "# usage"
run
check "no arguments → 2" "$rc" "2"
run umbree
check "one argument → 2" "$rc" "2"
run umbree "$STAMP" extra
check "three arguments → 2" "$rc" "2"
run nosuch "$STAMP"
check "unknown component → 2" "$rc" "2"
run umbree v0.2.0.beta.2026.10.09.0123abcd
check "a beta stamp → 2" "$rc" "2"
check_contains "…says what a stable stamp looks like" "$out" "v<X.Y.Z>.<YYYY>.<MM>.<DD>.<sha8>"
check "…promote-check was never asked" "$(grep -c . "$CALLS")" "0"
out="$(bash "$RP" --help 2>/dev/null)"; rc=$?
check "--help → 0" "$rc" "0"
check_contains "…on stdout, with the usage" "$out" "record-promoted.sh <component> <stamp>"
nothing_written "usage errors"

echo "# refuses when not yet live"
STUB_RC=1 STUB_LINE="umbree stable 0.1.8 v0.1.8.2026.09.20.7162a3f3 2026-09-20T00:00:00Z" STUB_ERR="✗ not yet" run umbree "$STAMP"
check "refuses when not yet live" "$rc" "1"
check_contains "…says not yet" "$out" "not live yet"
nothing_written "not yet"

echo "# refuses when a newer version is live"
STUB_RC=3 STUB_LINE="umbree stable 0.1.10 $NEWER 2026-10-10T00:00:00Z" STUB_ERR="✗ NEWER" run umbree "$STAMP"
check "refuses when a newer version is live" "$rc" "3"
check_contains "…says it cannot record it" "$out" "cannot tell, or a newer version is live"
nothing_written "newer"

echo "# refuses when cannot tell"
STUB_RC=3 STUB_ERR="✗ no manifest" run umbree "$STAMP"
check "refuses when cannot tell" "$rc" "3"
nothing_written "cannot tell"

echo "# expect is the stamp argument"
check "expect is the stamp argument" "$(cat "$CALLS")" "promote-check umbree stable --expect $STAMP"

echo "# stamp written from the verified fetch line"
STUB_RC=0 STUB_LINE="umbree stable 0.1.10 $NEWER 2026-10-10T00:00:00Z" run umbree "$STAMP"
check "stamp written from the verified fetch line: a live answer naming another stamp refuses" "$rc" "1"
check_contains "…names both" "$out" "$NEWER"
nothing_written "mismatched line"
STUB_RC=0 STUB_LINE="umbreed stable 0.1.9 $STAMP 2026-10-09T00:00:00Z" run umbree "$STAMP"
check "…a line for another component refuses" "$rc" "1"
nothing_written "other component"
STUB_RC=0 STUB_LINE="" run umbree "$STAMP"
check "…an exit 0 with no line refuses" "$rc" "1"
nothing_written "no line"

echo dirty >> "$REPO/README.md"
STUB_RC=0 STUB_LINE="umbree stable 0.1.9 $STAMP 2026-10-09T00:00:00Z" run umbree "$STAMP"
check "a dirty tree refuses" "$rc" "1"
check "…promote-check was never asked" "$(grep -c . "$CALLS")" "0"
/usr/bin/git -C "$REPO" checkout -q -- README.md

echo "# refuses off main"
/usr/bin/git -C "$REPO" checkout -q -b elsewhere
STUB_RC=0 STUB_LINE="umbree stable 0.1.9 $STAMP 2026-10-09T00:00:00Z" run umbree "$STAMP"
check "a branch other than main refuses" "$rc" "1"
check_contains "…naming the branch" "$out" "elsewhere"
check "…promote-check was never asked" "$(grep -c . "$CALLS")" "0"
nothing_written "off main"
/usr/bin/git -C "$REPO" checkout -q main
/usr/bin/git -C "$REPO" checkout -q --detach
STUB_RC=0 STUB_LINE="umbree stable 0.1.9 $STAMP 2026-10-09T00:00:00Z" run umbree "$STAMP"
check "a detached HEAD refuses" "$rc" "1"
nothing_written "detached"
/usr/bin/git -C "$REPO" checkout -q main

echo "# live writes stamp regenerates and marks"
STUB_RC=0 STUB_LINE="umbree stable 0.1.9 $STAMP 2026-10-09T00:00:00Z" run umbree "$STAMP"
check "live writes stamp regenerates and marks" "$rc" "0"
check "…one fetch, no second" "$(grep -c . "$CALLS")" "1"
check "…versions/umbree.stamp is the verified stamp" "$(cat "$REPO/versions/umbree.stamp")" "$STAMP"
check "…the bootstrap's floor is the stamp" "$(grep -c "^MIN_VERSION=\"$STAMP\"\$" "$REPO/umbree/install.sh")" "1"
check "…the marker" "$(/usr/bin/git -C "$REPO" log -1 --format=%s)" "[PROMOTED: umbree] $STAMP"
check "…carries exactly the floor and the bootstrap" \
    "$(/usr/bin/git -C "$REPO" show --name-only --format= HEAD | sort | tr '\n' ' ')" "umbree/install.sh versions/umbree.stamp "

echo "# one marker commit only"
check "one marker commit only" "$(/usr/bin/git -C "$REPO" rev-list --count "$HEAD_BEFORE..HEAD")" "1"
check "…and the tree is clean after it" "$(/usr/bin/git -C "$REPO" status --porcelain --untracked-files=no)" ""
STUB_RC=0 STUB_LINE="umbree stable 0.1.9 $STAMP 2026-10-09T00:00:00Z" run umbree "$STAMP"
check "…a second run is a no-op → 0" "$rc" "0"
check_contains "…that says so" "$out" "already recorded"
check "…and adds no commit" "$(/usr/bin/git -C "$REPO" rev-list --count "$HEAD_BEFORE..HEAD")" "1"

echo "# byte-identical to service render"
OTHER="$T/other"
copy_tree "$OTHER"
printf '%s\n' "$STAMP" > "$OTHER/versions/umbree.stamp"
( cd "$OTHER" && sh tools/gen-bootstraps.sh >/dev/null 2>&1 )
if cmp -s "$OTHER/umbree/install.sh" "$REPO/umbree/install.sh"; then same=yes; else same=no; fi
check "byte-identical to service render (gen-bootstraps.sh for the same stamp; the service's renderer is held to the same bytes by TestStaticRenderMatchesShellGenerators)" "$same" "yes"

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

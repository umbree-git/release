#!/usr/bin/env bash
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
fail=0
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3'"; fail=1;; esac; }
check_lacks() { case "$2" in *"$3"*) echo "FAIL: $1 — unwanted '$3'"; fail=1;; *) echo "ok: $1";; esac; }
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }

FAKE_STAMP="$ROOT/versions/umbree.beta.stamp"
[ ! -e "$FAKE_STAMP" ] || { echo "SKIP-REFUSED: $FAKE_STAMP exists — a beta cycle is open; this suite fabricates that file and will not touch a real one"; exit 1; }
cleanup() {
    rm -f "$FAKE_STAMP" "$ROOT/umbree/beta.install.sh" "$ROOT/umbree/beta.version.js"
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
stable_re="$(eval "$(printf '%s\n' "$stable" | sed -n '/^COMP=/,/^MIN_VERSION=/p')"; printf '%s' "$TAG_RE")"
check_contains "stable TAG_RE is the stable shape" "$stable_re" 'umbree/v[0-9]+'
check_lacks "stable TAG_RE has no beta" "$stable_re" "beta"
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

echo "# the twin is swept when the stamp goes (beta.version.js with it)"
rm -f "$FAKE_STAMP"
printf 'x\n' > "$ROOT/umbree/beta.version.js"
out="$("$ROOT/tools/gen-bootstraps.sh" 2>&1)" || { echo "FAIL: generator exited non-zero on the sweep: $out"; fail=1; }
check_contains "sweep says what it removed" "$out" "removed stale: beta.install.sh"
check_lacks "twin is gone" "$(ls "$ROOT/umbree")" "beta.install.sh"
check_contains "sweep also removes beta.version.js" "$out" "beta.version.js"
check_lacks "beta.version.js is gone" "$(ls "$ROOT/umbree")" "beta.version.js"

echo "# the manifest is the only source"
outside_minisign() { awk '/^# BEGIN (install-minisign-common|require-minisign)$/ { skip = 1 } !skip { print } /^# END (install-minisign-common|require-minisign)$/ { skip = 0 }' "$1"; }
for comp in umbree umbreed; do
    gen="$ROOT/$comp/install.sh"
    check_lacks "no github in generated bootstraps: $comp has no api.github.com" "$(cat "$gen")" "api.github.com"
    check_lacks "no github in generated bootstraps: $comp names no release repo" "$(cat "$gen")" "umbree-git/release"
    check_lacks "no github in generated bootstraps: $comp downloads no GitHub release" "$(outside_minisign "$gen" | grep -E 'github\.com/.*/releases')" "github.com"
    check_lacks "no gh-proxy in generated bootstraps: $comp" "$(tr 'A-Z' 'a-z' < "$gen")" "gh-proxy"
    check "no gh-proxy in generated bootstraps: $comp pins the mirror list empty" "$(grep -E '^GH_PROXIES=' "$gen")" 'GH_PROXIES=""'
    check_contains "downloads base baked and https: $comp" "$(cat "$gen")" 'DOWNLOADS_BASE="${UMBREE_DOWNLOADS_BASE-https://'
    check_contains "downloads base baked and https: $comp pins curl to https, redirects too" "$(grep -E '^CURL=' "$gen")" "--proto =https --proto-redir =https --tlsv1.2"
    check "downloads base baked and https: $comp has one curl line" "$(grep -cE '^ *CURL=' "$gen")" "1"
    check_lacks "committed bootstraps carry no test seam: $comp allow-http" "$(cat "$gen")" "UMBREE_TEST_ALLOW_HTTP"
    check_lacks "committed bootstraps carry no test seam: $comp download hook" "$(cat "$gen")" "UMBREE_DL_BASE"
    check_lacks "committed bootstraps carry no test seam: $comp placeholder" "$(cat "$gen")" "@TEST_SEAM@"
    check_lacks "committed bootstraps carry no test seam: $comp has no http curl" "$(cat "$gen")" "--proto =http "
    check_contains "committed bootstraps carry no test seam: $comp pins the hooks off" "$(cat "$gen")" "$(printf 'DL_BASE=""\nGH_PROXIES=""\nALLOW_LOOPBACK_HTTP=0')"
done

echo "# a test build"
W="$(mktemp -d)"
trap 'cleanup; rm -rf "$W"' EXIT
snapshot() { ( cd "$ROOT" && git status --porcelain --untracked-files=all && find . -path ./.git -prune -o -type f -newer "$W/mark" -print ); }
: > "$W/mark"; sleep 1
before="$(snapshot)"
mkdir -p "$W/build"
out="$("$ROOT/tools/gen-bootstraps.sh" --test-build "$W/build" 2>&1)"; rc=$?
check "test build writes only into its dir: exit 0" "$rc" "0"
for comp in umbree umbreed; do
    tb="$W/build/$comp/install.sh"
    if [ -f "$tb" ]; then echo "ok: test build writes only into its dir: $comp rendered"; else echo "FAIL: test build wrote no $tb: $out"; fail=1; continue; fi
    check_contains "test build writes only into its dir: $comp carries the seam" "$(cat "$tb")" "UMBREE_TEST_ALLOW_HTTP"
    check_lacks "test build writes only into its dir: $comp has no placeholder left" "$(cat "$tb")" "@TEST_SEAM@"
done
check "test build writes only into its dir: the repo is untouched" "$(snapshot)" "$before"

mkdir -p "$ROOT/umbree/tb-inside"
out="$("$ROOT/tools/gen-bootstraps.sh" --test-build "$ROOT/umbree/tb-inside" 2>&1)"; rc=$?
check "test build refuses a dir inside the repo: exit 2" "$rc" "2"
check_contains "test build refuses a dir inside the repo: says why" "$out" "inside the repository"
check "test build refuses a dir inside the repo: wrote nothing there" "$(ls -A "$ROOT/umbree/tb-inside")" ""
rmdir "$ROOT/umbree/tb-inside"
out="$("$ROOT/tools/gen-bootstraps.sh" --test-build "$ROOT" 2>&1)"; rc=$?
check "test build refuses a dir inside the repo: the root itself" "$rc" "2"

ln -s "$ROOT" "$W/link-root"
ln -s "$ROOT/umbreed" "$W/link-sub"
for link in link-root link-sub; do
    out="$("$ROOT/tools/gen-bootstraps.sh" --test-build "$W/$link" 2>&1)"; rc=$?
    check "test build refuses a symlink into the repo: $link exit 2" "$rc" "2"
    check_contains "test build refuses a symlink into the repo: $link says why" "$out" "inside the repository"
done
check "test build refuses a symlink into the repo: the repo is untouched" "$(snapshot)" "$before"

mkdir -p "$W/planted"; ln -s "$ROOT/umbree" "$W/planted/umbree"
out="$("$ROOT/tools/gen-bootstraps.sh" --test-build "$W/planted" 2>&1)"; rc=$?
check "test build writes only into its dir: a planted component symlink is refused" "$rc" "2"
check "test build writes only into its dir: nothing written through it" "$(snapshot)" "$before"
for base in "" "http://downloads.example"; do
    mkdir -p "$W/base"
    out="$(UMBREE_R2_DOWNLOADS_BASE="$base" "$ROOT/tools/gen-bootstraps.sh" --test-build "$W/base" 2>&1)"; rc=$?
    check "downloads base baked and https: '$base' is refused" "$rc" "1"
    check "downloads base baked and https: '$base' renders nothing" "$(ls -A "$W/base")" ""
done
out="$("$ROOT/tools/gen-bootstraps.sh" --test-build "$W/absent" 2>&1)"; rc=$?
check "test build refuses a missing dir" "$rc" "2"
out="$("$ROOT/tools/gen-bootstraps.sh" --test-build 2>&1)"; rc=$?
check "test build without a dir is a usage error" "$rc" "2"
check_contains "…and prints the usage" "$out" "Usage: tools/gen-bootstraps.sh"
out="$("$ROOT/tools/gen-bootstraps.sh" --bogus 2>&1)"; rc=$?
check "an unknown argument is a usage error" "$rc" "2"
out="$("$ROOT/tools/gen-bootstraps.sh" --help 2>/dev/null)"; rc=$?
check "--help exits 0" "$rc" "0"
check_contains "--help prints the usage" "$out" "--test-build <dir>"

echo "# module comments are stripped from the render"
"$ROOT/tools/gen-bootstraps.sh" >/dev/null || { echo "FAIL: generator exited non-zero"; fail=1; }
for comp in umbree umbreed; do
    left="$(awk '/^# BEGIN [a-z0-9-]+$/ { d++; next } /^# END [a-z0-9-]+$/ { d--; next } d > 0 && /^[ \t]*#/ && !/^[ \t]*# shellcheck [^ \t]+=/' "$ROOT/$comp/install.sh")"
    check "no module comment line survives in $comp/install.sh" "$left" ""
    check_contains "…the module's shellcheck directive survives" "$(cat "$ROOT/$comp/install.sh")" '        # shellcheck disable=SC2086
        $CURL'
    check_lacks "…without its prose tail" "$(cat "$ROOT/$comp/install.sh")" 'is a command plus its flags'
    check_contains "…and the splice markers" "$(cat "$ROOT/$comp/install.sh")" "# BEGIN verify-checksum"
done
echo "# a comment after a continued line is refused, not stripped"
FX="$(mktemp -d)"
mkdir -p "$FX/tools/modules" "$FX/versions" "$FX/stub"
cp "$ROOT/tools/gen-bootstraps.sh" "$FX/tools/"
cp "$ROOT/umbree-release.pub" "$FX/"
printf '#!/bin/sh\n@INCLUDE:edge@\n' > "$FX/tools/bootstrap.template.sh"
printf 'v0.1.0.2026.10.09.0a1b2c3d\n' > "$FX/versions/umbree.stamp"; cp "$FX/versions/umbree.stamp" "$FX/versions/umbreed.stamp"
printf '#!/bin/sh\necho umbree\necho umbreed\n' > "$FX/stub/go"; chmod +x "$FX/stub/go"
printf 'set -- a \\\n# gone\necho "n=$#"\n' > "$FX/tools/modules/edge.sh"
out="$(cd "$FX" && PATH="$FX/stub:$PATH" sh tools/gen-bootstraps.sh 2>&1)"; rc=$?
check "a continued line before a stripped comment refuses" "$([ "$rc" -ne 0 ] && echo refused)" "refused"
check_contains "…naming the continuation" "$out" "continuation"
check "…and writes no bootstrap" "$([ -e "$FX/umbree/install.sh" ] && echo written)" ""
printf 'set -- a \\\n  b\n# goes\necho "n=$#"\n' > "$FX/tools/modules/edge.sh"
out="$(cd "$FX" && PATH="$FX/stub:$PATH" sh tools/gen-bootstraps.sh 2>&1)"; rc=$?
check "keep-control: a continued line ended by code renders" "$rc" "0"
check "…with the comment stripped" "$(grep -c '^# goes$' "$FX/umbree/install.sh" 2>/dev/null)" "0"
rm -rf "$FX"

echo "# tree clean"
cleanup
dirty="$(cd "$ROOT" && git status --porcelain -- umbree umbreed versions)"
[ -z "$dirty" ] && echo "ok: tree clean after the suite" || { echo "FAIL: tree dirty after the suite:"; echo "$dirty"; fail=1; }

echo
[ "$fail" -eq 0 ] && echo "ALL OK"
exit "$fail"

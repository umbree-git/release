#!/usr/bin/env bash
# test-version-floor.sh — prove the outer bootstrap will not accept a
# network-RESOLVED version older than the floor baked into it, and that each
# channel's resolver only ever sees its own tag shape.
#
# Why the floor exists: when GitHub is unreachable, $TAG is answered by a
# mirror — the same party that then serves the artifacts — so the signature
# gates compare that party's answer against itself, and ANY older, genuinely
# signed release passes. @MIN_VERSION@ is the one input a download source
# cannot choose: it is baked from versions/<comp>.stamp (or .beta.stamp) by
# gen-bootstraps.sh and reaches the host over the first-party static channel.
#
# What this covers:
#   PREDICATE:  semver_of / is_semver / version_ge / assert_version_floor,
#               extracted VERBATIM from a scratch render (between the "BEGIN
#               version-floor" / "END version-floor" markers) and driven
#               directly — the shipped code, not a copy of it.
#   RESOLVER:   latest_tag under each channel's TAG_RE over one mixed tag list.
#   FAIL-CLOSED: an unbaked/placeholder floor aborts rather than waving the
#               resolved version through.
#   GENERATOR:  gen-bootstraps.sh refuses to render with no floor to bake.
#
# Fully hermetic: renders into a scratch copy of the repo (the real tree is
# never touched), no network, no minisign, nothing installed.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/.." && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3' in: $2"; fail=1;; esac; }

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
# A scratch copy of everything the generator reads; `go` is stubbed so the
# component list needs no toolchain here.
mkdir -p "$W/repo/tools" "$W/repo/versions" "$W/bin"
cp -R "$REPO_ROOT/tools/modules" "$W/repo/tools/modules"
cp "$REPO_ROOT/tools/gen-bootstraps.sh" "$REPO_ROOT/tools/bootstrap.template.sh" "$W/repo/tools/"
cp -R "$REPO_ROOT/tools/testkeys" "$W/repo/tools/testkeys"
printf '#!/bin/sh\n[ "$1" = run ] && [ "$3" = components ] && { echo umbree; exit 0; }\nexit 1\n' > "$W/bin/go"
chmod +x "$W/bin/go"
export PATH="$W/bin:$PATH" UMBREE_PUBKEY_FILE="$REPO_ROOT/tools/testkeys/test.pub"

STABLE_FLOOR=v0.1.8.2026.08.31.46b36734
BETA_FLOOR=v0.2.0.beta.2026.09.05.deadbeef
printf '%s\n' "$STABLE_FLOOR" > "$W/repo/versions/umbree.stamp"
printf '%s\n' "$BETA_FLOOR"   > "$W/repo/versions/umbree.beta.stamp"
sh "$W/repo/tools/gen-bootstraps.sh" >/dev/null 2>&1 || { echo "FAIL: scratch render failed"; exit 1; }
STABLE="$W/repo/umbree/install.sh"; TWIN="$W/repo/umbree/beta.install.sh"
[ -f "$TWIN" ] || { echo "FAIL: twin not rendered in the scratch repo"; exit 1; }

# extract <file> <out> — the knobs (COMP…MIN_VERSION), the version-floor block
# and the channel-pick block, plus latest_tag, into a sourceable file with the
# helpers module's fail/info/ok replaced by test doubles.
extract() {
    {
        echo 'fail() { printf "fail: %s\n" "$*"; exit 1; }'
        echo 'info() { :; }'
        echo 'ok() { :; }'
        sed -n '/^COMP=/,/^MIN_VERSION=/p' "$1"
        sed -n '/^# BEGIN version-floor/,/^# END version-floor/p' "$1"
        sed -n '/^# BEGIN channel-pick/,/^# END channel-pick/p' "$1"
        sed -n '/^latest_tag() {/,/^}/p' "$1"
    } > "$2"
    # assert_version_floor calls fail, which exits the bootstrap; every call
    # below runs in a subshell, so that exit is the status the test reads.
}
extract "$STABLE" "$W/stable.sh"; extract "$TWIN" "$W/twin.sh"
for fn in semver_of is_semver version_ge assert_version_floor beta_channel_pick latest_tag; do
    grep -q "^${fn}() {" "$W/twin.sh" || { echo "FAIL: ${fn} not found in the rendered twin"; exit 1; }
done

TAGS="$W/tags.json"
cat > "$TAGS" <<'EOF'
[{"tag_name":"umbree/v0.3.0.beta.2026.09.10.aaaaaaaa"},
 {"tag_name":"umbree/v0.2.1.2026.09.09.bbbbbbbb"},
 {"tag_name":"umbree/v0.1.8.2026.08.31.46b36734"},
 {"tag_name":"umbreed/v9.9.9.2026.09.05.ffffffff"}]
EOF

echo "# RESOLVER: each channel sees only its own shape"
# shellcheck disable=SC1090
( . "$W/stable.sh"; latest_tag < "$TAGS" ) > "$W/out" 2>&1
check "stable latest_tag picks the newest STABLE, ignoring a higher beta" "$(cat "$W/out")" "umbree/v0.2.1.2026.09.09.bbbbbbbb"
( . "$W/twin.sh"; latest_tag < "$TAGS" ) > "$W/out" 2>&1
check "beta latest_tag (TAG_RE beta) picks the beta, ignoring stable" "$(cat "$W/out")" "umbree/v0.3.0.beta.2026.09.10.aaaaaaaa"
( . "$W/twin.sh"; latest_tag "$STABLE_TAG_RE" < "$TAGS" ) > "$W/out" 2>&1
check "twin's explicit STABLE_TAG_RE picks the stable side" "$(cat "$W/out")" "umbree/v0.2.1.2026.09.09.bbbbbbbb"
( . "$W/twin.sh"; beta_channel_pick "umbree/v0.2.0.beta.2026.09.05.deadbeef" "umbree/v0.2.0.2026.09.12.cccccccc" ) > "$W/out"
check "pick: tie on X.Y.Z goes to stable (graduation)" "$(cat "$W/out")" "umbree/v0.2.0.2026.09.12.cccccccc"

echo "# PREDICATE: the floor"
( . "$W/stable.sh"; assert_version_floor umbree/v0.1.7.2026.01.01.aaaaaaaa ) > "$W/out" 2>&1; r=$?
check "v0.1.7 under floor v0.1.8 → refused" "$r" "1"
check_contains "…names the floor" "$(cat "$W/out")" "version floor not met"
( . "$W/stable.sh"; assert_version_floor umbree/v0.1.8.2026.09.01.bbbbbbbb ) > "$W/out" 2>&1; r=$?
check "v0.1.8 (same X.Y.Z, newer date) → ok" "$r" "0"
( . "$W/stable.sh"; assert_version_floor umbree/v0.2.1.2026.09.09.bbbbbbbb ) > "$W/out" 2>&1; r=$?
check "v0.2.1 above the floor → ok" "$r" "0"
( . "$W/twin.sh"; assert_version_floor umbree/v0.2.0.beta.2026.09.06.eeeeeeee ) > "$W/out" 2>&1; r=$?
check "twin: a beta at the beta floor's X.Y.Z → ok" "$r" "0"
( . "$W/twin.sh"; assert_version_floor umbree/v0.2.0.2026.09.12.cccccccc ) > "$W/out" 2>&1; r=$?
check "twin: the graduated stable at the same X.Y.Z → ok" "$r" "0"
( . "$W/twin.sh"; assert_version_floor umbree/v0.1.9.2026.09.12.cccccccc ) > "$W/out" 2>&1; r=$?
check "twin: a stable below the beta floor → refused" "$r" "1"
( . "$W/stable.sh"; version_ge "0.2.0" "vNOPE" ) ; r=$?
check "version_ge fails closed on a malformed side" "$r" "1"

echo "# FAIL-CLOSED: no floor baked"
sed 's/^MIN_VERSION=.*/MIN_VERSION=""/' "$W/twin.sh" > "$W/nofloor.sh"
( . "$W/nofloor.sh"; assert_version_floor umbree/v9.9.9.2026.09.09.bbbbbbbb ) > "$W/out" 2>&1; r=$?
check "empty floor → refused even for a very new tag" "$r" "1"
check_contains "…says no floor baked" "$(cat "$W/out")" "no version floor baked"

echo "# GENERATOR: refuses without a floor to bake"
rm -f "$W/repo/versions/umbree.stamp"
sh "$W/repo/tools/gen-bootstraps.sh" > "$W/out" 2>&1; r=$?
check "generator with no versions/umbree.stamp → 1" "$r" "1"
check_contains "…says cannot bake" "$(cat "$W/out")" "cannot bake"
UMBREE_MIN_VERSION=$STABLE_FLOOR sh "$W/repo/tools/gen-bootstraps.sh" > "$W/out" 2>&1; r=$?
check "UMBREE_MIN_VERSION override renders without the file" "$r" "0"
printf 'garbage\n' > "$W/repo/versions/umbree.stamp"
sh "$W/repo/tools/gen-bootstraps.sh" > "$W/out" 2>&1; r=$?
check "a non-numeric floor is refused" "$r" "1"

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

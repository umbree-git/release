#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${HERE}/.." && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3' in: $2"; fail=1;; esac; }

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
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

extract() {
    {
        echo 'fail() { printf "fail: %s\n" "$*"; exit 1; }'
        echo 'info() { :; }'
        echo 'ok() { :; }'
        sed -n '/^COMP=/,/^MIN_VERSION=/p' "$1"
        sed -n '/^# BEGIN version-floor/,/^# END version-floor/p' "$1"
        sed -n '/^# BEGIN channel-pick/,/^# END channel-pick/p' "$1"
        sed -n '/^latest_stamp() {/,/^}/p' "$1"
        sed -n '/^is_tag() {/,/^}/p' "$1"
    } > "$2"
}
extract "$STABLE" "$W/stable.sh"; extract "$TWIN" "$W/twin.sh"
for fn in semver_of is_semver version_ge assert_version_floor beta_channel_pick latest_stamp is_tag; do
    grep -q "^${fn}() {" "$W/twin.sh" || { echo "FAIL: ${fn} not found in the rendered twin"; exit 1; }
done

NEWEST_BETA=umbree/v0.3.0.beta.2026.09.10.aaaaaaaa
STABLE_TAG=umbree/v0.2.1.2026.09.09.bbbbbbbb
OTHER_COMP=umbreed/v9.9.9.2026.09.05.ffffffff

echo "# RESOLVER: each channel accepts only its own shape"
shape() { ( . "$1"; is_tag "$2" "${!3}" ) && echo yes || echo no; }
check "stable TAG_RE accepts a stable tag" "$(shape "$W/stable.sh" "$STABLE_TAG" TAG_RE)" "yes"
check "stable TAG_RE refuses a beta tag" "$(shape "$W/stable.sh" "$NEWEST_BETA" TAG_RE)" "no"
check "stable TAG_RE refuses another component" "$(shape "$W/stable.sh" "$OTHER_COMP" TAG_RE)" "no"
check "twin TAG_RE accepts a beta tag" "$(shape "$W/twin.sh" "$NEWEST_BETA" TAG_RE)" "yes"
check "twin TAG_RE refuses a stable tag" "$(shape "$W/twin.sh" "$STABLE_TAG" TAG_RE)" "no"
check "twin STABLE_TAG_RE accepts the stable side" "$(shape "$W/twin.sh" "$STABLE_TAG" STABLE_TAG_RE)" "yes"
check "stable BETA_TAG_RE accepts a beta pin" "$(shape "$W/stable.sh" "$NEWEST_BETA" BETA_TAG_RE)" "yes"
check "a tag with a second line is refused" "$(shape "$W/stable.sh" "$(printf '%s\n../x' "$STABLE_TAG")" TAG_RE)" "no"
check "a tag with a trailing path is refused" "$(shape "$W/stable.sh" "$STABLE_TAG/../x" TAG_RE)" "no"
( . "$W/stable.sh"; printf '{"stamp":"v0.2.1.2026.09.09.bbbbbbbb","x":1}' | latest_stamp ) > "$W/out" 2>&1
check "latest_stamp reads the stamp field" "$(cat "$W/out")" "v0.2.1.2026.09.09.bbbbbbbb"
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

#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_match() { if printf '%s' "$2" | grep -Eq "$3"; then echo "ok: $1"; else echo "FAIL: $1 — '$2' !~ /$3/"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — '$2' does not contain '$3'"; fail=1;; esac; }

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
export GIT_CONFIG_GLOBAL="$T/gitconfig"
git config --file "$GIT_CONFIG_GLOBAL" user.name t
git config --file "$GIT_CONFIG_GLOBAL" user.email t@t
mkdir -p "$T/tools" "$T/versions"
cp "$HERE/version.sh" "$T/tools/version.sh"
git -C "$T" init -q -b main
printf '0.1.8\n' > "$T/versions/umbree"
printf '0.1.1\n' > "$T/versions/umbreed"
git -C "$T" add -A && git -C "$T" commit -q -m seed

V="$T/tools/version.sh"
run() { bash "$V" "$@" 2>"$T/err"; echo "rc=$?"; }

check "stable --semver" "$(bash "$V" umbree --semver)" "0.1.8"
check "stable --semver umbreed" "$(bash "$V" umbreed --semver)" "0.1.1"
check_match "stable --stamp has no .beta." "$(SRC_DIR="$T" bash "$V" umbree --stamp)" '^v0\.1\.8\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$'
check "--channel bogus → 2" "$(run umbree --channel bogus --semver)" "rc=2"
check "--seed without --channel beta → 2" "$(run umbree --seed)" "rc=2"
check "--assert without --channel beta → 2" "$(run umbree --assert-beta-above-stable)" "rc=2"

check "beta --semver with no .beta file → 1" "$(run umbree --channel beta --semver)" "rc=1"
check_contains "refusal names --seed" "$(cat "$T/err")" "--seed"

check "seed umbree from 0.1.8 prints 0.2.0" "$(bash "$V" umbree --channel beta --seed 2>/dev/null)" "0.2.0"
check "seed wrote versions/umbree.beta" "$(cat "$T/versions/umbree.beta")" "0.2.0"
check "seed staged the file" "$(git -C "$T" status --porcelain versions/umbree.beta)" "A  versions/umbree.beta"
check "seed umbreed from 0.1.1 prints 0.2.0" "$(bash "$V" umbreed --channel beta --seed 2>/dev/null)" "0.2.0"
check "second seed → 1" "$(run umbree --channel beta --seed)" "rc=1"
check_contains "second seed says a cycle is open" "$(cat "$T/err")" "a beta cycle is open"
check "second seed left the file alone" "$(cat "$T/versions/umbree.beta")" "0.2.0"
check "stable file untouched by the seed" "$(cat "$T/versions/umbree")" "0.1.8"

check_match "beta --stamp" "$(SRC_DIR="$T" bash "$V" umbree --channel beta --stamp)" '^v0\.2\.0\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$'
check "beta --semver" "$(bash "$V" umbree --channel beta --semver)" "0.2.0"

check "assert: 0.2.0 > 0.1.8 → 0" "$(run umbree --channel beta --assert-beta-above-stable)" "rc=0"
printf '0.1.8\n' > "$T/versions/umbree.beta"
check "assert: 0.1.8 = 0.1.8 → 1" "$(run umbree --channel beta --assert-beta-above-stable)" "rc=1"
check_contains "assert: equal names 'must sort above'" "$(cat "$T/err")" "must sort above"
printf '0.1.9\n' > "$T/versions/umbree.beta"; printf '0.2.0\n' > "$T/versions/umbree"
check "assert: beta 0.1.9 < stable 0.2.0 → 1" "$(run umbree --channel beta --assert-beta-above-stable)" "rc=1"
printf '0.1.8\n' > "$T/versions/umbree"; printf '0.2.0\n' > "$T/versions/umbree.beta"
rm -f "$T/versions/umbreed"
check "assert: missing stable file → 1" "$(run umbreed --channel beta --assert-beta-above-stable)" "rc=1"
printf '0.1.1\n' > "$T/versions/umbreed"

check "beta --bump-patch prints 0.2.1" "$(bash "$V" umbree --channel beta --bump-patch 2>/dev/null)" "0.2.1"
check "beta bump wrote the beta file" "$(cat "$T/versions/umbree.beta")" "0.2.1"
check "beta bump left stable alone" "$(cat "$T/versions/umbree")" "0.1.8"

check_contains "help mentions --seed" "$(bash "$V" umbree --help)" "--seed"
check_contains "help mentions --assert-beta-above-stable" "$(bash "$V" umbree --help)" "--assert-beta-above-stable"

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

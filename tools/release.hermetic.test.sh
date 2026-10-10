#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
mkdir -p "$T/trap/config" "$T/trap/hooks"
printf '#!/bin/sh\necho "trap $0 $*" >> "%s/sprung"\nexit 97\n' "$T" > "$T/trap/go"
printf '#!/bin/sh\necho "trap hook $0" >> "%s/sprung"\nexit 97\n' "$T" > "$T/trap/hooks/pre-commit"
chmod +x "$T/trap/go" "$T/trap/hooks/pre-commit"
printf 'trap-branch\n' > "$T/trap/config/beta-branch"
printf '[core]\n\thooksPath = %s\n' "$T/trap/hooks" > "$T/trap/system.gitconfig"
BUCKET=trap-bucket-not-a-default

out="$(env GO_BIN="$T/trap/go" RELEASE_ORIGIN_REPO_ROOT="$T/trap" UMBREE_R2_BUCKET="$BUCKET" \
    UMBREE_SRC_UMBREED="$T/trap/umbreed-src" \
    GIT_DIR="$T/trap/no-such-git-dir" GIT_WORK_TREE="$T/trap" GIT_INDEX_FILE="$T/trap/index" \
    GIT_OBJECT_DIRECTORY="$T/trap/no-objects" GIT_COMMON_DIR="$T/trap/no-common" GIT_NAMESPACE=trap \
    GIT_CONFIG_PARAMETERS="'core.hookspath'='$T/trap/hooks'" \
    GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0="$T/trap/hooks" \
    GIT_CONFIG_SYSTEM="$T/trap/system.gitconfig" \
    bash "$HERE/release.test.sh" 2>&1)"; rc=$?
check "release.test.sh passes with overrides aimed at traps in its environment" "$rc" "0"
[ "$rc" = 0 ] || printf '%s\n' "$out" | grep -E '^FAIL' | head -20
check "…no trap was executed" "$([ -e "$T/sprung" ] && cat "$T/sprung")" ""
check "…and the trap bucket never reached the cut" "$(printf '%s\n' "$out" | grep -c "$BUCKET")" "0"

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

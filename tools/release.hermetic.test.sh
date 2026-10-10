#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
mkdir -p "$T/trap/config"
printf '#!/bin/sh\necho "trap $0 $*" >> "%s/sprung"\nexit 97\n' "$T" > "$T/trap/go"
chmod +x "$T/trap/go"
printf 'trap-branch\n' > "$T/trap/config/beta-branch"

out="$(GO_BIN="$T/trap/go" RELEASE_ORIGIN_REPO_ROOT="$T/trap" GIT_DIR="$T/trap/no-such-git-dir" \
    GIT_WORK_TREE="$T/trap" GIT_INDEX_FILE="$T/trap/index" UMBREE_R2_BUCKET=trap-bucket \
    bash "$HERE/release.test.sh" 2>&1)"; rc=$?
check "release.test.sh passes with overrides aimed at traps in its environment" "$rc" "0"
[ "$rc" = 0 ] || printf '%s\n' "$out" | grep -E '^FAIL' | head -20
check "…and the GO_BIN trap was never executed" "$([ -e "$T/sprung" ] && cat "$T/sprung")" ""

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

#!/usr/bin/env bash
set -uo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MOD="$ROOT/tools/modules/platform-detect.sh"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — '$2' lacks '$3'"; fail=1;; esac; }

W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT
mkdir -p "$W/base"
ln -s "$(command -v cut)" "$W/base/cut"

fake_host() {
    local dir="$W/host.$RANDOM$RANDOM"
    mkdir -p "$dir"
    printf '#!/bin/sh\ncase "$1" in -s) echo %s ;; -m) echo %s ;; esac\n' "$1" "$2" > "$dir/uname"
    printf '#!/bin/sh\n[ "$1" = -n ] && [ "$2" = sysctl.proc_translated ] && echo %s\n' "${4:-0}" > "$dir/sysctl"
    case "$3" in
        absent) ;;
        exits) printf '#!/bin/sh\nexit 1\n' > "$dir/sw_vers" ;;
        *) printf '#!/bin/sh\n[ "$1" = -productVersion ] && echo %s\n' "$3" > "$dir/sw_vers" ;;
    esac
    chmod +x "$dir"/*
    printf '%s' "$dir"
}

detect() {
    local dir
    dir="$(fake_host "$@")"
    PATH="$dir:$W/base" /bin/sh -c 'exec 3>&1; fail() { printf "REFUSED: %s" "$*" >&3; exit 1; }; COMP=umbreed; . "$1" >/dev/null 2>&1; printf "%s/%s" "$OS" "$ARCH" >&3' _ "$MOD"
}

echo "# platform-detect v2"
check "the module is v2" "$(sed -n '1s/^# module: platform-detect  *\(v[0-9]*\).*/\1/p' "$MOD")" "v2"

echo "# Intel below macOS 12 gets the legacy zip, 12+ the stock one"
check "Intel 10.15 → amd64-legacy" "$(detect Darwin x86_64 10.15.7)" "darwin/amd64-legacy"
check "Intel 11.7 → amd64-legacy" "$(detect Darwin x86_64 11.7.10)" "darwin/amd64-legacy"
check "Intel 12.0 → amd64" "$(detect Darwin x86_64 12.0)" "darwin/amd64"
check "Intel 15.3 → amd64" "$(detect Darwin x86_64 15.3)" "darwin/amd64"

echo "# Apple Silicon below 12 is refused, 12+ is unchanged"
refusal="$(detect Darwin arm64 11.7.10)"
check "Apple Silicon 11.7 → refused" "${refusal%%:*}" "REFUSED"
check_contains "…naming macOS 12" "$refusal" "upgrade to macOS 12 or later"
check "Apple Silicon 12.0 → arm64" "$(detect Darwin arm64 12.0)" "darwin/arm64"
check "Apple Silicon 15.3 → arm64" "$(detect Darwin arm64 15.3)" "darwin/arm64"

echo "# an unreadable version reads as current: the stock zip, never a refusal"
for arch in x86_64:amd64 arm64:arm64; do
    for sw in absent exits garbage ""; do
        check "${arch%%:*} with sw_vers '${sw}' → ${arch##*:}" "$(detect Darwin "${arch%%:*}" "$sw")" "darwin/${arch##*:}"
    done
done

echo "# a Rosetta-translated shell follows uname -m"
check "Rosetta on 12.4 → amd64" "$(detect Darwin x86_64 12.4 1)" "darwin/amd64"
check "Rosetta on 11.6 → amd64-legacy" "$(detect Darwin x86_64 11.6 1)" "darwin/amd64-legacy"

echo "# Linux is untouched"
check "Linux x86_64 ignores sw_vers" "$(detect Linux x86_64 10.15)" "linux/amd64"
check "Linux amd64 → amd64" "$(detect Linux amd64 absent)" "linux/amd64"
check "Linux aarch64 → arm64" "$(detect Linux aarch64 absent)" "linux/arm64"
check "Linux arm64 below-12 version → arm64, no refusal" "$(detect Linux arm64 11.0)" "linux/arm64"

echo
[ "$fail" -eq 0 ] && echo "ALL OK"
exit "$fail"

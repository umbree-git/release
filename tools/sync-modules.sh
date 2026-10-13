#!/bin/sh
set -eu
LOCAL_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [ "${1:-}" = "--repo" ]; then LOCAL_ROOT="$2"; shift 2; fi
REMOTE_ROOT="${1:-}"
[ -n "$REMOTE_ROOT" ] || { echo "usage: sync-modules.sh [--repo <local>] <other-repo-root>" >&2; exit 64; }
[ -d "$REMOTE_ROOT/tools/modules" ] || { echo "✗ no tools/modules under $REMOTE_ROOT" >&2; exit 1; }
EXCLUDE="$LOCAL_ROOT/tools/modules/MODULES.exclude"

ver_of() {
    v="$(sed -n '1,4s/^# module:[[:space:]]*[a-z0-9-]*[[:space:]]*v\([0-9][0-9]*\).*/\1/p' "$1" | head -1)"
    case "$v" in
        ''|*[!0-9]*) echo "✗ $1: missing or malformed '# module: <name>  vN' header" >&2; exit 1 ;;
    esac
    echo "$v"
}
sha_of() {
    if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
    else sha256sum "$1" | awk '{print $1}'; fi
}
is_excluded() {
    [ -f "$EXCLUDE" ] || return 1
    awk -v n="$1" '/^[[:space:]]*#/ { next } $1 == n { hit = 1 } END { exit !hit }' "$EXCLUDE"
}

rc=0
for rf in "$REMOTE_ROOT"/tools/modules/*.sh; do
    name="$(basename "$rf" .sh)"
    lf="$LOCAL_ROOT/tools/modules/$name.sh"
    rv="$(ver_of "$rf")"
    if is_excluded "$name"; then
        printf '%-20s  --     v%-4s NOT CARRIED (MODULES.exclude — not copied)\n' "$name" "$rv"
        continue
    fi
    if [ ! -f "$lf" ]; then
        cp "$rf" "$lf"
        printf '%-20s   --  -> v%-4s NEW\n' "$name" "$rv"
        continue
    fi
    lv="$(ver_of "$lf")"
    if [ "$lv" -lt "$rv" ]; then
        cp "$rf" "$lf"
        printf '%-20s v%-3s -> v%-4s UPDATED\n' "$name" "$lv" "$rv"
    elif [ "$lv" -gt "$rv" ]; then
        printf '%-20s v%-3s    v%-4s AHEAD (they are behind)\n' "$name" "$lv" "$rv"
    elif [ "$(sha_of "$lf")" = "$(sha_of "$rf")" ]; then
        printf '%-20s v%-3s == v%-4s ok\n' "$name" "$lv" "$rv"
    else
        printf '%-20s v%-3s != v%-4s LOCAL FORK (same version, different bytes)\n' "$name" "$lv" "$rv"
        rc=1
    fi
done
echo
echo "re-run tools/lock-modules.sh and tools/test-modules.sh after any copy."
exit "$rc"

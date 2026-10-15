#!/bin/sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "${HERE}/testkit.test.sh"
tk_begin

make_ladder() {
    L="${TK_ROOT}/$1"
    mkdir -p "$L"
    cp "${HERE}/lib.sh" "${HERE}/upgrade.sh" "$L/"
    {
        echo '#!/bin/sh'
        printf '%s\n' "$2"
        cat <<'FAKE'
printf '%s\n' "$*" > "$HOME/argv"
exit "$(cat "$HOME/rc" 2>/dev/null || echo 0)"
FAKE
    } > "$L/run.sh"
}
REAL_LEDGER="$(awk '$0 == "LEDGER='"'"'" { on = 1 } on { print } on && $0 == "'"'"'" { exit }' "${HERE}/run.sh")"
make_ladder real "${REAL_LEDGER}"
REAL="$L"
make_ladder fixture "LEDGER='
0.1.4 a.sh
0.3.0 b.sh
0.2.0 c.sh
'"
FIX="$L"

fresh() { H="${TK_ROOT}/home.$1"; rm -rf "$H"; mkdir -p "$H/.umbree/cli"; }
up() { d="$1"; shift; HOME="$H" UMBREE_ICONS=text sh "$d/upgrade.sh" "$@" >"${TK_ROOT}/out" 2>"${TK_ROOT}/err"; RC=$?; OUT="$(cat "${TK_ROOT}/out")"; ERR="$(cat "${TK_ROOT}/err")"; ARGV="$(cat "$H/argv" 2>/dev/null)"; }

check "real ledger's newest target is 0.1.4" contains "${REAL_LEDGER}" "0.1.4 v0_1_to_v0_1_cache_block.sh"

fresh default
up "${REAL}"
check "no argument: floor 0.1.4 from the ledger" eq "${ARGV}" "--assume-below 0.1.4 --rerun-recorded"
check "no argument: prints the selected rung" contains "${OUT}" "v0_1_to_v0_1_cache_block.sh (target 0.1.4)"
check "no argument: exit propagated" eq "$RC" 0

fresh above
up "${REAL}" 0.2.0
check "floor above the top: exit 1" eq "$RC" 1
check "floor above the top: names the floor" contains "${ERR}" "0.2.0"
check "floor above the top: names the top" contains "${ERR}" "0.1.4"
check "floor above the top: runner not called" eq "${ARGV}" ""

fresh fixdefault
up "${FIX}"
check "fixture: default floor is the newest target, not the release line" eq "${ARGV}" "--assume-below 0.3.0 --rerun-recorded"
fresh fixbackfill
up "${FIX}" 0.2.0
check "fixture: backfill floor passed verbatim" eq "${ARGV}" "--assume-below 0.2.0 --rerun-recorded"
check "fixture: selects targets at or above the floor" contains "${OUT}" "b.sh (target 0.3.0)"
check "fixture: selects c" contains "${OUT}" "c.sh (target 0.2.0)"
check "fixture: skips older" lacks "${OUT}" "a.sh"

for rc in 0 1 2 3 64; do
    fresh "rc$rc"
    echo "$rc" > "$H/rc"
    up "${FIX}"
    check "runner exit $rc propagated verbatim" eq "$RC" "$rc"
    check "runner exit $rc: no anchor written" absent "$H/.umbree/cli/.installed-version"
done

fresh bad
up "${FIX}" x.y
check "non-numeric floor: 64" eq "$RC" 64
up "${FIX}" 0.1.4 extra
check "two arguments: 64" eq "$RC" 64
up "${FIX}" ""
check "empty floor: 64" eq "$RC" 64
check "usage refusals never call the runner" eq "${ARGV}" ""

tk_end

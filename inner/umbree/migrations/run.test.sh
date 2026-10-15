#!/bin/sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "${HERE}/testkit.test.sh"
tk_begin

L="${TK_ROOT}/ladder"
mkdir -p "$L"
cp "${HERE}/lib.sh" "$L/lib.sh"
awk '
$0 == "LEDGER='"'"'" { print; print "0.1.4 a.sh"; print "0.2.0 b.sh"; skip = 1; next }
skip && $0 == "'"'"'" { skip = 0 }
!skip { print }
' "${HERE}/run.sh" > "$L/run.sh"
for n in a b; do
    cat > "$L/$n.sh" <<'STUB'
#!/bin/sh
n="$(basename "$0" .sh)"
echo "$n ${1:-perform}" >> "$HOME/calls"
if [ "${1:-}" = --applies ]; then exit "$(cat "$HOME/$n.probe" 2>/dev/null || echo 1)"; fi
exit "$(cat "$HOME/$n.rc" 2>/dev/null || echo 0)"
STUB
done

fresh() { H="${TK_ROOT}/home.$1"; rm -rf "$H"; mkdir -p "$H/.umbree/cli"; R="$H/.umbree/cli"; : > "$H/calls"; }
run() { HOME="$H" UMBREE_ICONS=text sh "$L/run.sh" "$@" >"${TK_ROOT}/out" 2>"${TK_ROOT}/err"; RC=$?; OUT="$(cat "${TK_ROOT}/out")"; ERR="$(cat "${TK_ROOT}/err")"; CALLS="$(cat "$H/calls")"; }

fresh none
run
check "no rung applies: exit 0" eq "$RC" 0
check "no rung applies: probes only" eq "${CALLS}" "a --applies
b --applies"

fresh one
echo 0 > "$H/a.probe"
run
check "one rung ran: exit 2" eq "$RC" 2
check "one rung ran: performed after its probe" contains "${CALLS}" "a --applies
b --applies
a perform"
check "one rung ran: printed before running" contains "${OUT}" "-> running a.sh (target 0.1.4)"
check "one rung ran: provenance receipt" eq "$(cat "$R/migration-receipts/a.sh@0.1.4.done")" "tree=$R target=0.1.4"
check "one rung ran: no receipt for b" eq "$(entries "$R/migration-receipts")" "a.sh@0.1.4.done "

fresh refuse
echo 0 > "$H/a.probe"; echo 0 > "$H/b.probe"; echo 1 > "$H/a.rc"
run
check "refusing rung: exit 1" eq "$RC" 1
check "refusing rung: later rung not run" lacks "${CALLS}" "b perform"
check "refusing rung: fail marker" contains "${ERR}" "!! migrate: a.sh exited 1"

fresh lost
echo 0 > "$H/a.probe"
: > "$R/migration-receipts"
run
check "receipt write fails: exit 3" eq "$RC" 3

for bad in "--bogus" "--assume-below 0.1.4 --installed-version 0.1.3" "--assume-below" "--assume-below x.y" "--installed-version 1.2.3.4.5"; do
    fresh usage
    eval "run ${bad}"
    check "usage '${bad}': 64" eq "$RC" 64
    check "usage '${bad}': nothing probed" eq "${CALLS}" ""
done
fresh usage
run --assume-below ""
check "usage empty version: 64" eq "$RC" 64

fresh brokenprobe
echo 0 > "$H/a.probe"; echo 5 > "$H/b.probe"
run
check "broken probe: exit 1" eq "$RC" 1
check "broken probe: no rung ran" lacks "${CALLS}" "perform"
check "broken probe: says so" contains "${ERR}" "b.sh --applies answered 5"

fresh missing
mv "$L/b.sh" "$L/b.sh.away"
echo 0 > "$H/a.probe"
run
mv "$L/b.sh.away" "$L/b.sh"
check "missing ledger script: exit 1" eq "$RC" 1
check "missing ledger script: nothing ran" eq "${CALLS}" ""
check "missing ledger script: names it" contains "${ERR}" "0.2.0 b.sh names a missing script"

for anchor in 0.1.3 0.1.4 0.1.9 "v0.1.9.20261015.abc"; do
    fresh anchor
    echo "${anchor}" > "$R/.installed-version"
    run
    case "${anchor}" in
        0.1.3)
            check "anchor ${anchor}: a pending without probe" eq "$(printf '%s\n' "${CALLS}" | grep '^a ')" "a perform" ;;
        *)
            check "anchor ${anchor}: a skipped without probing" eq "$(printf '%s\n' "${CALLS}" | grep -c '^a ')" 0 ;;
    esac
done

fresh receipt
mkdir -p "$R/migration-receipts"
echo "tree=$R target=0.1.4" > "$R/migration-receipts/a.sh@0.1.4.done"
echo 0 > "$H/a.probe"
run
check "own receipt: skipped, no probe" eq "$(printf '%s\n' "${CALLS}" | grep -c '^a ')" 0
check "own receipt: says recorded" contains "${OUT}" "skip a.sh (recorded"

fresh foreign
mkdir -p "$R/migration-receipts"
echo "tree=/elsewhere/.umbree/cli target=0.1.4" > "$R/migration-receipts/a.sh@0.1.4.done"
run
check "foreign receipt: re-evaluated by probe" contains "${CALLS}" "a --applies"
check "foreign receipt: warned" contains "${ERR}" "foreign or unprovenanced"
fresh unprov
mkdir -p "$R/migration-receipts"
: > "$R/migration-receipts/a.sh@0.1.4.done"
run
check "unprovenanced receipt: re-evaluated by probe" contains "${CALLS}" "a --applies"

fresh rerun
mkdir -p "$R/migration-receipts"
echo "tree=$R target=0.1.4" > "$R/migration-receipts/a.sh@0.1.4.done"
run --assume-below 0.1.4 --rerun-recorded
check "rerun-recorded: exit 2" eq "$RC" 2
check "rerun-recorded: says so per rung" contains "${OUT}" "reopening receipt $R/migration-receipts/a.sh@0.1.4.done (--rerun-recorded)"
check "rerun-recorded: both rungs ran without probes" eq "${CALLS}" "a perform
b perform"

fresh flooronly
mkdir -p "$R/migration-receipts"
echo "tree=$R target=0.1.4" > "$R/migration-receipts/a.sh@0.1.4.done"
run --assume-below 0.2.0
check "floor 0.2.0: older target skipped" lacks "${CALLS}" "a "
check "floor 0.2.0: b runs" eq "${CALLS}" "b perform"

fresh strict
run --installed-version 0.1.4
check "installed-version 0.1.4: skips the rung targeting exactly it" eq "${CALLS}" "b perform"

fresh anchornowrite
echo 0 > "$H/a.probe"
run
check "runner never writes the anchor" absent "$R/.installed-version"

H=relative
HOME="$H" sh "$L/run.sh" >/dev/null 2>&1
check "relative HOME: exit 1" eq "$?" 1

fresh sudo
echo 0 > "$H/a.probe"
mkdir -p "${TK_ROOT}/idshim"
cat > "${TK_ROOT}/idshim/id" <<'IDSHIM'
#!/bin/sh
[ "${1:-}" = -u ] && { echo 0; exit 0; }
echo root
IDSHIM
chmod 0755 "${TK_ROOT}/idshim/id"
HOME="$H" PATH="${TK_ROOT}/idshim:${PATH}" SUDO_USER=alice UMBREE_ICONS=text sh "$L/run.sh" >"${TK_ROOT}/out" 2>"${TK_ROOT}/err"
RC=$?
check "under sudo: exit 1" eq "$RC" 1
check "under sudo: names the user" contains "$(cat "${TK_ROOT}/err")" "run as alice"
check "under sudo: nothing probed or run" eq "$(cat "$H/calls")" ""

tk_end

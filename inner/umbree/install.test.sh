#!/bin/sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "${HERE}/migrations/testkit.test.sh"
tk_begin

KIT="${TK_ROOT}/kit"
mkdir -p "${KIT}/migrations" "${TK_ROOT}/shim"
cp "${HERE}/install.sh" "${KIT}/install.sh"
cp "${HERE}/migrations/lib.sh" "${HERE}/migrations/upgrade.sh" "${HERE}/migrations/v0_1_to_v0_1_cache_block.sh" "${KIT}/migrations/"
cat > "${KIT}/migrations/run.sh" <<'STUB'
#!/bin/sh
echo "ladder ran from $(cd "$(dirname "$0")" && pwd)" >> "$HOME/ladder.calls"
exit "$(cat "$HOME/ladder.rc")"
STUB
echo 'never installed' > "${KIT}/migrations/run.test.sh"
cat > "${KIT}/umbree" <<'BIN'
#!/bin/sh
[ "${1:-}" = --version ] && cat "$HOME/version.line"
BIN
chmod 0755 "${KIT}/umbree"
printf '#!/bin/sh\nexit 1\n' > "${TK_ROOT}/shim/curl"; chmod 0755 "${TK_ROOT}/shim/curl"
P="${TK_ROOT}/shim:${PATH}"

fresh() {
    H="${TK_ROOT}/home.$1"; rm -rf "$H"; mkdir -p "$H"
    R="$H/.umbree/cli"
    echo "umbree v0.1.9 (20261015.abc)" > "$H/version.line"
}
inst() { (cd "${KIT}" && HOME="$H" PREFIX="$H/.local" PATH="$P" UMBREE_ICONS="${ICONS:-text}" "$@" sh ./install.sh) >"${TK_ROOT}/out" 2>"${TK_ROOT}/err"; RC=$?; OUT="$(cat "${TK_ROOT}/out")"; ERR="$(cat "${TK_ROOT}/err")"; }

fresh ran
echo 2 > "$H/ladder.rc"
inst env
check "ladder exit 2: install exit 0" eq "$RC" 0
check "ladder exit 2: anchor holds the binary's version" eq "$(cat "$R/.installed-version")" 0.1.9
check "ladder exit 2: ran the installed ladder" eq "$(cat "$H/ladder.calls")" "ladder ran from $R/ladder"
check "ladder dir mode 0700" eq "$(tk_mode "$R/ladder")" drwx------
check "run.sh mode 0755" eq "$(tk_mode "$R/ladder/run.sh")" -rwxr-xr-x
check "rung mode 0755" eq "$(tk_mode "$R/ladder/v0_1_to_v0_1_cache_block.sh")" -rwxr-xr-x
check "lib.sh mode 0644" eq "$(tk_mode "$R/ladder/lib.sh")" -rw-r--r--
check "no test script installed" absent "$R/ladder/run.test.sh"
check "installer prints the ladder path" contains "${OUT}" "-> migration ladder installed at $R/ladder"
check "text mode ok marker" contains "${OUT}" "ok installed to $H/.local/bin: umbree"
check "binary placed" eq "$(tk_mode "$H/.local/bin/umbree")" -rwxr-xr-x

echo stale > "$R/ladder/stale.sh"
inst env
check "reinstall replaces the ladder copy" absent "$R/ladder/stale.sh"
check "reinstall leaves no temp ladder" eq "$(find "$R" -maxdepth 1 -name '.ladder*' | wc -l | tr -d ' ')" 0

fresh zero
echo 0 > "$H/ladder.rc"
inst env
check "ladder exit 0: anchor written" eq "$(cat "$R/.installed-version")" 0.1.9

fresh lost
echo 3 > "$H/ladder.rc"
inst env
check "ladder exit 3: install exit 0" eq "$RC" 0
check "ladder exit 3: no anchor" absent "$R/.installed-version"
check "ladder exit 3: warn line" contains "${ERR}" "! install: migrations ran but a receipt was lost"

for rc in 1 64 9; do
    fresh "refused$rc"
    echo "$rc" > "$H/ladder.rc"
    inst env
    check "ladder exit $rc: install exit 1" eq "$RC" 1
    check "ladder exit $rc: fail line names the exit" contains "${ERR}" "!! install: migration ladder exited $rc"
    check "ladder exit $rc: no anchor" absent "$R/.installed-version"
    check "ladder exit $rc: binary left placed" eq "$(tk_mode "$H/.local/bin/umbree")" -rwxr-xr-x
done

fresh noversion
echo 2 > "$H/ladder.rc"
echo "something else" > "$H/version.line"
inst env
check "unparseable version: exit 0" eq "$RC" 0
check "unparseable version: no anchor" absent "$R/.installed-version"
check "unparseable version: warn line" contains "${ERR}" "! install: cannot read a version"

fresh glyphs
echo 2 > "$H/ladder.rc"
ICONS=emoji inst env
check "emoji mode ok marker" contains "${OUT}" "✓ installed to"

fresh uninstall
echo 2 > "$H/ladder.rc"
inst env
mkdir -p "$R/migration-receipts"; echo r > "$R/migration-receipts/x.done"; echo '{}' > "$R/config.json"
inst env UMBREE_UNINSTALL=1
check "uninstall: exit 0" eq "$RC" 0
check "uninstall: ladder removed" absent "$R/ladder"
check "uninstall: config, receipts and anchor kept" eq "$(entries "$R")" ".installed-version config.json migration-receipts "
check "uninstall: binary removed" eq "$(entries "$H/.local/bin")" ""

fresh relhome
echo 2 > "$H/ladder.rc"
(cd "${KIT}" && HOME=relative PREFIX="$H/.local" PATH="$P" UMBREE_ICONS=text sh ./install.sh) >/dev/null 2>"${TK_ROOT}/err"
check "relative HOME: exit 1" eq "$?" 1
check "relative HOME: nothing placed" eq "$(entries "$H")" "ladder.rc version.line "

line_output() { cat "${HERE}/install.sh" "${HERE}/migrations/lib.sh" "${HERE}/migrations/run.sh" "${HERE}/migrations/upgrade.sh" "${HERE}/migrations/v0_1_to_v0_1_cache_block.sh"; }
check "no U+2192 arrow in line output" eq "$(line_output | grep -c '→' || true)" 0
check "glyphs spelled only in the helper block" eq "$(line_output | grep -c '✓\|✗' || true)" 1
check "no escaped glyph spellings" eq "$(line_output | grep -c '\\\\x[0-9a-fA-F]\|\\\\u[0-9a-fA-F]' || true)" 0

tk_end

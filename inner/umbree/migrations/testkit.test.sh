TK_FAILS=0
TK_PASSES=0

tk_real_home() {
    awk -F: -v u="$(id -un)" '$1 == u { print $6; exit }' /etc/passwd
}

tk_snapshot() {
    for d in "$(tk_real_home)/.umbree" /usr/local/umbree; do
        printf '== %s\n' "$d"
        ls -laR "$d" 2>&1 || true
    done | cksum
}

tk_begin() {
    TK_SNAP="$(tk_snapshot)"
    TK_ROOT="$(mktemp -d)"
    trap 'rm -rf "${TK_ROOT}"' EXIT
}

check() {
    tk_name="$1"; shift
    if "$@"; then
        TK_PASSES=$((TK_PASSES + 1)); printf 'ok   - %s\n' "${tk_name}"
    else
        TK_FAILS=$((TK_FAILS + 1)); printf 'FAIL - %s\n' "${tk_name}"
    fi
}

contains() { case "$1" in *"$2"*) return 0 ;; esac; return 1; }
lacks() { ! contains "$1" "$2"; }
same_file() { cmp -s "$1" "$2"; }
eq() { [ "$1" = "$2" ]; }

absent() { [ ! -e "$1" ]; }
entries() { find "$1" -mindepth 1 -maxdepth 1 -exec basename {} \; | LC_ALL=C sort | tr '\n' ' '; }

tk_mode() { stat -c %A "$1"; }

tk_end() {
    check "real \$HOME/.umbree and /usr/local/umbree unchanged" eq "$(tk_snapshot)" "${TK_SNAP}"
    printf '%s passed, %s failed\n' "${TK_PASSES}" "${TK_FAILS}"
    [ "${TK_FAILS}" = 0 ] || exit 1
    echo "ALL OK"
}

umbree_markers() {
    loc="${LC_ALL:-${LC_CTYPE:-${LANG:-}}}"
    fancy=0
    case "$(printf '%s' "${loc}" | tr '[:upper:]' '[:lower:]')" in *utf-8*|*utf8*) fancy=1 ;; esac
    case "${TERM:-}" in ''|dumb|linux|vt100*|vt102*|vt220*) fancy=0 ;; esac
    case "$(printf '%s' "${UMBREE_ICONS:-}" | tr '[:upper:]' '[:lower:]')" in
        text) fancy=0 ;;
        emoji) fancy=1 ;;
    esac
    if [ "${fancy}" = 1 ]; then M_INFO='->'; M_OK='✓'; M_FAIL='✗'; M_WARN='!'
    else M_INFO='->'; M_OK='ok'; M_FAIL='!!'; M_WARN='!'; fi
}
umbree_markers

say()  { printf '%s %s\n' "${M_INFO}" "$*"; }
ok()   { printf '%s %s\n' "${M_OK}" "$*"; }
warn() { printf '%s %s\n' "${M_WARN}" "$*" >&2; }
fail() { printf '%s %s\n' "${M_FAIL}" "$*" >&2; exit 1; }
fail_line() { printf '%s %s\n' "${M_FAIL}" "$*" >&2; }

umbree_config_root() {
    case "${HOME:-}" in
        /*) printf '%s/.umbree/cli' "${HOME%/}" ;;
        *) return 1 ;;
    esac
}

version_ok() {
    printf '%s\n' "$1" | grep -Eq '^[0-9]+\.[0-9]+(\.[0-9]+)?$'
}

version_core() {
    printf '%s\n' "$1" | sed -n 's/^v\{0,1\}\([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\).*/\1/p' | head -n 1
}

version_lt() {
    awk -v a="$1" -v b="$2" 'BEGIN {
        na = split(a, x, "."); nb = split(b, y, ".")
        for (i = 1; i <= 3; i++) {
            p = (i <= na) ? x[i] + 0 : 0; q = (i <= nb) ? y[i] + 0 : 0
            if (p < q) exit 0
            if (p > q) exit 1
        }
        exit 1
    }'
}

ledger_rows() {
    awk '$0 == "LEDGER='"'"'" { on = 1; next } on && $0 == "'"'"'" { exit } on && NF { print }' "$1"
}

jsonc_ends_in_newline() {
    [ ! -s "$1" ] && return 0
    [ "$(tail -c 1 "$1" | od -An -c | tr -d ' ')" = '\n' ]
}

# shellcheck disable=SC2016
JSONC_AWK='
function err(msg, at) { if (ERR == "") { ERR = msg; ERRAT = at } T = "err" }
function is_ws(c) { return c == " " || c == "\t" || c == "\n" || c == "\r" }
function scan_string(   q, c) {
    q = p + 1
    while (q <= n) {
        c = substr(s, q, 1)
        if (c == "\\") { q += 2; continue }
        if (c == "\"") { T = "s"; TE = q + 1; p = q + 1; return }
        if (c == "\n") break
        q++
    }
    err("unclosed string", p)
}
function next_tok(   c, d, q, lit) {
    if (ERR != "") { T = "err"; return }
    while (p <= n) {
        c = substr(s, p, 1)
        if (is_ws(c)) { p++; continue }
        if (c == "/") {
            d = substr(s, p + 1, 1)
            if (d == "/") { q = index(substr(s, p), "\n"); if (q == 0) p = n + 1; else p = p + q; continue }
            if (d == "*") { q = index(substr(s, p + 2), "*/"); if (q == 0) { err("unclosed block comment", p); return } p = p + q + 3; continue }
        }
        break
    }
    TS = p
    if (p > n) { T = "eof"; TE = p; return }
    c = substr(s, p, 1)
    if (index("{}[]:,", c) > 0) { T = c; p++; TE = p; return }
    if (c == "\"") { scan_string(); return }
    if (index("-0123456789tfn", c) > 0) {
        q = p
        while (q <= n && index("+-.0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", substr(s, q, 1)) > 0) q++
        lit = substr(s, p, q - p)
        if (lit !~ /^(true|false|null|-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?)$/) { err("invalid literal", p); return }
        T = "l"; p = q; TE = q; return
    }
    err("unexpected character", p)
}
function parse_value(role) {
    if (ERR != "") return
    if (T == "{") { parse_object(role); return }
    if (T == "[") { parse_array(); return }
    if (T == "s" || T == "l") { LASTE = TE; return }
    if (T == "eof") { err("truncated document", TS); return }
    err("expected a value", TS)
}
function parse_array() {
    next_tok(); if (ERR != "") return
    if (T == "]") { LASTE = TE; return }
    while (1) {
        parse_value(""); if (ERR != "") return
        next_tok(); if (ERR != "") return
        if (T == "]") { LASTE = TE; return }
        if (T == "eof") { err("truncated document", TS); return }
        if (T != ",") { err("expected , or ]", TS); return }
        next_tok(); if (ERR != "") return
        if (T == "]") { err("trailing comma", TS); return }
    }
}
function parse_object(role,   m, k, ks, vs, vt, ve, cm, child) {
    if (role == "cache") { cache_n++; c_open = TE; c_members = 0; cdir_n = 0 }
    m = 0
    next_tok(); if (ERR != "") return
    if (T == "}") { LASTE = TE; return }
    while (1) {
        if (T == "eof") { err("truncated document", TS); return }
        if (T != "s") { err("expected a member name", TS); return }
        k = substr(s, TS, TE - TS); ks = TS
        next_tok(); if (ERR != "") return
        if (T == "eof") { err("truncated document", TS); return }
        if (T != ":") { err("expected :", TS); return }
        next_tok(); if (ERR != "") return
        vt = T; vs = TS; m++
        child = ""
        if (role == "top" && k == "\"cache\"") child = "cache"
        parse_value(child); if (ERR != "") return
        ve = LASTE
        next_tok(); if (ERR != "") return
        cm = 0
        if (T == ",") cm = TS
        else if (T == "eof") { err("truncated document", TS); return }
        else if (T != "}") { err("expected , or }", TS); return }
        if (role == "top") { mk[m] = k; mks[m] = ks; mvs[m] = vs; mve[m] = ve; mvt[m] = vt; mcm[m] = cm; ntop = m }
        if (role == "cache") {
            c_members = m
            if (m == 1) c_first = ks
            if (k == "\"dir\"") { cdir_n++; cdir_raw = substr(s, vs, ve - vs); cdir_t = vt }
        }
        if (cm == 0) { LASTE = TE; return }
        next_tok(); if (ERR != "") return
        if (T == "}") { err("trailing comma", TS); return }
    }
}
function tname(vt, raw) {
    if (vt == "s") return "string"
    if (vt == "{") return "object"
    if (vt == "[") return "array"
    if (raw == "true" || raw == "false") return "boolean"
    if (raw == "null") return "null"
    return "number"
}
function raw_of(i) { return substr(s, mvs[i], mve[i] - mvs[i]) }
function line_start(pos) { while (pos > 1 && substr(s, pos - 1, 1) != "\n") pos--; return pos }
function blank(a, b,   i, c) {
    for (i = a; i < b; i++) { c = substr(s, i, 1); if (c != " " && c != "\t" && c != "\r") return 0 }
    return 1
}
function starts_line(pos) { return blank(line_start(pos), pos) }
function eol(pos) { while (pos <= n && substr(s, pos, 1) != "\n") pos++; return pos }
function add_edit(a, b, t) { ne++; ea[ne] = a; eb[ne] = b; et[ne] = t }
function cut_whole_line_or(a, b,   e) {
    e = eol(b)
    if (starts_line(a) && blank(b, e)) { add_edit(line_start(a), (e <= n) ? e + 1 : e, ""); return 1 }
    return 0
}
function remove_member(i,   a, b, c) {
    a = mks[i]; b = mve[i]
    if (mcm[i]) {
        if (blank(b, mcm[i])) {
            b = mcm[i] + 1
            if (cut_whole_line_or(a, b)) return
            while (b <= n && (substr(s, b, 1) == " " || substr(s, b, 1) == "\t")) b++
            add_edit(a, b, ""); return
        }
        add_edit(mcm[i], mcm[i] + 1, "")
        add_edit(a, b, ""); return
    }
    c = mcm[i - 1]
    add_edit(c, c + 1, "")
    if (cut_whole_line_or(a, b)) return
    while (a - 1 > c && (substr(s, a - 1, 1) == " " || substr(s, a - 1, 1) == "\t")) a--
    add_edit(a, b, "")
}
function rewrite(   i, cdi, cci, val, out, pos, j, k, t) {
    for (i = 1; i <= ntop; i++) { if (mk[i] == "\"cache_dir\"") cdi = i; if (mk[i] == "\"cache\"") cci = i }
    val = raw_of(cdi)
    if (!cci) add_edit(mks[cdi], mve[cdi], "\"cache\": {\"dir\": " val "}")
    else {
        remove_member(cdi)
        if (!cdir_n) {
            if (c_members == 0) add_edit(c_open, c_open, "\"dir\": " val)
            else if (starts_line(c_first)) add_edit(c_first, c_first, "\"dir\": " val ",\n" substr(s, line_start(c_first), c_first - line_start(c_first)))
            else add_edit(c_first, c_first, "\"dir\": " val ", ")
        }
    }
    for (i = 1; i <= ne; i++) for (j = i + 1; j <= ne; j++) if (ea[j] < ea[i]) {
        t = ea[i]; ea[i] = ea[j]; ea[j] = t; t = eb[i]; eb[i] = eb[j]; eb[j] = t; t = et[i]; et[i] = et[j]; et[j] = t
    }
    out = ""; pos = 1
    for (k = 1; k <= ne; k++) { out = out substr(s, pos, ea[k] - pos) et[k]; pos = eb[k] }
    out = out substr(s, pos)
    printf "%s", out
}
function report(   i, cd, cc, cdi, cci) {
    if (ERR != "") { print "status=error"; print "reason=" ERR; print "at=" (ERRAT - 1); return }
    print "status=ok"
    for (i = 1; i <= ntop; i++) {
        if (mk[i] == "\"cache_dir\"") { cd++; cdi = i }
        if (mk[i] == "\"cache\"") { cc++; cci = i }
    }
    print "cache_dir_count=" (cd + 0)
    if (cd) { print "cache_dir_type=" tname(mvt[cdi], raw_of(cdi)); print "cache_dir_value=" raw_of(cdi) }
    print "cache_count=" (cc + 0)
    if (cc) {
        print "cache_type=" tname(mvt[cci], raw_of(cci))
        print "cache_dir_in_cache_count=" (cdir_n + 0)
        if (cdir_n) { print "cache_dir_in_cache_type=" tname(cdir_t, cdir_raw); print "cache_dir_in_cache_value=" cdir_raw }
    }
}
{ s = s $0 "\n" }
END {
    if (!nl_end && length(s) > 0) s = substr(s, 1, length(s) - 1)
    n = length(s); p = 1; ERR = ""; ntop = 0; ne = 0; cache_n = 0; cdir_n = 0; c_members = 0
    next_tok()
    if (ERR == "") {
        if (T == "eof") err("empty document", TS)
        else if (T != "{") err("top-level value is not an object", TS)
        else {
            parse_object("top")
            if (ERR == "") { next_tok(); if (ERR == "" && T != "eof") err("trailing content after the top-level object", TS) }
        }
    }
    if (mode == "rewrite") { if (ERR == "") rewrite(); else exit 1 }
    else report()
}'

jsonc_scan() {
    nl=1
    jsonc_ends_in_newline "$1" || nl=0
    LC_ALL=C awk -v mode="$2" -v nl_end="${nl}" "${JSONC_AWK}" "$1"
}

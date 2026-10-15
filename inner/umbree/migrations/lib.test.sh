#!/bin/sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "${HERE}/testkit.test.sh"
. "${HERE}/lib.sh"
tk_begin

F="${TK_ROOT}/c.json"
scan() { printf '%s' "$1" > "$F"; jsonc_scan "$F" scan; }
field_of() { printf '%s\n' "$1" | sed -n "s/^$2=//p"; }

R="$(scan '{
  // "cache_dir": 1
  /* "cache_dir": 2,
     "cache": 3 */
  "url": "http://x//y \"q\" /* not a comment */",
  "profiles": {"home": {"cache_dir": "/nested", "device_key": "k"}},
  "cache_dir": "/a\"b//c" // trailing
}
')"
check "comments and strings: valid" eq "$(field_of "$R" status)" ok
check "comments and strings: one top-level cache_dir" eq "$(field_of "$R" cache_dir_count)" 1
check "comments and strings: value copied as the literal" eq "$(field_of "$R" cache_dir_value)" '"/a\"b//c"'
check "comments and strings: no cache member" eq "$(field_of "$R" cache_count)" 0

R="$(scan '{"profiles": {"p": {"cache_dir": "/nested"}}}')"
check "profile-level cache_dir is not top-level" eq "$(field_of "$R" cache_dir_count)" 0

R="$(scan '{"cache_dir": "/c", "cache": {"rules": [], "dir": "/d"}}')"
check "cache object with dir: type" eq "$(field_of "$R" cache_type)" object
check "cache object with dir: value" eq "$(field_of "$R" cache_dir_in_cache_value)" '"/d"'

R="$(scan '{"cache_dir": 7, "cache": [1]}')"
check "non-string cache_dir typed" eq "$(field_of "$R" cache_dir_type)" number
check "non-object cache typed" eq "$(field_of "$R" cache_type)" array

R="$(scan '{"a": [1, {"b": null}, true, -2.5e3], "c": {}}')"
check "nested arrays and literals are valid" eq "$(field_of "$R" status)" ok

refused() {
    R="$(scan "$2")"
    check "$1: refused" eq "$(field_of "$R" status)" error
    check "$1: reason is '$3'" eq "$(field_of "$R" reason)" "$3"
    check "$1: byte offset $4" eq "$(field_of "$R" at)" "$4"
}
refused "truncated file" '{"a": 1,' "truncated document" 8
refused "unclosed string" '{"a": "x}' "unclosed string" 6
refused "unclosed block comment" '{"a": 1 /* x' "unclosed block comment" 8
refused "two top-level objects" '{}{}' "trailing content after the top-level object" 2
refused "top-level array" '[1]' "top-level value is not an object" 0
refused "trailing comma" '{"a": 1,}' "trailing comma" 8
refused "bare word" '{"a": nope}' "invalid literal" 6
refused "empty file" '' "empty document" 0
refused "comment only" '// nothing' "empty document" 10
refused "missing colon" '{"a" 1}' "expected :" 5

printf '{"cache_dir": "/c"}' > "$F"
check "no trailing newline: rewrite keeps it absent" eq "$(jsonc_scan "$F" rewrite | od -An -c | tr -d ' \n')" '{"cache":{"dir":"/c"}}'
printf '{"cache_dir": "/c"}\n' > "$F"
check "trailing newline: rewrite keeps it" eq "$(jsonc_scan "$F" rewrite | tail -c 1 | od -An -c | tr -d ' ')" '\n'

check "version_lt 0.1.3 < 0.1.4" version_lt 0.1.3 0.1.4
check "version_lt 0.1.4 !< 0.1.4" eval '! version_lt 0.1.4 0.1.4'
check "version_lt 0.1.10 !< 0.1.9" eval '! version_lt 0.1.10 0.1.9'
check "version_core strips v and tail" eq "$(version_core v0.1.9.20261015.abc)" 0.1.9
check "version_ok refuses words" eval '! version_ok x.y'
check "version_ok refuses empty" eval '! version_ok ""'

HOME=relative
check "config root refuses a relative HOME" eval '! umbree_config_root >/dev/null'
HOME=/h/
check "config root follows HOME" eq "$(umbree_config_root)" /h/.umbree/cli

markers_with() {
    (
        UMBREE_ICONS="$1" LC_ALL='' LC_CTYPE='' LANG="$2" TERM="$3"
        export UMBREE_ICONS LC_ALL LC_CTYPE LANG TERM
        umbree_markers
        printf '%s|%s|%s|%s' "${M_INFO}" "${M_OK}" "${M_FAIL}" "${M_WARN}"
    )
}
check "UMBREE_ICONS=text selects ok and !!" eq "$(markers_with text en_US.UTF-8 xterm)" '->|ok|!!|!'
check "UMBREE_ICONS=EMOJI selects glyphs" eq "$(markers_with EMOJI C dumb)" '->|✓|✗|!'
check "LANG=C selects text" eq "$(markers_with '' C xterm)" '->|ok|!!|!'
check "UTF-8 xterm selects glyphs" eq "$(markers_with '' en_US.UTF-8 xterm)" '->|✓|✗|!'
check "UTF-8 dumb terminal selects text" eq "$(markers_with '' en_US.UTF-8 dumb)" '->|ok|!!|!'

tk_end

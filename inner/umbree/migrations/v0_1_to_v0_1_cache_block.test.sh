#!/bin/sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "${HERE}/testkit.test.sh"
tk_begin
RUNG="${HERE}/v0_1_to_v0_1_cache_block.sh"
SHIM="${TK_ROOT}/shim"
mkdir -p "${SHIM}"
BASE_PATH="${PATH}"

fresh() {
    H="${TK_ROOT}/home.$1"
    rm -rf "$H"
    mkdir -p "$H/.umbree/cli"
    CFG="$H/.umbree/cli/config.json"
    DIR="$H/.umbree/cli"
}
put() { printf '%s' "$1" > "${CFG}"; chmod 0644 "${CFG}"; }
expect() { printf '%s' "$1" > "${TK_ROOT}/expected"; }
rung() { HOME="$H" PATH="${RUNG_PATH:-${BASE_PATH}}" UMBREE_ICONS=text sh "${RUNG}" "$@" >"${TK_ROOT}/out" 2>"${TK_ROOT}/err"; RC=$?; OUT="$(cat "${TK_ROOT}/out")"; ERR="$(cat "${TK_ROOT}/err")"; }
listing() { entries "${DIR}"; }
backups() { find "${DIR}" -maxdepth 1 -name 'config.json.pre-0.1.4.*' | wc -l | tr -d ' '; }
the_backup() { find "${DIR}" -maxdepth 1 -name 'config.json.pre-0.1.4.*' -exec basename {} \; | head -n 1; }
temps() { find "${DIR}" -maxdepth 1 -name '.config.json*' | wc -l | tr -d ' '; }

fresh top
ORIG='{
  // top comment
  "listen": "127.0.0.1:1080",
  "cache_dir": "/home/u/.cache/umbree",
  "profiles": {
    "home": {"device_key": "k1", "cache_dir": "/profile/level"},
    "work": {"device_key": "k2"}
  }
}
'
put "${ORIG}"
cp "${CFG}" "${TK_ROOT}/orig"
rung --applies
check "probe: top-level cache_dir needs it" eq "$RC" 0
rung
check "top only: exit 0" eq "$RC" 0
expect '{
  // top comment
  "listen": "127.0.0.1:1080",
  "cache": {"dir": "/home/u/.cache/umbree"},
  "profiles": {
    "home": {"device_key": "k1", "cache_dir": "/profile/level"},
    "work": {"device_key": "k2"}
  }
}
'
check "top only: exactly the expected edit" same_file "${CFG}" "${TK_ROOT}/expected"
check "top only: mode 0600" eq "$(tk_mode "${CFG}")" "-rw-------"
check "top only: one backup" eq "$(backups)" 1
check "top only: backup byte-identical to the original" same_file "${DIR}/$(the_backup)" "${TK_ROOT}/orig"
check "top only: ok line names value and backup" contains "${OUT}" "ok moved cache_dir -> cache.dir (\"/home/u/.cache/umbree\"); backup ${DIR}/config.json.pre-0.1.4."
check "top only: no temp left" eq "$(temps)" 0
cp "${CFG}" "${TK_ROOT}/after1"
rung --applies
check "probe after: not needed" eq "$RC" 1
rung
check "idempotent: second run exit 0" eq "$RC" 0
check "idempotent: file unchanged" same_file "${CFG}" "${TK_ROOT}/after1"
check "idempotent: no second backup" eq "$(backups)" 1

fresh rules
put '{
  "cache_dir": "/c",
  "cache": {
    "rules": [{"match": "*.deb", "ttl": "1h"}]
  }
}
'
rung
check "cache without dir: exit 0" eq "$RC" 0
expect '{
  "cache": {
    "dir": "/c",
    "rules": [{"match": "*.deb", "ttl": "1h"}]
  }
}
'
check "cache without dir: dir inserted, rules unchanged" same_file "${CFG}" "${TK_ROOT}/expected"

fresh inline
put '{"a": 1, "cache_dir": "/c", "cache": {"rules": []}}'
rung
expect '{"a": 1, "cache": {"dir": "/c", "rules": []}}'
check "inline: dir inserted inline" same_file "${CFG}" "${TK_ROOT}/expected"

fresh emptycache
put '{"cache": {}, "cache_dir": "/c"}'
rung
expect '{"cache": {"dir": "/c"}}'
check "empty cache: dir inserted, last-member comma removed" same_file "${CFG}" "${TK_ROOT}/expected"

fresh equal
put '{
  "cache": {"dir": "/c"},
  "cache_dir": "/c"
}
'
rung
expect '{
  "cache": {"dir": "/c"}
}
'
check "equal cache.dir: exit 0" eq "$RC" 0
check "equal cache.dir: only cache_dir removed" same_file "${CFG}" "${TK_ROOT}/expected"

fresh differ
put '{"cache_dir": "/one", "cache": {"dir": "/two"}}'
cp "${CFG}" "${TK_ROOT}/orig"
BEFORE="$(listing)"
rung
check "different cache.dir: exit 1" eq "$RC" 1
check "different cache.dir: names cache_dir value" contains "${ERR}" '"/one"'
check "different cache.dir: names cache.dir value" contains "${ERR}" '"/two"'
check "different cache.dir: fail marker" contains "${ERR}" '!! v0_1_to_v0_1_cache_block:'
check "different cache.dir: file unchanged" same_file "${CFG}" "${TK_ROOT}/orig"
check "different cache.dir: directory unchanged" eq "$(listing)" "${BEFORE}"

fresh jsonc
put '{
  /* "cache_dir": 1 */
  "url": "http://x//y \"q\"", // "cache_dir": 2
  "cache_dir": "/a\"b//c" /* after */,
  "profiles": {"p": {"cache_dir": "/nested"}}
}
'
rung
expect '{
  /* "cache_dir": 1 */
  "url": "http://x//y \"q\"", // "cache_dir": 2
  "cache": {"dir": "/a\"b//c"} /* after */,
  "profiles": {"p": {"cache_dir": "/nested"}}
}
'
check "jsonc: exit 0" eq "$RC" 0
check "jsonc: comments kept, escapes copied, profile key untouched" same_file "${CFG}" "${TK_ROOT}/expected"

refuse() {
    fresh refuse
    put "$2"
    cp "${CFG}" "${TK_ROOT}/orig"
    BEFORE="$(listing)"
    rung
    check "$1: exit 1" eq "$RC" 1
    check "$1: file unchanged" same_file "${CFG}" "${TK_ROOT}/orig"
    check "$1: directory unchanged" eq "$(listing)" "${BEFORE}"
    check "$1: names the path" contains "${ERR}" "${CFG}"
    check "$1: names the reason" contains "${ERR}" "$3"
}
refuse "truncated" '{"cache_dir": "/c",' "truncated document"
refuse "unclosed string" '{"cache_dir": "/c}' "unclosed string"
refuse "unclosed block comment" '{"cache_dir": "/c" /*' "unclosed block comment"
refuse "two top-level objects" '{"cache_dir": "/c"}{}' "trailing content"
refuse "top-level array" '[{"cache_dir": "/c"}]' "not an object"
refuse "non-string cache_dir" '{"cache_dir": 5}' "cache_dir is a number"
refuse "non-object cache" '{"cache_dir": "/c", "cache": "x"}' "cache is a string"
check "unparseable: message points at doctor" contains "$(fresh p; put '{'; rung; printf '%s' "${ERR}")" "not valid JSON/JSONC (truncated document at byte 1) — not rewritten; fix it by hand or run 'umbree doctor'"

fresh probebad
put '{'
rung --applies
check "probe: unparseable answers 0 so perform refuses loudly" eq "$RC" 0
rm -f "${CFG}"
rung --applies
check "probe: no config answers 1" eq "$RC" 1
rung
check "perform: no config is exit 0" eq "$RC" 0
check "perform: no config writes nothing" eq "$(listing)" ""
rung --bogus
check "unknown argument: 64" eq "$RC" 64
rung --applies extra
check "extra argument: 64" eq "$RC" 64
HOME_SAVE="$H"; H=relative
rung
check "relative HOME: exit 1" eq "$RC" 1
H="${HOME_SAVE}"

fresh clobber
put '{"cache_dir": "/c"}'
cp "${CFG}" "${TK_ROOT}/orig"
printf '#!/bin/sh\necho 20261015T000000Z\n' > "${SHIM}/date"; chmod 0755 "${SHIM}/date"
printf 'OLD' > "${DIR}/config.json.pre-0.1.4.20261015T000000Z"
RUNG_PATH="${SHIM}:${BASE_PATH}" rung
check "existing backup: exit 1" eq "$RC" 1
check "existing backup: never overwritten" eq "$(cat "${DIR}/config.json.pre-0.1.4.20261015T000000Z")" OLD
check "existing backup: config unchanged" same_file "${CFG}" "${TK_ROOT}/orig"
check "existing backup: no temp left" eq "$(temps)" 0
rm -f "${SHIM}/date"

fresh mvfail
put '{"cache_dir": "/c"}'
cp "${CFG}" "${TK_ROOT}/orig"
printf '#!/bin/sh\nexit 1\n' > "${SHIM}/mv"; chmod 0755 "${SHIM}/mv"
RUNG_PATH="${SHIM}:${BASE_PATH}" rung
check "injected mv failure: exit 1" eq "$RC" 1
check "injected mv failure: config byte-identical" same_file "${CFG}" "${TK_ROOT}/orig"
check "injected mv failure: no temp left" eq "$(temps)" 0
check "injected mv failure: says left as found" contains "${ERR}" "left as found"
rm -f "${SHIM}/mv"

tk_end

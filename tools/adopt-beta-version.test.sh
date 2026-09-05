#!/usr/bin/env bash
# tools/adopt-beta-version.test.sh — cycle close: versions/<comp> adopts
# versions/<comp>.beta (beta.md §5 step 3). Copied from burrowee-git/release
# branch beta-channel-graduation (unmerged as of 2026-09-05). This step is load-bearing and has no other
# guard: if it does not run, the stable release carries a lower version than the
# beta everyone soaked, the tie in beta_channel_pick never fires, and beta hosts
# silently never graduate. Every failure mode below is therefore a hard error,
# never a warning.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail_count=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail_count=1; fi; }

ROOT="$(mktemp -d)"; trap 'rm -rf "$ROOT"' EXIT INT TERM
mkdir -p "$ROOT/versions"

run() { ( cd "$ROOT" && REPO_ROOT="$ROOT" bash "${HERE}/adopt-beta-version.sh" "$@" >/dev/null 2>&1; echo $? ); }

printf '0.2.19\n' > "$ROOT/versions/umbree"
printf '0.3.5\n'  > "$ROOT/versions/umbree.beta"
check "adopts the beta version"   "$(run umbree)"                        "0"
check "stable file now equals beta" "$(cat "$ROOT/versions/umbree")"     "0.3.5"
check "beta file is untouched"      "$(cat "$ROOT/versions/umbree.beta")" "0.3.5"

check "idempotent second run"     "$(run umbree)"                        "0"
check "still equal"               "$(cat "$ROOT/versions/umbree")"       "0.3.5"

rm -f "$ROOT/versions/umbreed.beta"
printf '0.2.15\n' > "$ROOT/versions/umbreed"
check "no beta file → refuses"    "$(run umbreed)"                     "1"
check "stable left alone"         "$(cat "$ROOT/versions/umbreed")"    "0.2.15"

check "no component → usage"      "$(run)"                             "2"
check "extra argument → usage"    "$(run umbree umbreed)"                "2"

printf 'not-a-version\n' > "$ROOT/versions/other.beta"
printf '0.1.0\n' > "$ROOT/versions/other"
check "malformed beta → refuses"  "$(run other)"                       "1"
check "stable left alone"         "$(cat "$ROOT/versions/other")"      "0.1.0"

# A glob-plus-charclass check ("[0-9]*.[0-9]*.[0-9]*" + "*[!0-9.]*") accepts
# both of these: an extra field ("0.3.5.6", rc 0 under a naive X.Y.Z read) and
# a doubled/misplaced dot ("1..2.3") — only a strict ^[0-9]+\.[0-9]+\.[0-9]+$
# anchor rejects them.
printf '0.3.5.6\n' > "$ROOT/versions/fourfield.beta"
printf '0.3.0\n' > "$ROOT/versions/fourfield"
check "4-field beta → refuses"     "$(run fourfield)"                      "1"
check "stable left alone"          "$(cat "$ROOT/versions/fourfield")"     "0.3.0"

printf '1..2.3\n' > "$ROOT/versions/doubledot.beta"
printf '1.0.0\n' > "$ROOT/versions/doubledot"
check "doubled-dot beta → refuses" "$(run doubledot)"                        "1"
check "stable left alone"          "$(cat "$ROOT/versions/doubledot")"       "1.0.0"

# The closing line names umbree's own no-bump rule, not burrowee's --keep-version.
msg="$(cd "$ROOT" && REPO_ROOT="$ROOT" bash "${HERE}/adopt-beta-version.sh" umbree 2>&1)"
case "$msg" in
    *"NO bump flag"*) echo "ok: closing line says to cut stable with no bump flag" ;;
    *) echo "FAIL: closing line does not say 'NO bump flag': $msg"; fail_count=1 ;;
esac
case "$msg" in
    *keep-version*) echo "FAIL: closing line still names --keep-version (umbree has none)"; fail_count=1 ;;
    *) echo "ok: closing line does not name --keep-version" ;;
esac

exit "$fail_count"

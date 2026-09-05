#!/usr/bin/env bash
# tools/channel-pick.test.sh — the beta channel's "newest of beta-or-stable"
# decision (burrowee's docs/specs/2026-08-28-beta-cycle-release-flow-design.md
# §3; the shared beta guideline §6). Copied from burrowee-git/release branch
# beta-channel-graduation (unmerged as of 2026-09-05) with umbree/ tags.
#
# Sources the REAL semver_of/is_semver/version_ge out of bootstrap.template.sh
# rather than restating them, so this tests the composition that ships. If the
# template's comparator changes shape, this test stops sourcing and fails loudly.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail_count=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail_count=1; fi; }

TPL="${HERE}/bootstrap.template.sh"
HELPERS="$(mktemp)"; MOD="$(mktemp)"; TAGS="$(mktemp)"
trap 'rm -f "$HELPERS" "$MOD" "$TAGS"' EXIT INT TERM
for fn in semver_of is_semver version_ge; do
    sed -n "/^${fn}() {/,/^}/p" "$TPL" >> "$HELPERS"
done
# Refuse to run against a template that no longer defines them, rather than
# silently testing nothing.
for fn in semver_of is_semver version_ge; do
    grep -q "^${fn}() {" "$HELPERS" || { echo "FAIL: ${fn} not found in bootstrap.template.sh"; exit 1; }
done
sed -e 's/@brand@/umbree/g' -e 's/@BRAND@/UMBREE/g' "${HERE}/modules/channel-pick.sh" > "$MOD"
# shellcheck disable=SC1090
. "$HELPERS"
# shellcheck disable=SC1090
. "$MOD"

B=umbree/v0.3.5.beta.2026.08.28.abc12345
B4=umbree/v0.3.4.beta.2026.08.20.11112222
S5=umbree/v0.3.5
S6=umbree/v0.3.6
S2=umbree/v0.2.19

check "mid-cycle: beta ahead of stable"      "$(beta_channel_pick "$B" "$S2")"  "$B"
check "at release: tie goes to stable"       "$(beta_channel_pick "$B" "$S5")"  "$S5"
check "patch phase: stable ahead"            "$(beta_channel_pick "$B" "$S6")"  "$S6"
check "stable ahead of an older beta"        "$(beta_channel_pick "$B4" "$S5")" "$S5"
check "no stable tag at all"                 "$(beta_channel_pick "$B" "")"     "$B"
check "no beta tag at all"                   "$(beta_channel_pick "" "$S5")"    "$S5"
check "neither side"                         "$(beta_channel_pick "" "")"       ""
check "malformed stable is not preferred"    "$(beta_channel_pick "$B" "umbree/vNOPE")" "$B"
check "malformed beta loses to real stable"  "$(beta_channel_pick "umbree/vNOPE" "$S5")" "$S5"

BETA_RE="^umbree/v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
STABLE_RE="^umbree/v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$"
cat > "$TAGS" <<EOF
umbree/v0.3.4.beta.2026.08.20.11112222
umbree/v0.3.5.beta.2026.08.28.abc12345
umbree/v0.3.5.2026.09.01.deadbeef
umbree/v0.3.10.2026.09.05.cafebabe
umbreed/v9.9.9.2026.09.05.ffffffff
EOF
check "beta winner by shape"   "$(latest_tag_matching "$BETA_RE" "$TAGS")"   "umbree/v0.3.5.beta.2026.08.28.abc12345"
check "stable winner by shape" "$(latest_tag_matching "$STABLE_RE" "$TAGS")" "umbree/v0.3.10.2026.09.05.cafebabe"
check "no match is empty"      "$(latest_tag_matching "^nothing/" "$TAGS")"    ""

# The catalog answers per channel, so graduating needs its stable answer too.
# Shape-checking is what keeps a channel from leaking; assert the stable answer
# is held to STABLE_TAG_RE and the beta answer to TAG_RE, never interchanged.
catalog_pick() { # <beta_answer> <stable_answer> — mirrors version-resolve's logic
    _cb="$1"; _cs="$2"
    printf '%s\n' "$_cb" | grep -Eq "$BETA_RE"   || _cb=""
    printf '%s\n' "$_cs" | grep -Eq "$STABLE_RE" || _cs=""
    beta_channel_pick "$_cb" "$_cs"
}
check "catalog: stable answer graduates"    "$(catalog_pick "$B" "umbree/v0.3.5.2026.09.01.deadbeef")" "umbree/v0.3.5.2026.09.01.deadbeef"
check "catalog: beta-shaped stable rejected" "$(catalog_pick "$B" "$B")" "$B"
check "catalog: stable-shaped beta rejected" "$(catalog_pick "umbree/v0.3.9.2026.09.09.aaaaaaaa" "umbree/v0.3.5.2026.09.01.deadbeef")" "umbree/v0.3.5.2026.09.01.deadbeef"

exit "$fail_count"

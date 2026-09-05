# Copied VERBATIM below this header from burrowee-git/release, branch beta-channel-graduation
# (UNMERGED into burrowee main as of 2026-09-05), tools/modules/channel-pick.sh. Once burrowee
# merges it, drop these three lines so tools/sync-modules.sh compares shas as for any module.
# module: channel-pick  v1
# needs:  helpers
# since:  2026-08-28
# The beta channel installs the NEWEST of its own channel and stable, so a host
# that soaked a cycle graduates onto the release without ever changing channel —
# the channel flip is what caused a beta fleet to silently migrate to stable
# once already. The two channels keep SEPARATE sorts: `sort -V` does not
# implement semver pre-release ordering (it puts 0.3.0.beta.… ABOVE 0.3.0), so a
# mixed list would pin a beta host to a pre-release forever. Each shape is
# sorted alone, where sort -V is correct, and only the two winners are compared.

# latest_tag_matching <regex> <file> — highest tag in <file> whose shape matches
# <regex>. One shape per call: every tag in the result has the same number of
# dot-separated fields, which is the condition under which `sort -V` is right.
latest_tag_matching() {
    grep -E "$1" < "$2" | sort -V | tail -n1
}

# beta_channel_pick <beta_tag> <stable_tag> — the tag a BETA host installs.
# Compares X.Y.Z only: semver_of truncates at three fields, so it drops a
# .beta.<date>.<sha8> suffix and a beta tag compares equal to the stable tag
# it was cut from. A TIE therefore means "this stable release is the one this
# beta soaked", and it goes to stable — that single case is what graduates the
# fleet at cycle close.
#
# A malformed side must never win, and which side is malformed decides the
# outcome on its own — version_ge alone can't carry that: it fails CLOSED
# whenever EITHER side isn't a well-formed semver, so version_ge(stable, beta)
# returns false both when stable is malformed (beta should win) and when beta
# is malformed (stable should win), and "false" only ever routes to one
# branch. So malformed-ness is checked per side, explicitly, before falling
# back to version_ge for the case both sides are well-formed.
beta_channel_pick() {
    _bcp_beta="$1"
    _bcp_stable="$2"
    if [ -z "$_bcp_stable" ]; then printf '%s' "$_bcp_beta"; return 0; fi
    if [ -z "$_bcp_beta" ]; then printf '%s' "$_bcp_stable"; return 0; fi
    # Strip the "<comp>/" prefix before comparing — semver_of only strips a
    # leading "v", exactly as assert_version_floor does at its call site.
    _bcp_stable_v="${_bcp_stable#*/}"
    _bcp_beta_v="${_bcp_beta#*/}"
    if ! is_semver "$(semver_of "$_bcp_stable_v")"; then
        printf '%s' "$_bcp_beta"; return 0
    fi
    if ! is_semver "$(semver_of "$_bcp_beta_v")"; then
        printf '%s' "$_bcp_stable"; return 0
    fi
    if version_ge "$_bcp_stable_v" "$_bcp_beta_v"; then
        printf '%s' "$_bcp_stable"
    else
        printf '%s' "$_bcp_beta"
    fi
}

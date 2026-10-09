#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3' in: $2"; fail=1;; esac; }
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_not_contains() { case "$2" in *"$3"*) echo "FAIL: $1 — unwanted '$3' in: $2"; fail=1;; *) echo "ok: $1";; esac; }

WORK="$(mktemp -d)"; trap 'rm -rf "${WORK}"' EXIT
STUB="${WORK}/stub"; mkdir -p "${STUB}"
cat > "${STUB}/gh" <<'EOF'
#!/usr/bin/env bash
# Fake gh: "api …/releases" and "api …/matching-refs/tags/" both print the
# fixed tag fixture (as ref names for the latter); anything else no-ops.
echo "gh $*" >> "${GH_STUB_LOG:-/dev/null}"
case "$1 $2" in
  "api repos/"*"/releases") cat "${GH_STUB_TAGS}" ;;
  "api repos/"*"/git/matching-refs/tags/") sed 's#^#refs/tags/#' "${GH_STUB_TAGS}" ;;
  *) exit 0 ;;
esac
EOF
chmod +x "${STUB}/gh"

cat > "${WORK}/tags" <<'EOF'
umbree/v0.1.1.2026.06.01.aaaaaaaa
umbree/v0.1.2.2026.06.02.bbbbbbbb
umbree/v0.1.3.2026.06.03.cccccccc
umbree/v0.1.4.2026.06.04.dddddddd
umbree/v0.2.1.beta.2026.07.01.eeeeeeee
umbree/v0.2.2.beta.2026.07.02.ffffffff
umbree/v0.2.3.beta.2026.07.03.11111111
EOF

run() { CHANNEL="$1" KEEP="$2" COMPONENTS=umbree UMBREE_GH="${STUB}/gh" GH_STUB_TAGS="${WORK}/tags" \
  bash "${HERE}/prune-releases.sh"; }
export GH_STUB_LOG="${WORK}/gh.log"

check_refused() {
  local how="$1"; shift
  : > "${GH_STUB_LOG}"
  out_refused="$(env -u KEEP "$@" COMPONENTS=umbree UMBREE_GH="${STUB}/gh" GH_STUB_TAGS="${WORK}/tags" \
    bash "${HERE}/prune-releases.sh" --execute 2>&1)" && rc_refused=0 || rc_refused=$?
  check_contains "stable channel refused, nothing deleted: ${how} says why" "${out_refused}" "stable tags are never deleted"
  check "stable channel refused, nothing deleted: ${how} exits 2" "${rc_refused}" "2"
  check "stable channel refused, nothing deleted: ${how} never ran gh" "$(cat "${GH_STUB_LOG}")" ""
}
check_refused "CHANNEL=stable" CHANNEL=stable
check_refused "CHANNEL=stable KEEP=2" CHANNEL=stable KEEP=2
check_refused "CHANNEL unset" -u CHANNEL
check_not_contains "stable channel refused, nothing deleted: no deletion line" "${out_refused}" "✓ deleted"

out_beta="$(run beta 2)"
check_contains  "beta drops the oldest beta tag" "${out_beta}" "would delete umbree/v0.2.1.beta"
check_not_contains "beta keeps v0.2.2/v0.2.3" "${out_beta}" "would delete umbree/v0.2.2"
check_not_contains "beta never lists a stable tag" "${out_beta}" "would delete umbree/v0.1."

out_beta1="$(run beta 1)"
check_contains  "beta KEEP=1 drops v0.2.1" "${out_beta1}" "would delete umbree/v0.2.1.beta"
check_contains  "beta KEEP=1 drops v0.2.2" "${out_beta1}" "would delete umbree/v0.2.2.beta"
check_not_contains "beta KEEP=1 keeps the newest" "${out_beta1}" "would delete umbree/v0.2.3"
for odd in "umbree/v0.9.9-rc1" "22222222.extra"; do
  check_not_contains "neither-shape tag absent from the beta pass" "${out_beta1}" "${odd}"
done
check_contains "beta KEEP=1 counts exactly three betas" "${out_beta1}" "3 releases → keep newest 1, remove 2"

default_run() { env -u KEEP CHANNEL="$1" COMPONENTS=umbree UMBREE_GH="${STUB}/gh" GH_STUB_TAGS="${WORK}/tags" \
  bash "${HERE}/prune-releases.sh"; }
out_default_beta="$(default_run beta)"
check_contains "beta pass unchanged: beta keeps 1 by default" "${out_default_beta}" "channel=beta  keep=1 "
check_contains "beta pass unchanged: beta drops two of three" "${out_default_beta}" "3 releases → keep newest 1, remove 2"

: > "${GH_STUB_LOG}"
out_exec="$(env -u KEEP CHANNEL=beta COMPONENTS=umbree UMBREE_GH="${STUB}/gh" GH_STUB_TAGS="${WORK}/tags" \
  bash "${HERE}/prune-releases.sh" --execute 2>&1)"
check_contains "beta pass unchanged: --execute lists tag refs" "$(sed -n 1p "${GH_STUB_LOG}")" "gh api repos/umbree-git/release/git/matching-refs/tags/ --paginate"
check "beta pass unchanged: --execute deletes exactly the two old beta refs" "$(grep -c -- '-X DELETE' "${GH_STUB_LOG}")" "2"
check_contains "beta pass unchanged: deletes v0.2.1" "$(cat "${GH_STUB_LOG}")" "gh api -X DELETE repos/umbree-git/release/git/refs/tags/umbree/v0.2.1.beta.2026.07.01.eeeeeeee"
check_contains "beta pass unchanged: deletes v0.2.2" "$(cat "${GH_STUB_LOG}")" "gh api -X DELETE repos/umbree-git/release/git/refs/tags/umbree/v0.2.2.beta.2026.07.02.ffffffff"
check_not_contains "beta pass unchanged: never a release delete" "$(cat "${GH_STUB_LOG}")" "release delete"
check_not_contains "beta pass unchanged: never a stable tag" "$(cat "${GH_STUB_LOG}")" "umbree/v0.1."
check_contains "beta pass unchanged: reports each deletion" "${out_exec}" "✓ deleted umbree/v0.2.2.beta.2026.07.02.ffffffff"

help_out="$(COMPONENTS=umbree UMBREE_GH=/nonexistent bash "${HERE}/prune-releases.sh" --help 2>/dev/null)"; help_rc=$?
check_contains "--help prints the usage on stdout" "${help_out}" "Usage: tools/prune-releases.sh [--execute]"
check_contains "--help names the KEEP default" "${help_out}" "KEEP "
check_contains "--help says stable tags are never deleted" "${help_out}" "stable tags are never deleted"
[ "${help_rc}" = 0 ] && echo "ok: --help exits 0" || { echo "FAIL: --help exited ${help_rc}"; fail=1; }
help_unset="$(env -u COMPONENTS UMBREE_GH=/nonexistent PATH=/usr/bin:/bin bash "${HERE}/prune-releases.sh" -h 2>&1)"; unset_rc=$?
check_contains "-h needs neither rkit nor gh" "${help_unset}" "Usage: tools/prune-releases.sh"
[ "${unset_rc}" = 0 ] && echo "ok: -h exits 0 with nothing resolved" || { echo "FAIL: -h exited ${unset_rc}"; fail=1; }
bad_out="$(COMPONENTS=umbree UMBREE_GH="${STUB}/gh" GH_STUB_TAGS="${WORK}/tags" bash "${HERE}/prune-releases.sh" --bogus 2>&1 >/dev/null)"; bad_rc=$?
check_contains "an unknown argument prints the usage on stderr" "${bad_out}" "Usage: tools/prune-releases.sh"
[ "${bad_rc}" = 2 ] && echo "ok: an unknown argument exits 2" || { echo "FAIL: unknown argument exited ${bad_rc}"; fail=1; }

exit "${fail}"

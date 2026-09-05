#!/usr/bin/env bash
# tools/prune-releases.test.sh — channel-filtered retention: a stable prune
# must never count or delete a beta tag, and vice versa, and a tag matching
# NEITHER shape appears in neither pass. Stubs `gh` on PATH (the script never
# has real network/GitHub access here) so the delete list can be asserted
# directly from DRY-RUN output. Copied from burrowee's, umbree tags.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3' in: $2"; fail=1;; esac; }
check_not_contains() { case "$2" in *"$3"*) echo "FAIL: $1 — unwanted '$3' in: $2"; fail=1;; *) echo "ok: $1";; esac; }

WORK="$(mktemp -d)"; trap 'rm -rf "${WORK}"' EXIT
STUB="${WORK}/stub"; mkdir -p "${STUB}"
cat > "${STUB}/gh" <<'EOF'
#!/usr/bin/env bash
# Fake gh: "api …/releases" and "api …/matching-refs/tags/" both print the
# fixed tag fixture (as ref names for the latter); anything else no-ops.
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

# UMBREE_GH names the stub outright, so a real gh anywhere on PATH is never
# reached; COMPONENTS is set so the script does not need `go run`.
run() { CHANNEL="$1" KEEP="$2" COMPONENTS=umbree UMBREE_GH="${STUB}/gh" GH_STUB_TAGS="${WORK}/tags" \
  bash "${HERE}/prune-releases.sh"; }

out_stable="$(run stable 2)"
check_contains  "stable drops the 2 oldest stable tags" "${out_stable}" "would delete umbree/v0.1.1"
check_contains  "stable drops the 2 oldest stable tags (2nd)" "${out_stable}" "would delete umbree/v0.1.2"
check_not_contains "stable keeps v0.1.3/v0.1.4" "${out_stable}" "would delete umbree/v0.1.3"
check_not_contains "stable never lists a beta tag" "${out_stable}" "beta"

out_beta="$(run beta 2)"
check_contains  "beta drops the oldest beta tag" "${out_beta}" "would delete umbree/v0.2.1.beta"
check_not_contains "beta keeps v0.2.2/v0.2.3" "${out_beta}" "would delete umbree/v0.2.2"
check_not_contains "beta never lists a stable tag" "${out_beta}" "would delete umbree/v0.1."

# A tag matching neither shape is ignored everywhere: never counted (the
# beta pass with KEEP=1 must still keep v0.2.3 and drop exactly the two older
# betas), never deleted.
out_beta1="$(run beta 1)"
check_contains  "beta KEEP=1 drops v0.2.1" "${out_beta1}" "would delete umbree/v0.2.1.beta"
check_contains  "beta KEEP=1 drops v0.2.2" "${out_beta1}" "would delete umbree/v0.2.2.beta"
check_not_contains "beta KEEP=1 keeps the newest" "${out_beta1}" "would delete umbree/v0.2.3"
for odd in "umbree/v0.9.9-rc1" "22222222.extra"; do
  check_not_contains "neither-shape tag absent from the stable pass" "${out_stable}" "${odd}"
  check_not_contains "neither-shape tag absent from the beta pass" "${out_beta1}" "${odd}"
done
check_contains "beta KEEP=1 counts exactly three betas" "${out_beta1}" "3 releases → keep newest 1, remove 2"

exit "${fail}"

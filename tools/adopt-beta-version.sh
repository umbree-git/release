#!/usr/bin/env bash
# tools/adopt-beta-version.sh <comp> — close a beta cycle for one component by
# adopting the version its betas reached.
#
# A cycle's beta cuts climb the patch (release.sh bumps whenever the source
# moved), so the version everyone soaked is versions/<comp>.beta, not the
# X.Y.0 the cycle opened at. The stable release must carry exactly that number:
# it is what makes beta_channel_pick's tie fire and graduate the beta fleet. A
# stable cut at a LOWER version leaves every beta host pinned to a pre-release
# with nothing to tell anyone it happened.
#
# Copied verbatim from burrowee-git/release branch beta-channel-graduation
# (unmerged as of 2026-09-05); only the closing line is umbree's.
#
# Deliberately separate from tools/release.sh: the cut path is not modified.
# After this, cut stable with NO bump flag (umbree has no --keep-version: a
# cut with no bump flag stamps versions/<comp> as it is) so nothing bumps on
# top; then remove versions/<comp>.beta and versions/<comp>.beta.stamp to
# close the cycle (README "Beta channel").
set -euo pipefail
ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

usage() { echo "usage: adopt-beta-version.sh <comp>" >&2; exit 2; }
[ "$#" -eq 1 ] || usage
COMP="$1"
case "$COMP" in ""|*/*|*" "*) usage ;; esac

STABLE_FILE="${ROOT}/versions/${COMP}"
BETA_FILE="${ROOT}/versions/${COMP}.beta"

[ -f "$BETA_FILE" ] || {
    echo "✗ ${BETA_FILE} not found — no open beta cycle for ${COMP} to close" >&2
    exit 1
}
BETA_VERSION="$(tr -d '[:space:]' < "$BETA_FILE")"
# grep -Eq, not a glob/charclass case: `[0-9]*.[0-9]*.[0-9]*` plus a
# `*[!0-9.]*` charclass check both accept "0.3.5.6" (rc 0) and "1..2.3" —
# neither excludes an extra field or a repeated/misplaced dot, since `.` in
# a glob is a literal and `*` swallows any run of digits-and-dots. A strict
# X.Y.Z anchor is the only check that actually rejects those.
printf '%s' "$BETA_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || { echo "✗ ${BETA_FILE} does not hold a version (got '${BETA_VERSION}')" >&2; exit 1; }

printf '%s\n' "$BETA_VERSION" > "$STABLE_FILE"

# Assert rather than trust: this is the step whose silent failure the whole
# graduation depends on.
ADOPTED="$(tr -d '[:space:]' < "$STABLE_FILE")"
[ "$ADOPTED" = "$BETA_VERSION" ] || {
    echo "✗ adoption did not take — versions/${COMP} reads '${ADOPTED}', expected '${BETA_VERSION}'" >&2
    exit 1
}
echo "✓ ${COMP}: versions/${COMP} adopted ${BETA_VERSION} — cut stable with NO bump flag (CHANNEL=\"stable\", FLAGS=\"--public\") so nothing bumps on top"

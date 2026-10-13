#!/usr/bin/env bash
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
printf '%s' "$BETA_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || { echo "✗ ${BETA_FILE} does not hold a version (got '${BETA_VERSION}')" >&2; exit 1; }

printf '%s\n' "$BETA_VERSION" > "$STABLE_FILE"

ADOPTED="$(tr -d '[:space:]' < "$STABLE_FILE")"
[ "$ADOPTED" = "$BETA_VERSION" ] || {
    echo "✗ adoption did not take — versions/${COMP} reads '${ADOPTED}', expected '${BETA_VERSION}'" >&2
    exit 1
}
echo "✓ ${COMP}: versions/${COMP} adopted ${BETA_VERSION} — cut stable with NO bump flag (CHANNEL=\"stable\", FLAGS=\"--public\") so nothing bumps on top"

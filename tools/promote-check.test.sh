#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="${REPO_ROOT}/tools/promote-check.sh"

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf '\n✗ FAILED: %s\n' "$*" >&2; exit 1; }
has() { case "$2" in *"$1"*) return 0 ;; *) return 1 ;; esac; }

command -v python3 >/dev/null || die "python3 is required"
[ -x "${CHECK}" ] || die "not executable: ${CHECK}"

W="$(mktemp -d)"
SERVER_PID=""
cleanup() { [ -n "${SERVER_PID}" ] && kill "${SERVER_PID}" 2>/dev/null || true; rm -rf "${W}"; }
trap cleanup EXIT

SRV="${W}/srv"
mkdir -p "${SRV}/umbreed/beta" "${SRV}/umbree"

manifest() {
    cat > "$1/latest.json" <<JSON
{
  "component": "$2",
  "minisig": "$3/SHA256SUMS.txt.minisig",
  "path": "$3",
  "sha256sums": "$3/SHA256SUMS.txt",
  "stamp": "$5",
  "updated": "2026-09-11T09:00:00Z",
  "version": "$4",
  "zips": ["$2-darwin-arm64.zip"]
}
JSON
}
manifest "${SRV}/umbreed/beta" umbreed "umbreed/beta/v0.3.9.beta.2026.09.11.abcd1234" 0.3.9 v0.3.9.beta.2026.09.11.abcd1234
manifest "${SRV}/umbree"       umbree  "umbree/v0.3.13.2026.09.11.beefcafe"          0.3.13 v0.3.13.2026.09.11.beefcafe

PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
( cd "${SRV}" && exec python3 -m http.server "${PORT}" --bind 127.0.0.1 ) >/dev/null 2>&1 &
SERVER_PID=$!
disown "${SERVER_PID}" 2>/dev/null || true
for _ in $(seq 1 50); do
    curl -fsS "http://127.0.0.1:${PORT}/umbree/latest.json" -o /dev/null 2>/dev/null && break
    sleep 0.1
done

export UMBREE_R2_DOWNLOADS_BASE="http://127.0.0.1:${PORT}"
export UMBREE_CHECK_ALLOW_HTTP=1

run() { set +e; OUT="$("${CHECK}" "$@" 2>&1)"; RC=$?; set -e; }

say "the expected version is live -> 0, and the line is quotable"
run umbreed beta --expect 0.3.9
[ "${RC}" -eq 0 ] || die "expected 0, got ${RC}: ${OUT}"
has "umbreed beta 0.3.9 v0.3.9.beta.2026.09.11.abcd1234 2026-09-11T09:00:00Z" "${OUT}" \
    || die "output is not the one-line answer: ${OUT}"

say "stable resolves <comp>/latest.json, not the beta twin"
run umbree stable --expect 0.3.13
[ "${RC}" -eq 0 ] || die "expected 0, got ${RC}: ${OUT}"
has "umbree stable 0.3.13" "${OUT}" || die "wrong manifest read: ${OUT}"

say "no --expect reports what is live -> 0"
run umbree stable
[ "${RC}" -eq 0 ] || die "expected 0, got ${RC}: ${OUT}"
has "umbree stable 0.3.13" "${OUT}" || die "did not report the live version: ${OUT}"

say "a single version field holding the whole stamp reads as both"
printf '{"version":"v0.3.13.2026.09.11.beefcafe","sums":"umbree/v0.3.13.2026.09.11.beefcafe/SHA256SUMS.txt"}\n' umbree \
    > "${SRV}/umbree/latest.json"
run umbree stable --expect 0.3.13
[ "${RC}" -eq 0 ] || die "expected 0 for the stamp-only manifest shape, got ${RC}: ${OUT}"
has "umbree stable 0.3.13 v0.3.13.2026.09.11.beefcafe" "${OUT}" \
    || die "did not split the stamp into version and stamp: ${OUT}"

say "--expect also takes the full stamp, the spelling a cut report hands over"
run umbree stable --expect v0.3.13.2026.09.11.beefcafe
[ "${RC}" -eq 0 ] || die "expected 0 for the stamp spelling, got ${RC}: ${OUT}"

say "the stamp-only shape still answers not-yet, not cannot-determine"
run umbree stable --expect 0.3.14
[ "${RC}" -eq 1 ] || die "expected 1 (not yet), got ${RC}: ${OUT}"

say "the manifest names an OLDER version -> 1, not yet"
run umbreed beta --expect 0.3.10
[ "${RC}" -eq 1 ] || die "expected 1 (not yet), got ${RC}: ${OUT}"
has "not yet" "${OUT}" || die "did not say not yet: ${OUT}"

say "the manifest names a NEWER version -> 3, cannot tell — not 1"
run umbreed beta --expect 0.3.8
[ "${RC}" -eq 3 ] || die "expected 3 (cannot determine), got ${RC}: ${OUT}"
has "NEWER" "${OUT}" || die "did not name the newer manifest: ${OUT}"

say "a version that is not comparable -> 3, never a guess"
run umbreed beta --expect 0.3.9-rc1
[ "${RC}" -eq 3 ] || die "expected 3, got ${RC}: ${OUT}"

say "no manifest on the channel -> 3, because absent and misspelled look alike"
run umbree beta --expect 0.3.13
[ "${RC}" -eq 3 ] || die "expected 3, got ${RC}: ${OUT}"
has "no manifest" "${OUT}" || die "did not name the missing manifest: ${OUT}"

say "a manifest with no version -> 3"
mkdir -p "${SRV}/umbree/beta"
printf '{ "component": "umbree" }\n' > "${SRV}/umbree/beta/latest.json"
run umbree beta --expect 0.3.13
[ "${RC}" -eq 3 ] || die "expected 3 on a manifest with no version, got ${RC}: ${OUT}"
rm -rf "${SRV}/umbree/beta"

say "a component with no versions/ file is a usage error, not a guess"
run nosuch beta --expect 0.3.22
[ "${RC}" -eq 2 ] || die "expected 2 for an unknown component, got ${RC}: ${OUT}"
has "versions/nosuch" "${OUT}" || die "did not say where the component set comes from: ${OUT}"

say "usage errors are 2, and every one of them"
for bad in "" "umbreed" "umbreed nightly" "nosuch beta" "umbreed beta --expect" "umbreed beta --nope" "umbreed beta extra"; do
    # shellcheck disable=SC2086
    run ${bad}
    [ "${RC}" -eq 2 ] || die "expected 2 for [${bad}], got ${RC}: ${OUT}"
done

say "a plaintext base is refused unless the test hook says otherwise"
set +e; OUT="$(UMBREE_CHECK_ALLOW_HTTP= "${CHECK}" umbreed beta --expect 0.3.9 2>&1)"; RC=$?; set -e
[ "${RC}" -eq 2 ] || die "expected 2 for an http base, got ${RC}: ${OUT}"
has "https" "${OUT}" || die "did not explain why the base was refused: ${OUT}"

say "explicit help is stdout and exit 0, with the exits spelled out"
set +e; HELP_OUT="$("${CHECK}" --help 2>/dev/null)"; RC=$?; HELP_ERR="$("${CHECK}" -h 2>&1 >/dev/null)"; set -e
[ "${RC}" -eq 0 ] || die "expected 0 for --help, got ${RC}"
[ -z "${HELP_ERR}" ] || die "help wrote to stderr: ${HELP_ERR}"
has "Usage:" "${HELP_OUT}" || die "help has no usage line: ${HELP_OUT}"
has "3  cannot determine" "${HELP_OUT}" || die "help does not name exit 3: ${HELP_OUT}"
has "registers a staged row with the manage service" "${HELP_OUT}" || die "help does not describe this brand's cut and promote: ${HELP_OUT}"
has "record-promoted.sh" "${HELP_OUT}" || die "help does not name the step after a live answer: ${HELP_OUT}"

say "the check writes nothing and needs no credentials"
if grep -qE '\b(aws|rclone|scp|ssh)\b' "${CHECK}"; then die "the check reached for a credentialed tool"; fi
if grep -qE '(-X *(PUT|POST|DELETE)|--upload-file)' "${CHECK}"; then die "the check names a write verb"; fi

printf '\n✓ promote-check.sh: all checks passed\n'

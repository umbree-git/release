#!/usr/bin/env bash
# test-e2e.sh — prove the whole umbree release chain OFFLINE with the TEST key.
#
# No GitHub, no nsm, no real signing key. For the single umbree component this:
#   1. dry-run-builds the release via `rkit build` (signed by the TEST key) into
#      dist/<stamp>/ — offline (--no-vulncheck; the real CVE gate is proven in
#      Task 10).
#   2. regenerates the outer bootstrap (baking the TEST pubkey).
#   3. runs verify-no-env on the freshly built binary.
#   4. HAPPY PATH: serves dist/<stamp>/ over http and runs the REAL outer
#      bootstrap (umbree/install.sh) against it (UMBREE_DL_BASE + PREFIX);
#      asserts the installed umbree reports the expected stamp. The burrowee-cli
#      dependency step is exercised but tolerant — it's already installed on this
#      box, or skipped if release.burrowee.com is unreachable.
#   5. TAMPER PATH: flips one byte inside the served zip and asserts the outer
#      bootstrap's verification gate ABORTS non-zero AND installs nothing.
#
# Exits 0 only if the component prints "HAPPY-PATH OK" and "TAMPER-ABORTED OK".
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# go on PATH (the per-dir hook can strip /opt/homebrew/bin) --------------------
GO_BIN="${GO_BIN:-go}"
command -v "${GO_BIN}" >/dev/null 2>&1 || GO_BIN=/opt/homebrew/bin/go
export GO_BIN

# component source dir — build from the main checkout -------------------------
export UMBREE_SRC_UMBREE="${UMBREE_SRC_UMBREE:-/Volumes/MacintoshED/Workstation/Coding/Umbree/cli/code/cli}"

WHAT="${1:-umbree}"
case "${WHAT}" in
    umbree) ;;
    -h|--help) sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "✗ usage: test-e2e.sh umbree" >&2; exit 2 ;;
esac

PORT="${E2E_PORT:-8741}"

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf '\n✗ E2E FAILED: %s\n' "$*" >&2; exit 1; }

# minisign / sha256 verifiers must be present — the outer bootstrap requires them.
command -v minisign >/dev/null 2>&1 || die "minisign not found (brew install minisign)"
command -v python3  >/dev/null 2>&1 || die "python3 not found (needed for the local http server + byte-flip)"
if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
    die "neither shasum nor sha256sum found"
fi
TEST_PUB="${REPO_ROOT}/tools/testkeys/test.pub"
[ -f "${TEST_PUB}" ] || die "TEST pubkey missing: ${TEST_PUB} (minisign -G -p tools/testkeys/test.pub -s tools/testkeys/test.key)"

# host os/arch (the zip the bootstrap requests on this box)
case "$(uname -s)" in Darwin) OS=darwin ;; Linux) OS=linux ;; *) die "unsupported OS $(uname -s)" ;; esac
case "$(uname -m)" in arm64|aarch64) ARCH=arm64 ;; x86_64|amd64) ARCH=amd64 ;; *) die "unsupported arch $(uname -m)" ;; esac

SERVER_PID=""
cleanup() { [ -n "${SERVER_PID}" ] && kill "${SERVER_PID}" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# render the outer bootstrap once (TEST pubkey) -------------------------------
# The bootstrap bakes the TEST pubkey so it verifies against the TEST key that
# `rkit build --dry-run` signs SHA256SUMS.txt with.
say "gen-bootstraps.sh (bake TEST pubkey)"
UMBREE_PUBKEY_FILE="${TEST_PUB}" bash tools/gen-bootstraps.sh

run_component() {
    local comp="$1" stamp serve_dir zip pin

    say "rkit build ${comp} --dry-run --no-vulncheck (TEST-key signed, offline)"
    "${GO_BIN}" run ./cmd/rkit build --component "${comp}" --dry-run --no-vulncheck

    stamp="$(SRC_DIR="${UMBREE_SRC_UMBREE}" bash tools/version.sh "${comp}" --stamp)"
    serve_dir="${REPO_ROOT}/dist/${stamp}"
    [ -d "${serve_dir}" ] || die "expected dist dir not found: ${serve_dir}"
    pin="${comp}/${stamp}"
    zip="${comp}-${OS}-${ARCH}.zip"
    [ -f "${serve_dir}/${zip}" ] || die "host zip not present: ${serve_dir}/${zip}"
    say "${comp} stamp = ${stamp}  (pin = ${pin})"

    # ---- verify-no-env on the freshly built binary (unzip a copy) -----------
    local envchk; envchk="$(mktemp -d)"
    unzip -q -o "${serve_dir}/${zip}" -d "${envchk}"
    "${REPO_ROOT}/tools/verify-no-env.sh" "${envchk}/${comp}"
    rm -rf "${envchk}"
    echo "ENV-GUARD OK (${comp})"

    run_umbree "${comp}" "${serve_dir}" "${zip}" "${stamp}" "${pin}"
}

# ----- umbree: real outer bootstrap against a local http server -------------
run_umbree() {
    local comp="$1" serve_dir="$2" zip="$3" stamp="$4" pin="$5"
    local happy="${TMPDIR:-/tmp}/e2e-${comp}-prefix" tamper="${TMPDIR:-/tmp}/e2e-${comp}-prefix-tamper"
    rm -rf "${happy}" "${tamper}"

    say "serving ${serve_dir} on 127.0.0.1:${PORT}"
    ( cd "${serve_dir}" && exec python3 -m http.server "${PORT}" --bind 127.0.0.1 ) >/dev/null 2>&1 &
    SERVER_PID=$!
    local i=0
    until curl -fsS "http://127.0.0.1:${PORT}/${zip}" -o /dev/null 2>/dev/null; do
        i=$((i+1)); [ "${i}" -lt 50 ] || die "http server did not come up on ${PORT}"
        sleep 0.1
    done
    say "server up (serving ${zip})"

    local dl_base="http://127.0.0.1:${PORT}"
    run_install() {
        UMBREE_DL_BASE="${dl_base}" \
        UMBREE_VERSION="${pin}" \
        PREFIX="$1" \
            sh "${REPO_ROOT}/${comp}/install.sh"
    }

    say "HAPPY PATH — install into ${happy}"
    run_install "${happy}" || die "happy-path install exited non-zero (expected success)"
    local bin="${happy}/bin/${comp}"
    [ -x "${bin}" ] || die "${comp} not installed at ${bin}"
    local got; got="$("${bin}" --version 2>&1 || true)"
    say "installed ${comp} version → ${got}"
    case "${got}" in
        *"${stamp}"*) printf '\nHAPPY-PATH OK (%s)\n' "${comp}" ;;
        *) die "version mismatch: expected stamp '${stamp}' in output, got: ${got}" ;;
    esac

    say "TAMPER PATH — flip one byte inside the served ${zip}"
    local zip_path="${serve_dir}/${zip}" backup="${serve_dir}/${zip}.orig"
    cp "${zip_path}" "${backup}"
    python3 - "${zip_path}" <<'PY'
import sys
p = sys.argv[1]; off = 256
with open(p, "r+b") as f:
    f.seek(off); b = f.read(1)
    if not b: raise SystemExit("zip too small to tamper at offset %d" % off)
    f.seek(off); f.write(bytes([b[0] ^ 0xFF]))
print("flipped byte at offset %d (0x%02x -> 0x%02x)" % (off, b[0], b[0] ^ 0xFF))
PY
    say "TAMPER PATH — rerun the SAME install into ${tamper} (must abort)"
    set +e
    run_install "${tamper}"
    local rc=$?
    set -e
    mv -f "${backup}" "${zip_path}"
    [ "${rc}" -ne 0 ] || die "tampered install returned 0 — verification gate FAILED to abort"
    [ ! -e "${tamper}/bin/${comp}" ] || die "tampered install left a binary — must install nothing"
    say "tampered install aborted with rc=${rc} and installed nothing"
    printf '\nTAMPER-ABORTED OK (%s)\n' "${comp}"

    kill "${SERVER_PID}" 2>/dev/null || true; SERVER_PID=""
}

run_component "${WHAT}"

printf '\n✓ E2E PASSED (%s) — happy path + tamper-abort\n' "${WHAT}"

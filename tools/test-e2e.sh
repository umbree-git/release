#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

GO_BIN="${GO_BIN:-go}"
command -v "${GO_BIN}" >/dev/null 2>&1 || GO_BIN=/opt/homebrew/bin/go
export GO_BIN

src_for() {
    case "$1" in
        umbree)
            : "${UMBREE_SRC_UMBREE:?set UMBREE_SRC_UMBREE to the component source worktree (the cli checkout that ships cmd/umbree)}"
            printf '%s' "${UMBREE_SRC_UMBREE}"
            ;;
        umbreed)
            : "${UMBREE_SRC_UMBREED:?set UMBREE_SRC_UMBREED to the component source worktree (the daemon checkout that ships cmd/umbreed)}"
            printf '%s' "${UMBREE_SRC_UMBREED}"
            ;;
    esac
}

usage() {
    cat <<'EOF'
Usage: tools/test-e2e.sh <umbree|umbreed|manifest>

Prove the whole umbree release chain OFFLINE with the TEST key. No GitHub, no
release host, no real signing key. For the given component this:
  1. dry-run-builds the release via `rkit build` (signed by the TEST key) into
     dist/<stamp>/, offline (--no-vulncheck).
  2. regenerates the outer bootstrap (baking the TEST pubkey).
  3. runs verify-no-env on the freshly built binary.
  4. HAPPY PATH: serves dist/<stamp>/ over http and runs the outer bootstrap
     against it; asserts the installed binary reports the expected stamp.
  5. TAMPER PATH: flips one byte inside the served zip and asserts the outer
     bootstrap's verification gate aborts non-zero and installs nothing.

It never installs a system service: umbreed runs with UMBREED_NO_SERVICE=1.

`manifest` needs no build and no component source: it signs fixture releases
with throwaway keys, serves them and their latest.json from a loopback server,
and drives a --test-build bootstrap through resolution, pins, the signature and
checksum chain and redirects; a committed bootstrap is run only to prove it
refuses an empty or non-https base before any fetch.
EOF
}

WHAT="${1:-umbree}"
case "${WHAT}" in
    umbree|umbreed|manifest) ;;
    -h|--help) usage; exit 0 ;;
    *) { echo "✗ unknown argument: ${WHAT}"; echo; usage; } >&2; exit 2 ;;
esac

PORT="${E2E_PORT:-8741}"

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf '\n✗ E2E FAILED: %s\n' "$*" >&2; exit 1; }

command -v minisign >/dev/null 2>&1 || die "minisign not found (brew install minisign)"
command -v python3  >/dev/null 2>&1 || die "python3 not found (needed for the local http server + byte-flip)"
if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
    die "neither shasum nor sha256sum found"
fi
TEST_PUB="${REPO_ROOT}/tools/testkeys/test.pub"
[ -f "${TEST_PUB}" ] || die "TEST pubkey missing: ${TEST_PUB} (minisign -G -p tools/testkeys/test.pub -s tools/testkeys/test.key)"

case "$(uname -s)" in Darwin) OS=darwin ;; Linux) OS=linux ;; *) die "unsupported OS $(uname -s)" ;; esac
case "$(uname -m)" in arm64|aarch64) ARCH=arm64 ;; x86_64|amd64) ARCH=amd64 ;; *) die "unsupported arch $(uname -m)" ;; esac

M_FAIL=0
mcheck() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; M_FAIL=1; fi; }
mcontains() { case "$2" in *"$3"*) echo "ok: $1" ;; *) echo "FAIL: $1 — missing '$3' in: $2"; M_FAIL=1 ;; esac; }
mlacks() { case "$2" in *"$3"*) echo "FAIL: $1 — unwanted '$3' in: $2"; M_FAIL=1 ;; *) echo "ok: $1" ;; esac; }

M_FLOOR=v0.5.0.2026.01.01.aaaaaaaa
M_NEW=v0.6.0.2026.02.02.bbbbbbbb
M_OLD=v0.4.0.2026.01.15.cccccccc
M_WRONGKEY=v0.6.1.2026.02.03.dddddddd
M_TAMPZIP=v0.6.2.2026.02.04.eeeeeeee
M_TAMPSUMS=v0.6.3.2026.02.05.ffffffff

manifest_server() {
    cat > "${M}/server.py" <<'PY'
import functools, http.server, os, sys
root, log, redirects, portfile = sys.argv[1:5]
class Handler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        with open(log, "a") as f:
            f.write(self.path + "\n")
        if os.path.exists(redirects):
            for line in open(redirects):
                parts = line.split()
                if len(parts) == 2 and parts[0] == self.path:
                    dst = parts[1]
                    self.send_response(302)
                    self.send_header("Location", dst)
                    self.end_headers()
                    return
        super().do_GET()
    def log_message(self, *args):
        pass
server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(Handler, directory=root))
with open(portfile + ".tmp", "w") as f:
    f.write(str(server.server_address[1]))
os.rename(portfile + ".tmp", portfile)
server.serve_forever()
PY
    python3 "${M}/server.py" "${M}/srv" "${M}/requests.log" "${M}/redirects" "${M}/port" >/dev/null 2>&1 &
    SERVER_PID=$!
    local i=0
    until [ -s "${M}/port" ]; do
        i=$((i+1)); [ "${i}" -lt 100 ] || die "fixture server did not start"
        sleep 0.1
    done
    M_PORT="$(cat "${M}/port")"
    M_BASE="http://127.0.0.1:${M_PORT}"
    M_DEAD_PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()')"
}

manifest_release() {
    local stamp="$1" key="$2" dir="${M}/srv/umbree/$1" zip="umbree-${OS}-${ARCH}.zip"
    mkdir -p "${dir}" "${M}/inner/${stamp}"
    printf '#!/bin/sh\nset -eu\nmkdir -p "$PREFIX/bin"\nprintf %s > "$PREFIX/bin/umbree"\nchmod +x "$PREFIX/bin/umbree"\n' \
        "'#!/bin/sh\necho \"umbree ${stamp}\"\n'" > "${M}/inner/${stamp}/install.sh"
    python3 -c 'import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    z.write(sys.argv[2], "install.sh")' "${dir}/${zip}" "${M}/inner/${stamp}/install.sh"
    ( cd "${dir}" && sha256_line "${zip}" > SHA256SUMS.txt )
    minisign -S -s "${key}" -m "${dir}/SHA256SUMS.txt" -x "${dir}/SHA256SUMS.txt.minisig" >/dev/null
}

sha256_line() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi
}

flip_byte() {
    python3 -c 'import sys
p = sys.argv[1]
with open(p, "r+b") as f:
    f.seek(64); b = f.read(1); f.seek(64); f.write(bytes([b[0] ^ 0xFF]))' "$1"
}

manifest_fixture() {
    mkdir -p "${M}/srv/umbree" "${M}/srv/alt" "${M}/tmp" "${M}/home" "${M}/gostub" "${M}/curlstub" "${M}/build"
    minisign -G -W -p "${M}/a.pub" -s "${M}/a.key" >/dev/null
    minisign -G -W -p "${M}/b.pub" -s "${M}/b.key" >/dev/null
    local zip="umbree-${OS}-${ARCH}.zip"
    manifest_release "${M_NEW}" "${M}/a.key"
    manifest_release "${M_OLD}" "${M}/a.key"
    manifest_release "${M_WRONGKEY}" "${M}/b.key"
    manifest_release "${M_TAMPZIP}" "${M}/a.key"
    flip_byte "${M}/srv/umbree/${M_TAMPZIP}/${zip}"
    manifest_release "${M_TAMPSUMS}" "${M}/a.key"
    flip_byte "${M}/srv/umbree/${M_TAMPSUMS}/${zip}"
    ( cd "${M}/srv/umbree/${M_TAMPSUMS}" && sha256_line "${zip}" > SHA256SUMS.txt )
    printf '{"stamp":"%s"}\n' "${M_NEW}" > "${M}/srv/alt/latest.json"
    printf '{"stamp":"%s"}\n' "${M_OLD}" > "${M}/srv/alt/old.json"
    printf '#!/bin/sh\n[ "$1" = run ] && [ "$3" = components ] && { echo umbree; exit 0; }\nexit 1\n' > "${M}/gostub/go"
    printf '#!/bin/sh\necho "curl $*" >> "%s/curl.log"\nexit 7\n' "${M}" > "${M}/curlstub/curl"
    chmod +x "${M}/gostub/go" "${M}/curlstub/curl"
    PATH="${M}/gostub:${PATH}" UMBREE_PUBKEY_FILE="${M}/a.pub" UMBREE_MIN_VERSION="${M_FLOOR}" \
        bash "${REPO_ROOT}/tools/gen-bootstraps.sh" --test-build "${M}/build" >/dev/null \
        || die "gen-bootstraps.sh --test-build failed"
    M_TB="${M}/build/umbree/install.sh"
    M_PATH="$(dirname "$(command -v minisign)"):/usr/bin:/bin"
}

set_manifest() { printf '%s\n' "$1" > "${M}/srv/umbree/latest.json"; }
set_redirects() { printf '%s\n' "$@" > "${M}/redirects"; }

boot() {
    local script="$1" name="$2"; shift 2
    : > "${M}/requests.log"; : > "${M}/curl.log"
    rm -rf "${M}/p/${name}"
    B_OUT="$(cd "${M}" && env -i PATH="${B_PATH:-${M_PATH}}" HOME="${M}/home" TMPDIR="${M}/tmp" \
        PREFIX="${M}/p/${name}" "$@" sh "${script}" 2>&1)" && B_RC=0 || B_RC=$?
    B_REQS="$(cat "${M}/requests.log")"
    B_CURLS="$(cat "${M}/curl.log")"
    B_GOT="$("${M}/p/${name}/bin/umbree" 2>/dev/null || true)"
}

tb() { boot "${M_TB}" "$1" UMBREE_DOWNLOADS_BASE="${M_BASE}" UMBREE_TEST_ALLOW_HTTP=1 "${@:2}"; }

only_under() {
    local reqs="$1" prefix="$2" line
    while IFS= read -r line; do
        case "${line}" in "${prefix}"*|/umbree/latest.json) ;; *) printf '%s\n' "${line}" ;; esac
    done <<< "${reqs}"
}

expect_refused() {
    local label="$1" text="$2"
    [ "${B_RC}" != 0 ] && echo "ok: ${label}: exits non-zero" || { echo "FAIL: ${label}: exited 0: ${B_OUT}"; M_FAIL=1; }
    mcontains "${label}: says why" "${B_OUT}" "${text}"
    mcheck "${label}: installs nothing" "${B_GOT}" ""
}

manifest_cases_resolve() {
    local zip="umbree-${OS}-${ARCH}.zip" body
    echo "# resolution reads latest.json and nothing else"
    set_redirects; set_manifest "{\"stamp\":\"${M_NEW}\"}"
    tb happy
    mcheck "stable installs manifest stamp: exit 0" "${B_RC}" "0"
    mcheck "stable installs manifest stamp: the installed build" "${B_GOT}" "umbree ${M_NEW}"
    mcontains "stable installs manifest stamp: read the manifest" "${B_REQS}" "/umbree/latest.json"
    mcontains "stable installs manifest stamp: fetched the zip from <comp>/<stamp>/" "${B_REQS}" "/umbree/${M_NEW}/${zip}"
    mcontains "stable installs manifest stamp: fetched the signature" "${B_REQS}" "/umbree/${M_NEW}/SHA256SUMS.txt.minisig"
    mcheck "stable installs manifest stamp: no other request" "$(only_under "${B_REQS}" "/umbree/${M_NEW}/")" ""
    rm -f "${M}/srv/umbree/latest.json"
    tb missing
    expect_refused "unreachable manifest fails naming url: 404" "${M_BASE}/umbree/latest.json"
    mcheck "unreachable manifest fails naming url: 404 fetched nothing else" "${B_REQS}" "/umbree/latest.json"
    boot "${M_TB}" dead UMBREE_DOWNLOADS_BASE="http://127.0.0.1:${M_DEAD_PORT}" UMBREE_TEST_ALLOW_HTTP=1
    expect_refused "unreachable manifest fails naming url: refused connection" "http://127.0.0.1:${M_DEAD_PORT}/umbree/latest.json"
    for body in 'not json' '{"stamp":"v0.6.0"}' '{"version":"'"${M_NEW}"'"}' \
        '{"stamp":"'"${M_NEW}"'\n../../'"${M_OLD}"'"}' '{"stamp":"'"${M_NEW}"'/../../x"}' '{"stamp":["'"${M_NEW}"'"]}'; do
        set_manifest "${body}"
        tb malformed
        expect_refused "malformed manifest fails: ${body}" "${M_BASE}/umbree/latest.json"
        mcheck "malformed manifest fails: ${body} fetched nothing else" "${B_REQS}" "/umbree/latest.json"
    done
    set_manifest "{\"stamp\":\"${M_OLD}\"}"
    tb below
    expect_refused "manifest below floor refused" "version floor not met"
    mcheck "manifest below floor refused: fetched nothing else" "${B_REQS}" "/umbree/latest.json"
}

manifest_cases_pin() {
    local pin
    echo "# pins"
    set_redirects; set_manifest "{\"stamp\":\"${M_NEW}\"}"
    tb pin UMBREE_VERSION="umbree/${M_OLD}"
    mcheck "pin downloads from base: exit 0" "${B_RC}" "0"
    mcheck "pin downloads from base: the pinned build, under the floor" "${B_GOT}" "umbree ${M_OLD}"
    mlacks "pin downloads from base: the manifest is not read" "${B_REQS}" "latest.json"
    mcheck "pin downloads from base: only <comp>/<stamp>/" "$(only_under "${B_REQS}" "/umbree/${M_OLD}/")" ""
    for pin in "umbree/../../etc/passwd" "umbreed/${M_NEW}" "${M_NEW}" "umbree/${M_NEW}/../${M_OLD}" \
        "umbree/v0.6.0" "$(printf 'umbree/%s\numbree/%s' "${M_NEW}" "${M_OLD}")"; do
        tb badpin UMBREE_VERSION="${pin}"
        expect_refused "bad pin shape refused before fetch: ${pin}" "is not a umbree release tag"
        mcheck "bad pin shape refused before fetch: ${pin} fetched nothing" "${B_REQS}" ""
    done
}

manifest_cases_verify() {
    echo "# the signature and checksum chain"
    set_redirects
    tb tampzip UMBREE_VERSION="umbree/${M_TAMPZIP}"
    expect_refused "tampered artifact refused" "checksum mismatch"
    tb tampsums UMBREE_VERSION="umbree/${M_TAMPSUMS}"
    expect_refused "tampered sums refused" "signature verification failed"
    tb wrongkey UMBREE_VERSION="umbree/${M_WRONGKEY}"
    expect_refused "wrong key refused" "signature verification failed"
    set_manifest "{\"stamp\":\"${M_WRONGKEY}\"}"
    tb tampmanifest
    expect_refused "tampered manifest naming unsigned bytes refused" "signature verification failed"
}

manifest_cases_redirect() {
    local zip="umbree-${OS}-${ARCH}.zip"
    echo "# redirects"
    set_manifest "{\"stamp\":\"${M_NEW}\"}"
    set_redirects "/umbree/latest.json ${M_BASE}/alt/latest.json"
    tb redir-good
    mcheck "redirected manifest is followed: control installs" "${B_GOT}" "umbree ${M_NEW}"
    set_redirects "/umbree/latest.json ${M_BASE}/alt/old.json"
    tb redir-old
    expect_refused "redirected manifest below floor refused" "version floor not met"
    set_redirects "/umbree/${M_NEW}/${zip} ${M_BASE}/umbree/${M_TAMPZIP}/${zip}"
    tb redir-zip
    expect_refused "redirected artifact to tampered bytes refused" "checksum mismatch"
    set_redirects "/umbree/${M_NEW}/SHA256SUMS.txt file:///etc/passwd"
    tb redir-file
    expect_refused "redirect to another scheme refused" "download failed: ${M_BASE}/umbree/${M_NEW}/SHA256SUMS.txt"
    set_redirects
}

manifest_cases_committed() {
    local committed="${REPO_ROOT}/umbree/install.sh" base
    echo "# a committed bootstrap"
    B_PATH="${M}/curlstub:${M_PATH}" boot "${committed}" empty UMBREE_DOWNLOADS_BASE=
    expect_refused "empty downloads base fails" "no download source"
    mcheck "empty downloads base fails: before any fetch" "${B_CURLS}" ""
    for base in "${M_BASE}" "http://example.invalid" "ftp://127.0.0.1:${M_PORT}" "file:///etc"; do
        B_PATH="${M}/curlstub:${M_PATH}" boot "${committed}" http UMBREE_DOWNLOADS_BASE="${base}"
        expect_refused "http base fails in committed bootstrap: ${base}" "must be https://"
        mcheck "http base fails in committed bootstrap: ${base} before any fetch" "${B_CURLS}" ""
    done
    B_PATH="${M}/curlstub:${M_PATH}" boot "${committed}" allow UMBREE_DOWNLOADS_BASE="${M_BASE}" \
        UMBREE_TEST_ALLOW_HTTP=1 ALLOW_LOOPBACK_HTTP=1 UMBREE_DL_BASE="${M_BASE}/umbree/${M_NEW}" DL_BASE="${M_BASE}/umbree/${M_NEW}"
    expect_refused "allow-http ignored by committed bootstrap" "must be https://"
    mcheck "allow-http ignored by committed bootstrap: before any fetch" "${B_CURLS}" ""
    mcheck "allow-http ignored by committed bootstrap: the server saw nothing" "${B_REQS}" ""
    B_PATH="${M}/curlstub:${M_PATH}" boot "${M_TB}" tb-remote UMBREE_DOWNLOADS_BASE="http://example.invalid" UMBREE_TEST_ALLOW_HTTP=1
    expect_refused "a test build allows loopback http only" "must be https://"
    mcheck "a test build allows loopback http only: before any fetch" "${B_CURLS}" ""
}

manifest_cases_nojq() {
    local f
    echo "# without jq"
    mkdir -p "${M}/nojq"
    for f in /usr/bin/* /bin/*; do
        [ "$(basename "${f}")" = jq ] || [ -e "${M}/nojq/$(basename "${f}")" ] || ln -s "${f}" "${M}/nojq/$(basename "${f}")"
    done
    command -v minisign >/dev/null && [ -e "${M}/nojq/minisign" ] || ln -sf "$(command -v minisign)" "${M}/nojq/minisign"
    set_redirects; set_manifest "{\"stamp\":\"${M_NEW}\"}"
    B_PATH="${M}/nojq" tb nojq
    mcheck "stable installs manifest stamp without jq" "${B_GOT}" "umbree ${M_NEW}"
    set_manifest '{"stamp":"'"${M_NEW}"'\n../../'"${M_OLD}"'"}'
    B_PATH="${M}/nojq" tb nojq-bad
    expect_refused "malformed manifest fails without jq" "${M_BASE}/umbree/latest.json"
}

manifest_suite() {
    M="$(mktemp -d)"
    trap 'cleanup; rm -rf "${M}"' EXIT INT TERM
    manifest_fixture
    manifest_server
    manifest_cases_resolve
    manifest_cases_pin
    manifest_cases_verify
    manifest_cases_redirect
    manifest_cases_committed
    manifest_cases_nojq
    echo
    [ "${M_FAIL}" = 0 ] || { echo "TESTS FAILED"; exit 1; }
    echo "ALL OK"
}

SERVER_PID=""
cleanup() { [ -n "${SERVER_PID}" ] && kill "${SERVER_PID}" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

if [ "${WHAT}" = manifest ]; then
    manifest_suite
    exit 0
fi

say "gen-bootstraps.sh (bake TEST pubkey)"
UMBREE_PUBKEY_FILE="${TEST_PUB}" bash tools/gen-bootstraps.sh

run_component() {
    local comp="$1" src var stamp serve_dir zip pin
    src="$(src_for "${comp}")"
    var="UMBREE_SRC_$(printf '%s' "${comp}" | tr '[:lower:]' '[:upper:]')"
    export "${var}=${src}"

    say "rkit build ${comp} --dry-run --no-vulncheck (TEST-key signed, offline)"
    "${GO_BIN}" run ./cmd/rkit build --component "${comp}" --dry-run --no-vulncheck

    stamp="$(SRC_DIR="${src}" bash tools/version.sh "${comp}" --stamp)"
    serve_dir="${REPO_ROOT}/dist/${stamp}"
    [ -d "${serve_dir}" ] || die "expected dist dir not found: ${serve_dir}"
    pin="${comp}/${stamp}"
    zip="${comp}-${OS}-${ARCH}.zip"
    [ -f "${serve_dir}/${zip}" ] || die "host zip not present: ${serve_dir}/${zip}"
    say "${comp} stamp = ${stamp}  (pin = ${pin})"

    local envchk; envchk="$(mktemp -d)"
    unzip -q -o "${serve_dir}/${zip}" -d "${envchk}"
    "${REPO_ROOT}/tools/verify-no-env.sh" "${envchk}/${comp}"
    rm -rf "${envchk}"
    echo "ENV-GUARD OK (${comp})"

    run_umbree "${comp}" "${serve_dir}" "${zip}" "${stamp}" "${pin}"
}

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
        UMBREED_NO_SERVICE=1 \
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

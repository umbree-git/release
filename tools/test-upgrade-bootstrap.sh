#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
W="$(mktemp -d)"
SERVER_PID=""
cleanup() { [ -z "${SERVER_PID}" ] || kill "${SERVER_PID}" 2>/dev/null || true; rm -rf "${W}"; }
trap cleanup EXIT

FAIL=0
pass() { echo "ok: $1"; }
miss() { echo "FAIL: $1"; FAIL=1; }
has() { case "$2" in *"$3"*) pass "$1" ;; *) miss "$1 — missing '$3' in: $2" ;; esac; }
is() { if [ "$2" = "$3" ]; then pass "$1"; else miss "$1 — got '$2' want '$3'"; fi; }

for tool in minisign python3 unzip setsid; do
    command -v "${tool}" >/dev/null 2>&1 || { echo "FAIL: ${tool} not found"; exit 1; }
done
case "$(uname -s)" in Darwin) OS=darwin ;; Linux) OS=linux ;; *) echo "FAIL: unsupported OS"; exit 1 ;; esac
case "$(uname -m)" in arm64|aarch64) ARCH=arm64 ;; x86_64|amd64) ARCH=amd64 ;; *) echo "FAIL: unsupported arch"; exit 1 ;; esac

FLOOR=v0.1.0.2026.01.01.aaaaaaaa
LADDER=v0.2.0.2026.02.02.bbbbbbbb
PLAIN=v0.2.1.2026.02.03.cccccccc
TAMPERED=v0.2.2.2026.02.04.dddddddd

sha256_line() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi; }

kit() {
    local comp="$1" stamp="$2" ladder="$3" dir="${W}/srv/$1/$2" src="${W}/kits/$1-$2"
    mkdir -p "${dir}" "${src}/migrations"
    printf '#!/bin/sh\necho "inner %s %s" >> "%s/log"\n' "${comp}" "${stamp}" "${W}" > "${src}/install.sh"
    if [ "${ladder}" = yes ]; then
        printf '#!/bin/sh\necho "ladder %s $#:$*" >> "%s/log"\n' "${stamp}" "${W}" > "${src}/migrations/upgrade.sh"
    fi
    ( cd "${src}" && python3 -c 'import os, sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    for root, _, files in os.walk("."):
        for f in files:
            z.write(os.path.join(root, f), os.path.relpath(os.path.join(root, f), "."))' "${dir}/${comp}-${OS}-${ARCH}.zip" )
    ( cd "${dir}" && sha256_line "${comp}-${OS}-${ARCH}.zip" > SHA256SUMS.txt )
    minisign -S -s "${W}/a.key" -m "${dir}/SHA256SUMS.txt" -x "${dir}/SHA256SUMS.txt.minisig" >/dev/null
}

serve() {
    printf '%s\n' 'import functools, http.server, os, sys' \
        'root, log, portfile = sys.argv[1:4]' \
        'class H(http.server.SimpleHTTPRequestHandler):' \
        '    def do_GET(self):' \
        '        open(log, "a").write(self.path + "\n")' \
        '        super().do_GET()' \
        '    def log_message(self, *a):' \
        '        pass' \
        's = http.server.ThreadingHTTPServer(("127.0.0.1", 0), functools.partial(H, directory=root))' \
        'open(portfile + ".tmp", "w").write(str(s.server_address[1]))' \
        'os.rename(portfile + ".tmp", portfile)' \
        's.serve_forever()' > "${W}/server.py"
    python3 "${W}/server.py" "${W}/srv" "${W}/requests" "${W}/port" >/dev/null 2>&1 &
    SERVER_PID=$!
    local i=0
    until [ -s "${W}/port" ]; do i=$((i + 1)); [ "${i}" -lt 100 ] || { echo "FAIL: server did not start"; exit 1; }; sleep 0.1; done
    BASE="http://127.0.0.1:$(cat "${W}/port")"
}

fixture() {
    mkdir -p "${W}/srv" "${W}/kits" "${W}/build" "${W}/gostub" "${W}/home" "${W}/tmp"
    minisign -G -W -p "${W}/a.pub" -s "${W}/a.key" >/dev/null
    kit umbree "${LADDER}" yes
    kit umbree "${PLAIN}" no
    kit umbreed "${PLAIN}" no
    kit umbreed "${TAMPERED}" no
    python3 -c 'import sys
p = sys.argv[1]
with open(p, "r+b") as f:
    f.seek(40); b = f.read(1); f.seek(40); f.write(bytes([b[0] ^ 0xFF]))' "${W}/srv/umbreed/${TAMPERED}/umbreed-${OS}-${ARCH}.zip"
    printf '#!/bin/sh\necho umbree\necho umbreed\n' > "${W}/gostub/go"
    printf '#!/bin/sh\necho "umbreed $* stdin=$(readlink /proc/self/fd/0 2>/dev/null || echo unknown) read=$(head -c 64 | tr -d "\\n")" >> "%s/log"\nexit "$(cat "%s/restart-rc" 2>/dev/null || echo 0)"\n' "${W}" "${W}" > "${W}/umbreed"
    chmod +x "${W}/gostub/go" "${W}/umbreed"
    PATH="${W}/gostub:${PATH}" UMBREE_PUBKEY_FILE="${W}/a.pub" UMBREE_MIN_VERSION="${FLOOR}" \
        sh "${REPO_ROOT}/tools/gen-bootstraps.sh" --test-build "${W}/build" >/dev/null
    RUN_PATH="$(dirname "$(command -v minisign)"):/usr/bin:/bin"
    serve
}

boot() {
    local comp="$1" mode="$2" pin="$3"; shift 3
    : > "${W}/log"; : > "${W}/requests"
    OUT="$(cd "${W}" && cat "${W}/build/${comp}/${mode}.sh" | env -i PATH="${RUN_PATH}" HOME="${W}/home" TMPDIR="${W}/tmp" \
        UMBREE_DOWNLOADS_BASE="${BASE}" UMBREE_TEST_ALLOW_HTTP=1 UMBREE_TEST_UMBREED_EXEC="${W}/umbreed" \
        UMBREE_VERSION="${comp}/${pin}" PREFIX="${W}/prefix" setsid -w sh -s -- "$@" 2>&1)" && RC=0 || RC=$?
    LOG="$(cat "${W}/log")"
    REQS="$(cat "${W}/requests")"
}

case_ladder() {
    boot umbree upgrade "${LADDER}" 0.1.9
    is "a ladder kit upgrades cleanly" "${RC}" 0
    is "the inner install runs, then the ladder with the line" "${LOG}" "$(printf 'inner umbree %s\nladder %s 1:0.1.9' "${LADDER}" "${LADDER}")"
    boot umbree upgrade "${LADDER}"
    has "the ladder runs with no line when none is given" "${LOG}" "ladder ${LADDER} 0:"
}

case_umbreed_restart() {
    boot umbreed upgrade "${PLAIN}"
    is "an umbreed kit with no ladder upgrades cleanly" "${RC}" 0
    has "the inner install runs first" "${LOG}" "inner umbreed ${PLAIN}"
    has "umbreed restart runs after it" "${LOG}" "umbreed restart stdin=/dev/null read="
    is "the restart reads nothing from the script pipe" "$(printf '%s\n' "${LOG}" | grep '^umbreed ')" "umbreed restart stdin=/dev/null read="
    is "the install comes before the restart" "$(printf '%s\n' "${LOG}" | head -n1)" "inner umbreed ${PLAIN}"
    has "the fallback says there is no ladder" "${OUT}" "no migration ladder in this umbreed kit — restarted the service"
    echo 1 > "${W}/restart-rc"
    boot umbreed upgrade "${PLAIN}"
    rm -f "${W}/restart-rc"
    is "a failed restart fails the upgrade" "${RC}" 1
    has "a failed restart names the command" "${OUT}" "✗ installed ${PLAIN}, but the service was not restarted — run: sudo /usr/local/umbree/bin/umbreed service restart"
    has "the new binary stays installed" "${LOG}" "inner umbreed ${PLAIN}"
}

case_refusals() {
    boot umbree upgrade "${PLAIN}"
    is "an umbree kit with no ladder is refused" "${RC}" 1
    has "the refusal names the component and the stamp" "${OUT}" "the umbree kit ${PLAIN} ships no migration ladder"
    has "it refuses after installing" "${LOG}" "inner umbree ${PLAIN}"
    boot umbreed upgrade "${PLAIN}" 0.1.9
    is "a line to a ladder-less kit is refused" "${RC}" 1
    has "the refusal names the line" "${OUT}" "the line 0.1.9 has nothing to run"
    is "nothing runs for a refused line" "${LOG}" ""
    boot umbreed upgrade "${TAMPERED}"
    is "a tampered zip is refused" "${RC}" 1
    is "nothing runs after a failed verification" "${LOG}" ""
}

case_arguments() {
    boot umbreed install "${PLAIN}" extra
    is "install.sh refuses an argument" "${RC}" 1
    has "the refusal says why" "${OUT}" "install.sh takes no arguments"
    is "a refused install fetches nothing" "${REQS}" ""
    boot umbreed upgrade "${PLAIN}" 0.1.9 0.2.0
    is "upgrade.sh refuses two arguments" "${RC}" 1
    boot umbreed upgrade "${PLAIN}" latest
    is "upgrade.sh refuses a line that is not X.Y.Z" "${RC}" 1
    is "a refused upgrade fetches nothing" "${REQS}" ""
    boot umbreed install "${PLAIN}"
    is "install.sh still installs" "${RC}" 0
    is "install.sh never restarts" "${LOG}" "inner umbreed ${PLAIN}"
}

fixture
case_ladder
case_umbreed_restart
case_refusals
case_arguments
if [ "${FAIL}" -ne 0 ]; then
    echo "upgrade bootstrap checks FAILED"
    exit 1
fi
echo "all upgrade bootstrap checks passed"

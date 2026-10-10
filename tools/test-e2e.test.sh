#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E="${HERE}/test-e2e.sh"

[ -r "${E2E}" ] || { echo "FAIL: ${E2E} not readable"; exit 1; }

fails=0

echo "# expect: the bootstrap invocation suppresses the service install"
run_install_body="$(awk '/^[[:space:]]*run_install\(\)/,/^[[:space:]]*}/' "${E2E}")"
if [ -z "${run_install_body}" ]; then
    echo "FAIL: could not find run_install() in ${E2E}"
    fails=1
elif ! printf '%s' "${run_install_body}" | grep -q 'UMBREED_NO_SERVICE=1'; then
    echo "FAIL: run_install() does not pass UMBREED_NO_SERVICE=1 — running this"
    echo "      harness for the umbreed component would install and load a real"
    echo "      system boot unit on the host, then orphan it."
    fails=1
else
    echo "run_install passes UMBREED_NO_SERVICE=1"
fi

echo "# expect: the harness never invokes a privileged service command itself"
if sed 's/#.*//' "${E2E}" | grep -nE '(^|[^[:alnum:]_-])(sudo|launchctl|systemctl)([^[:alnum:]_-]|$)'; then
    echo "FAIL: test-e2e.sh invokes a privileged service command directly (above)"
    fails=1
else
    echo "no direct sudo/launchctl/systemctl call"
fi

echo "# expect: --help prints the usage from code, an unknown argument exits 2"
help_out="$(bash "${E2E}" --help 2>/dev/null)" && help_rc=0 || help_rc=$?
case "${help_out}" in
    *"Usage: tools/test-e2e.sh <umbree|umbreed|manifest>"*"UMBREED_NO_SERVICE=1"*) echo "--help prints the usage" ;;
    *) echo "FAIL: --help printed: ${help_out}"; fails=1 ;;
esac
[ "${help_rc}" = 0 ] || { echo "FAIL: --help exited ${help_rc}"; fails=1; }
bad_out="$(bash "${E2E}" --bogus 2>&1 >/dev/null)" && bad_rc=0 || bad_rc=$?
case "${bad_out}" in
    *"Usage: tools/test-e2e.sh"*) echo "an unknown argument prints the usage on stderr" ;;
    *) echo "FAIL: unknown argument printed: ${bad_out}"; fails=1 ;;
esac
[ "${bad_rc}" = 2 ] || { echo "FAIL: unknown argument exited ${bad_rc}"; fails=1; }

echo "# expect: a build mode signs with a throwaway key made for the run, and installs against its public key"
REPO="$(cd "${HERE}/.." && pwd)"
W="$(mktemp -d)"
trap 'rm -rf "${W}"' EXIT
mkdir -p "${W}/stub" "${W}/src"
git -C "${W}/src" init -q
git -C "${W}/src" -c user.name=t -c user.email=t@t commit -q --allow-empty -m seed
cat > "${W}/stub/go" <<'STUB'
#!/bin/sh
if [ "$1" = run ] && [ "$2" = ./cmd/rkit ] && [ "$3" = components ]; then echo umbree; echo umbreed; exit 0; fi
[ "$1" = run ] && [ "$2" = ./cmd/rkit ] && [ "$3" = build ] || { echo "stub go: unexpected $*" >&2; exit 1; }
key=""; comp=""
while [ $# -gt 0 ]; do
    case "$1" in --sign-key) key="$2" ;; --component) comp="$2" ;; esac
    shift
done
echo "sign-key ${key}" >> "${STUB_LOG}"
[ -n "${key}" ] && [ -f "${key}" ] || { echo "stub rkit: no --sign-key file (${key})" >&2; exit 1; }
if [ -n "${STUB_SIGN_OTHER:-}" ]; then
    minisign -G -W -p "${STUB_LOG}.other.pub" -s "${STUB_LOG}.other.key" >/dev/null
    key="${STUB_LOG}.other.key"
fi
stamp="$(SRC_DIR="${UMBREE_SRC_UMBREE}" bash tools/version.sh "${comp}" --stamp)"
case "$(uname -m)" in arm64|aarch64) arch=arm64 ;; *) arch=amd64 ;; esac
case "$(uname -s)" in Darwin) os=darwin ;; *) os=linux ;; esac
d="dist/${stamp}"; z="${comp}-${os}-${arch}.zip"
mkdir -p "${d}/x"
printf '#!/bin/sh\necho "%s %s"\n' "${comp}" "${stamp}" > "${d}/x/${comp}"
printf '#!/bin/sh\nset -e\nmkdir -p "$PREFIX/bin"\ncp %s "$PREFIX/bin/%s"\nchmod 755 "$PREFIX/bin/%s"\n' "${comp}" "${comp}" "${comp}" > "${d}/x/install.sh"
python3 -c 'import sys,zipfile,os
z=zipfile.ZipFile(sys.argv[1],"w")
for n in ("install.sh",sys.argv[3]): z.write(os.path.join(sys.argv[2],n),n)
z.close()' "${d}/${z}" "${d}/x" "${comp}"
rm -rf "${d}/x"
( cd "${d}" && sha256sum "${z}" > SHA256SUMS.txt )
minisign -S -s "${key}" -m "${d}/SHA256SUMS.txt" -x "${d}/SHA256SUMS.txt.minisig" >/dev/null
STUB
chmod +x "${W}/stub/go"
e2e_build() {
    ( cd "${REPO}" && env -u GO_BIN PATH="${W}/stub:${PATH}" STUB_LOG="${W}/calls" UMBREE_SRC_UMBREE="${W}/src" \
        E2E_PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')" \
        "$@" bash "${E2E}" umbree 2>&1 )
}
: > "${W}/calls"
out="$(e2e_build)" && rc=0 || rc=$?
case "${out}" in
    *"HAPPY-PATH OK (umbree)"*"TAMPER-ABORTED OK (umbree)"*"E2E PASSED (umbree)"*) echo "a build mode passes signed by its own key" ;;
    *) echo "FAIL: build mode rc ${rc}: $(printf '%s' "${out}" | tail -n 5)"; fails=1 ;;
esac
key="$(sed -n 's/^sign-key //p' "${W}/calls" | head -n1)"
case "${key}" in
    "") echo "FAIL: rkit build was given no --sign-key"; fails=1 ;;
    "${REPO}"/*) echo "FAIL: the sign key ${key} lives in the repository"; fails=1 ;;
    *) echo "the sign key is outside the repository" ;;
esac
if [ -n "${key}" ] && [ -e "${key}" ]; then echo "FAIL: the run's key ${key} outlived the run"; fails=1; else echo "the run's key is gone after the run"; fi
if [ -e "${REPO}/tools/testkeys/test.key" ]; then echo "FAIL: a private key file exists at tools/testkeys/test.key"; fails=1; else echo "no private key file in the repository"; fi
: > "${W}/calls"
out="$(e2e_build STUB_SIGN_OTHER=1)" && rc=0 || rc=$?
case "${out}" in
    *"HAPPY-PATH OK"*) echo "FAIL: an install signed by another key passed — the bootstrap does not verify against the run's key"; fails=1 ;;
    *) [ "${rc}" -ne 0 ] && echo "a release signed by another key is refused" || { echo "FAIL: rc 0 with another key"; fails=1; } ;;
esac
rm -rf "${REPO}"/dist/v*.*.*.*.*.*."$(git -C "${W}/src" rev-parse --short=8 HEAD)"

[ "${fails}" -eq 0 ] || exit 1
echo "ALL OK"

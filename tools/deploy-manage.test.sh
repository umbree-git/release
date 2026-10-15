#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${REPO_ROOT}/tools/deploy-manage.sh"
VHOST="${REPO_ROOT}/ops/nginx/release.umbree.org.conf"
HELPER=/usr/local/umbree-release/bin/umbree-deploy-helper
VERBS="preflight install-binary render activate restore"

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf '\n✗ FAILED: %s\n' "$*" >&2; exit 1; }
has() { case "$2" in *"$1"*) return 0 ;; *) return 1 ;; esac; }

[ -f "${SCRIPT}" ] || die "missing: ${SCRIPT}"

W="$(mktemp -d)"
trap 'rm -rf "${W}"' EXIT

REPO="${W}/repo"
mkdir -p "${REPO}/tools" "${REPO}/ops" "${REPO}/cmd/umbree-release-manage" "${REPO}/cmd/umbree-deploy-helper"
cp "${SCRIPT}" "${REPO}/tools/deploy-manage.sh"
cp -R "${REPO_ROOT}/ops/systemd" "${REPO_ROOT}/ops/nginx" "${REPO}/ops/"
cp -R "${REPO}/ops" "${W}/ops.committed"

HOST="${W}/host"
mkdir -p "${HOST}/stage"
SHIMS="${W}/shims"; HOSTSHIMS="${W}/hostshims"; mkdir -p "${SHIMS}" "${HOSTSHIMS}"
LOG="${W}/calls.log"; : > "${LOG}"
: > "${W}/all.log"
export SANDBOX_LOG="${LOG}" SANDBOX_HOSTSHIMS="${HOSTSHIMS}" SANDBOX_HELPER="${HELPER}"

cat > "${SHIMS}/go" <<'EOF'
#!/bin/sh
echo "go $*" >> "$SANDBOX_LOG"
case "$1" in
    version) echo "go version ${SANDBOX_GOVERSION:-go1.26.9} linux/amd64"; exit 0 ;;
    env) echo "${SANDBOX_GOVERSION:-go1.26.9}"; exit 0 ;;
esac
out=""; pkg=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && { out="$2"; shift; }; pkg="$1"; shift; done
printf 'fake %s %s\n' "${pkg}" "${SANDBOX_BUILD_TAG:-1}" > "$out"; chmod 0755 "$out"
EOF
cat > "${SHIMS}/scp" <<'EOF'
#!/bin/sh
src=""; dst=""
for a in "$@"; do case "$a" in -q) ;; *:*) echo "scp-dest ${a%%:*}" >> "$SANDBOX_LOG"; dst="${a#*:}" ;; *) src="$a" ;; esac; done
cp "$src" "$dst"; echo "scp $src -> $dst" >> "$SANDBOX_LOG"
EOF
cat > "${SHIMS}/ssh" <<'EOF'
#!/bin/sh
script=""; dest=""; for a in "$@"; do dest="$script"; script="$a"; done
echo "ssh-dest ${dest}" >> "$SANDBOX_LOG"
PATH="$SANDBOX_HOSTSHIMS:$PATH" sh -c "$script"
EOF
cat > "${SHIMS}/curl" <<'EOF'
#!/bin/sh
echo "curl $*" >> "$SANDBOX_LOG"
exit 0
EOF
cat > "${HOSTSHIMS}/curl" <<'EOF'
#!/bin/sh
echo "curl $*" >> "$SANDBOX_LOG"
case "$*" in
    *http_code*/umbree/version.json*) [ "${SANDBOX_JSON_FAIL:-0}" = 1 ] && printf '\n404' || printf '{"component":"umbree","channel":"production","version":"1.0.0","stamp":"s"}\n200' ;;
    *http_code*/manage/login*) [ "${SANDBOX_LOGIN_FAIL:-0}" = 1 ] && printf '502' || printf '200' ;;
    *healthz*) printf 'ok\n' ;;
esac
EOF
cat > "${HOSTSHIMS}/systemctl" <<'EOF'
#!/bin/sh
echo "systemctl $*" >> "$SANDBOX_LOG"
[ "$1" = is-active ] && echo active
exit 0
EOF
cat > "${HOSTSHIMS}/sudo" <<'EOF'
#!/bin/sh
echo "sudo $*" >> "$SANDBOX_LOG"
[ "$1" = -n ] || { echo "sudo: a password is required" >&2; exit 1; }
shift
list=0; [ "$1" = -l ] && { list=1; shift; }
[ "$1" = "$SANDBOX_HELPER" ] || { echo "sudo: $1 is not allowed" >&2; exit 1; }
[ $# -eq 2 ] || { echo "sudo: unexpected argv: $*" >&2; exit 1; }
case " ${SANDBOX_SUDO_DENY:-} " in *" $2 "*) [ "$list" = 1 ] && exit 1; echo "sudo: a password is required" >&2; exit 1 ;; esac
[ "$list" = 1 ] && { echo "$1 $2"; exit 0; }
echo "helper $2" >> "$SANDBOX_LOG"
case " ${SANDBOX_HELPER_FAIL:-} " in *" $2 "*) echo "!! $2: simulated failure" >&2; exit 1 ;; esac
[ "$2" = status ] && [ "${SANDBOX_NO_PREV:-0}" != 1 ] && echo "-> /usr/local/umbree-release/bin/umbree-release-manage: previous copy held"
exit 0
EOF
cat > "${HOSTSHIMS}/install" <<'EOF'
#!/bin/sh
echo "install $*" >> "$SANDBOX_LOG"
[ "$1" = -d ] || exit 1
for d in "$@"; do last="$d"; done; mkdir -p "$last"
EOF
chmod 0755 "${SHIMS}"/* "${HOSTSHIMS}"/*

CFG="${W}/manage-deploy.env"
write_cfg() {
    {
        echo "RELEASE_HOST=sandbox-host"
        echo "SSH_USER=deployer"
        echo "MANAGE_DATA_DIR=/var/lib/umbree-release-manage"
        echo "STATIC_DIR=/srv/static"
        echo "STAGE_DIR=${HOST}/stage"
        echo "MANAGE_URL=https://release.example"
    } > "${CFG}"
}
write_cfg

run() {
    set +e
    OUT="$(env PATH="${SHIMS}:${PATH}" UMBREE_MANAGE_DEPLOY_CONFIG="${CFG}" UMBREE_ICONS=1 \
        "$@" sh "${REPO}/tools/deploy-manage.sh" ${ARGS[@]+"${ARGS[@]}"} 2>&1)"
    RC=$?
    set -e
}
calls() { cat "${LOG}"; }
reset_log() { cat "${LOG}" >> "${W}/all.log"; : > "${LOG}"; }
remote_calls() { grep -E '^(ssh|scp|curl|sudo|helper)' "${LOG}" || true; }
helper_calls() { sed -n 's/^helper //p' "${LOG}" | tr '\n' ' '; }

say "help on stdout, exit 0"
ARGS=(--help); run
[ "${RC}" -eq 0 ] || die "--help exit ${RC}"
has "usage: tools/deploy-manage.sh" "${OUT}" || die "--help printed no usage: ${OUT}"
has "--build-helper" "${OUT}" || die "--help does not name --build-helper"

say "an unknown flag, or the removed --skip-vhost, is usage on stderr, exit 2"
for flag in --bogus --skip-vhost; do
    ARGS=("${flag}"); run
    [ "${RC}" -eq 2 ] || die "${flag} exit ${RC}"
    has "usage:" "${OUT}" || die "${flag} printed no usage: ${OUT}"
done
set +e
ERR_ONLY="$(env PATH="${SHIMS}:${PATH}" UMBREE_MANAGE_DEPLOY_CONFIG="${CFG}" sh "${REPO}/tools/deploy-manage.sh" --bogus 2>/dev/null)"
set -e
[ -z "${ERR_ONLY}" ] || die "unknown-flag usage went to stdout: ${ERR_ONLY}"

say "--dry-run builds, lints, prints a plan and contacts no host"
reset_log
ARGS=(--dry-run); run
[ "${RC}" -eq 0 ] || die "dry-run exit ${RC}: ${OUT}"
[ -z "$(remote_calls)" ] || die "dry-run contacted a host: $(remote_calls)"
has "go version go1.26.9" "${OUT}" || die "dry-run did not print go version: ${OUT}"
has "sha256 " "${OUT}" || die "dry-run printed no sha256: ${OUT}"
has "User=umbree-release" "${OUT}" || die "dry-run did not show the linted unit: ${OUT}"
has "ReadWritePaths=/var/lib/umbree-release-manage /srv/static" "${OUT}" || die "linted unit lacks the static dir: ${OUT}"
has "ExecStart=/usr/local/umbree-release/bin/umbree-release-manage serve" "${OUT}" || die "linted unit does not use the helper root: ${OUT}"
has "sudo -n -l ${HELPER}" "${OUT}" || die "the plan does not name the sudoers check: ${OUT}"
has "/umbree/version.json" "${OUT}" || die "the plan does not name the version.json probe: ${OUT}"
has "nothing copied" "${OUT}" || die "dry-run did not say it changed nothing: ${OUT}"
diff -r "${W}/ops.committed" "${REPO}/ops" >/dev/null || die "a run changed the committed ops files"

say "--build-helper writes the bundle, verifies its sums, contacts no host, needs no config"
reset_log
ARGS=(--build-helper); run UMBREE_MANAGE_DEPLOY_CONFIG="${W}/absent.env"
[ "${RC}" -eq 0 ] || die "--build-helper exit ${RC}: ${OUT}"
[ -z "$(remote_calls)" ] || die "--build-helper contacted a host: $(remote_calls)"
B="${REPO}/dist/umbree-deploy-helper"
grep -q 'fake ./cmd/umbree-deploy-helper' "${B}/umbree-deploy-helper" || die "the bundle lacks the helper build"
grep -q 'GOOS=linux\|go build' "${LOG}" || die "no go build ran"
for t in umbree-release-manage.service umbree-release-manage-retain.service umbree-release-manage-retain.timer release.umbree.org.conf; do
    [ -f "${B}/templates/${t}" ] || die "the bundle lacks templates/${t}"
done
cmp -s "${B}/templates/release.umbree.org.conf" "${VHOST}" || die "the bundled vhost template is not the committed file"
( cd "${B}" && shasum -a 256 -c SHA256SUMS >/dev/null ) || die "SHA256SUMS does not verify"
[ "$(wc -l < "${B}/SHA256SUMS")" -eq 5 ] || die "SHA256SUMS does not list five files"
has "SHA256SUMS sha256" "${OUT}" || die "--build-helper did not print the bundle sha256: ${OUT}"
ARGS=(--build-helper --dry-run); run
[ "${RC}" -eq 2 ] || die "--build-helper with another flag exit ${RC}"

say "required keys, SSH_USER, path, value and URL refusals, exit 2, nothing run"
refuses_cfg() {
    reset_log
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "$1: exit ${RC}: ${OUT}"
    has "$2" "${OUT}" || die "$1 refusal not named $2: ${OUT}"
    [ -z "$(calls)" ] || die "$1 still ran tools"
    write_cfg
}
for key in RELEASE_HOST SSH_USER MANAGE_DATA_DIR STATIC_DIR; do
    grep -v "^${key}=" "${CFG}" > "${CFG}.tmp"; mv "${CFG}.tmp" "${CFG}"
    refuses_cfg "missing ${key}" "${key}"
done
for v in root 0 "" "de ployer"; do echo "SSH_USER=${v}" >> "${CFG}"; refuses_cfg "SSH_USER='${v}'" SSH_USER; done
for key in STATIC_DIR MANAGE_DATA_DIR MANAGE_ENV_FILE STAGE_DIR; do
    for v in "rel/path" "/a/../b" "/a//b" "/a/"; do echo "${key}=${v}" >> "${CFG}"; refuses_cfg "${key}='${v}'" "${key}"; done
done
for v in "/srv/a#b" "/srv/a&b" "/srv/a'b" "/srv/a b"; do printf 'MANAGE_DATA_DIR=%s\n' "${v}" >> "${CFG}"; refuses_cfg "MANAGE_DATA_DIR='${v}'" MANAGE_DATA_DIR; done
for v in "http://release.example" "https://" "https://10.0.0.1" "release.example"; do echo "MANAGE_URL=${v}" >> "${CFG}"; refuses_cfg "MANAGE_URL='${v}'" MANAGE_URL; done
mv "${CFG}" "${CFG}.away"
ARGS=(--dry-run); run
[ "${RC}" -eq 2 ] || die "missing config file: exit ${RC}"
mv "${CFG}.away" "${CFG}"

say "the pinned Go toolchain is required, and a GO_VERSION key does not move it"
ARGS=(--dry-run); run SANDBOX_GOVERSION=go1.27.1
[ "${RC}" -ne 0 ] || die "a go1.27.1 build was accepted"
has "go1.26.9" "${OUT}" || die "toolchain refusal did not name the pin: ${OUT}"
echo "GO_VERSION=go1.27.1" >> "${CFG}"
ARGS=(--dry-run); run SANDBOX_GOVERSION=go1.27.1
[ "${RC}" -ne 0 ] || die "GO_VERSION=go1.27.1 opted out of the pin"
ARGS=(--build-helper); run SANDBOX_GOVERSION=go1.27.1
[ "${RC}" -ne 0 ] || die "--build-helper accepted go1.27.1"
write_cfg

say "the workstation unit lint still refuses User=root, a placeholder, a dropped ProtectSystem=strict"
echo "SERVICE_USER=root" >> "${CFG}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "User=root was accepted"
write_cfg
UNIT_SRC="${REPO}/ops/systemd/umbree-release-manage.service"
cp "${UNIT_SRC}" "${W}/unit.orig"
printf 'Environment=X=<UNFILLED>\n' >> "${UNIT_SRC}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "a surviving placeholder was accepted"
grep -v '^ProtectSystem=strict' "${W}/unit.orig" > "${UNIT_SRC}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "a unit without ProtectSystem=strict was accepted"
cp "${W}/unit.orig" "${UNIT_SRC}"

say "a config listen that disagrees with the vhost refuses"
echo "MANAGE_LISTEN=127.0.0.1:8799" >> "${CFG}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "config listen 8799 against vhost 8788 was accepted"
has "8799" "${OUT}" || die "mismatch refusal did not name the port: ${OUT}"
write_cfg

say "each missing sudoers verb refuses before staging and names the verb"
for v in ${VERBS}; do
    reset_log
    ARGS=(); run SANDBOX_SUDO_DENY="${v}"
    [ "${RC}" -ne 0 ] || die "a missing sudoers rule for ${v} was accepted"
    has "sudo -n ${HELPER} ${v}" "${OUT}" || die "the refusal for ${v} did not name it: ${OUT}"
    ! grep -q '^scp ' "${LOG}" || die "a missing ${v} rule still copied the binary"
    [ -z "$(helper_calls)" ] || die "a missing ${v} rule still ran helper verbs: $(helper_calls)"
done

say "a helper preflight refusal copies nothing"
reset_log
ARGS=(); run SANDBOX_HELPER_FAIL=preflight
[ "${RC}" -ne 0 ] || die "a refused preflight was accepted"
! grep -q '^scp ' "${LOG}" || die "a refused preflight copied the binary"

say "a deploy stages only the binary, then preflight, install-binary, render, activate, verify"
reset_log
ARGS=(); run
[ "${RC}" -eq 0 ] || die "deploy exit ${RC}: ${OUT}"
[ "$(helper_calls)" = "preflight install-binary render activate " ] || die "helper verbs: $(helper_calls)"
[ "$(grep -c '^scp ' "${LOG}")" -eq 1 ] || die "more than the binary was copied: $(grep '^scp ' "${LOG}")"
grep -q "^scp .* -> ${HOST}/stage/umbree-release-manage.new$" "${LOG}" || die "the binary was not staged as .new"
for v in ${VERBS}; do grep -q "^sudo -n -l ${HELPER} ${v}$" "${LOG}" || die "no sudo -n -l check for ${v}"; done
grep -q 'healthz' "${LOG}" || die "no /healthz verify"
grep -q '/manage/login' "${LOG}" || die "no /manage/login verify"
grep -q '/umbree/version.json' "${LOG}" || die "no /umbree/version.json verify"

failing() {
    reset_log
    ARGS=(); run "$@"
    [ "${RC}" -ne 0 ] || die "$* exited 0"
    [ "$(helper_calls)" = "${WANT}" ] || die "$*: helper verbs '$(helper_calls)', want '${WANT}'"
}
say "install-binary refused: no restore, nothing changed"
WANT="preflight install-binary "; failing SANDBOX_HELPER_FAIL=install-binary
say "render, activate or verify failing calls restore"
WANT="preflight install-binary render restore "; failing SANDBOX_HELPER_FAIL=render
WANT="preflight install-binary render activate restore "; failing SANDBOX_HELPER_FAIL=activate
WANT="preflight install-binary render activate restore "; failing SANDBOX_LOGIN_FAIL=1
WANT="preflight install-binary render activate restore "; failing SANDBOX_JSON_FAIL=1
has "/umbree/version.json -> 404" "${OUT}" || die "a failed version.json probe was not named: ${OUT}"

say "--rollback checks its sudoers verbs, then the helper restore, then verify"
reset_log
ARGS=(--rollback); run
[ "${RC}" -eq 0 ] || die "rollback exit ${RC}: ${OUT}"
[ "$(helper_calls)" = "status restore " ] || die "rollback helper verbs: $(helper_calls)"
grep -q "^sudo -n -l ${HELPER} restore$" "${LOG}" || die "rollback did not check its restore rule"
reset_log
ARGS=(--rollback); run SANDBOX_NO_PREV=1
[ "${RC}" -ne 0 ] || die "rollback with no previous binary succeeded"
[ "$(helper_calls)" = "status " ] || die "rollback without a previous copy still restored: $(helper_calls)"
reset_log
ARGS=(--rollback); run SANDBOX_JSON_FAIL=1
[ "${RC}" -eq 0 ] || die "rollback probed version.json: exit ${RC}: ${OUT}"
! grep -q 'version.json' "${LOG}" || die "rollback curled version.json: $(grep version.json "${LOG}")"
ARGS=(--rollback); run SANDBOX_SUDO_DENY=restore
[ "${RC}" -ne 0 ] || die "rollback without a restore rule was accepted"

say "every connection is <SSH_USER>@host and every sudo is sudo -n on the helper with one verb"
reset_log
grep -q '^ssh-dest deployer@sandbox-host$' "${W}/all.log" || die "ssh did not connect as deployer@sandbox-host"
! grep -E '^(ssh|scp)-dest' "${W}/all.log" | grep -qv 'deployer@sandbox-host$' || die "a connection used another destination"
! grep '^sudo ' "${W}/all.log" | grep -vqE "^sudo -n (-l )?${HELPER} (preflight|install-binary|render|activate|restore|status)$" \
    || die "a sudo call is not the helper with one verb: $(grep '^sudo ' "${W}/all.log" | grep -vE "^sudo -n (-l )?${HELPER} [a-z-]+$" | sort -u)"
! grep -qE '^install -m|^nginx|^ss ' "${W}/all.log" || die "a raw privileged command ran on the host"

say "the committed vhost: seven proxied paths to one upstream, no forwarded-for, no real_ip, / still try_files"
! grep -qE 'X-Forwarded-For|real_ip|set_real_ip_from' "${VHOST}" || die "the vhost carries X-Forwarded-For or real_ip"
[ "$(grep -cE 'proxy_pass +http://umbree_manage;' "${VHOST}")" -eq 7 ] || die "the vhost does not proxy exactly seven locations to the upstream"
[ "$(grep -c '8788' "${VHOST}")" -eq 1 ] || die "the port is not stated once"
grep -qE '^upstream umbree_manage \{ *server 127.0.0.1:8788; *\}' "${VHOST}" || die "no upstream block"
grep -q 'location ^~ /manage/ {' "${VHOST}" || die "no ^~ /manage/ location"
grep -q 'location ^~ /api/v1/releases/ {' "${VHOST}" || die "no ^~ /api/v1/releases/ location"
grep -q 'location = /healthz {' "${VHOST}" || die "no = /healthz location"
for comp in umbree umbreed; do
    grep -q "location = /${comp}/version.json {" "${VHOST}" || die "no = /${comp}/version.json location"
done
grep -qE 'location = /manage \{ *return 301 /manage/; *\}' "${VHOST}" || die "no /manage redirect"
awk '/location \/ \{/{f=1} f&&/try_files \$uri \$uri\/ =404;/{print; exit}' "${VHOST}" | grep -q try_files || die "location / no longer try_files"

say "public hygiene stays green with the script and its test tracked"
bash "${REPO_ROOT}/tools/public-hygiene.sh" >/dev/null || die "tools/public-hygiene.sh is red"

printf '\n✓ deploy-manage: all checks passed\n'

#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${REPO_ROOT}/tools/deploy-manage.sh"
VHOST="${REPO_ROOT}/ops/nginx/release.umbree.org.conf"

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf '\n✗ FAILED: %s\n' "$*" >&2; exit 1; }
has() { case "$2" in *"$1"*) return 0 ;; *) return 1 ;; esac; }

[ -f "${SCRIPT}" ] || die "missing: ${SCRIPT}"

W="$(mktemp -d)"
trap 'rm -rf "${W}"' EXIT

REPO="${W}/repo"
mkdir -p "${REPO}/tools" "${REPO}/ops/nginx" "${REPO}/ops/systemd" "${REPO}/cmd/umbree-release-manage"
cp "${SCRIPT}" "${REPO}/tools/deploy-manage.sh"
cp "${REPO_ROOT}"/ops/systemd/* "${REPO}/ops/systemd/"
cp "${VHOST}" "${REPO}/ops/nginx/release.umbree.org.conf"
cp -R "${REPO}/ops" "${W}/ops.committed"

HOST="${W}/host"
mkdir -p "${HOST}/bin" "${HOST}/vhosts" "${HOST}/units" "${HOST}/stage" "${HOST}/etc" "${HOST}/data" "${HOST}/static"
SHIMS="${W}/shims"; HOSTSHIMS="${W}/hostshims"; mkdir -p "${SHIMS}" "${HOSTSHIMS}"
LOG="${W}/calls.log"; : > "${LOG}"
PRIVLOG="${W}/priv.log"; : > "${PRIVLOG}"
export SANDBOX_LOG="${LOG}" SANDBOX_HOSTSHIMS="${HOSTSHIMS}" SANDBOX_PRIVLOG="${PRIVLOG}" SANDBOX_STAGE="${HOST}/stage"

cat > "${SHIMS}/go" <<'EOF'
#!/bin/sh
echo "go $*" >> "$SANDBOX_LOG"
case "$1" in
    version) echo "go version ${SANDBOX_GOVERSION:-go1.26.9} linux/amd64"; exit 0 ;;
    env) echo "${SANDBOX_GOVERSION:-go1.26.9}"; exit 0 ;;
esac
out=""; while [ $# -gt 0 ]; do [ "$1" = -o ] && { out="$2"; shift; }; shift; done
printf 'fake umbree-release-manage %s\n' "${SANDBOX_BUILD_TAG:-1}" > "$out"; chmod 0755 "$out"
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
echo "ssh: $(printf '%s' "$script" | head -c 40 | tr '\n' ' ')" >> "$SANDBOX_LOG"
PATH="$SANDBOX_HOSTSHIMS:$PATH" sh -c "$script"
EOF
cat > "${SHIMS}/curl" <<'EOF'
#!/bin/sh
echo "curl $*" >> "$SANDBOX_LOG"
exit 0
EOF
cp "${SHIMS}/curl" "${HOSTSHIMS}/curl"
cat > "${HOSTSHIMS}/privcheck" <<'EOF'
need_sudo() { [ "${SANDBOX_IN_SUDO:-0}" = 1 ] || echo "UNSUDOED $*" >> "$SANDBOX_PRIVLOG"; }
no_sudo() { [ "${SANDBOX_IN_SUDO:-0}" = 1 ] && echo "OVERSUDO $*" >> "$SANDBOX_PRIVLOG"; return 0; }
EOF
cat > "${HOSTSHIMS}/curl" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"; no_sudo curl "$@"
echo "curl $*" >> "$SANDBOX_LOG"
case "$*" in
    *http_code*/manage/login*) [ "${SANDBOX_LOGIN_FAIL:-0}" = 1 ] && printf '502' || printf '200' ;;
    *healthz*) printf 'ok\n' ;;
esac
EOF
cat > "${HOSTSHIMS}/systemctl" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"
case "$1" in --version|is-active|show) no_sudo systemctl "$@" ;; *) need_sudo systemctl "$@" ;; esac
echo "systemctl $*" >> "$SANDBOX_LOG"
case "$1" in
    --version) echo "systemd ${SANDBOX_SYSTEMD:-255} (${SANDBOX_SYSTEMD:-255})"; echo "+PAM +AUDIT" ;;
    is-active) echo active ;;
    show) echo "${SANDBOX_MAINPID:-0}" ;;
    restart) [ "${SANDBOX_RESTART_FAIL:-0}" = 1 ] && { echo "Job for $2 failed." >&2; exit 1; } ;;
esac
exit 0
EOF
cat > "${HOSTSHIMS}/sudo" <<'EOF'
#!/bin/sh
echo "sudo $*" >> "$SANDBOX_LOG"
[ "$1" = -n ] || echo "NO-N sudo $*" >> "$SANDBOX_PRIVLOG"
[ "$1" = -n ] && shift
[ "$1" = -u ] && shift 2
case "${SANDBOX_SUDO_DENY:-}" in all|"$1") echo "sudo: a password is required" >&2; exit 1 ;; esac
[ "${SANDBOX_STATIC_RO:-0}" = 1 ] && [ "$1" = test ] && exit 1
SANDBOX_IN_SUDO=1 exec "$@"
EOF
cat > "${HOSTSHIMS}/nginx" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"; need_sudo nginx "$@"
echo "nginx $*" >> "$SANDBOX_LOG"
[ "${SANDBOX_NGINX_FAIL:-0}" = 1 ] && { echo "nginx: [emerg] bad directive" >&2; exit 1; }
exit 0
EOF
cat > "${HOSTSHIMS}/ss" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"; need_sudo ss "$@"
echo "ss $*" >> "$SANDBOX_LOG"
echo "State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process"
[ -n "${SANDBOX_PORT_HOLDER:-}" ] && echo "LISTEN 0 4096 127.0.0.1:8788 0.0.0.0:* users:((\"$(printf '%s' "${SANDBOX_PORT_HOLDER}" | cut -c1-15)\",pid=${SANDBOX_PORT_PID:-42},fd=3))"
exit 0
EOF
cat > "${HOSTSHIMS}/sha256sum" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"; no_sudo sha256sum "$@"
if [ "$1" = -c ]; then shasum -a 256 -c -; else shasum -a 256 "$@"; fi
EOF
cat > "${HOSTSHIMS}/install" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"
if [ "$1" = -d ]; then no_sudo install "$@"; for d in "$@"; do last="$d"; done; mkdir -p "$last"; exit 0; fi
mode=0755; src=""; dst=""
while [ $# -gt 0 ]; do case "$1" in -m) mode="$2"; shift ;; -o|-g) shift ;; *) [ -z "$src" ] && src="$1" || dst="$1" ;; esac; shift; done
case "$dst" in "$SANDBOX_STAGE"/*) no_sudo install "$dst" ;; *) need_sudo install "$dst" ;; esac
cp "$src" "$dst" && chmod "$mode" "$dst"; echo "install $src -> $dst" >> "$SANDBOX_LOG"
EOF
cat > "${HOSTSHIMS}/rm" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"
for a in "$@"; do case "$a" in -*) ;; "$SANDBOX_STAGE"/*) no_sudo rm "$a" ;; *) need_sudo rm "$a" ;; esac; done
exec /bin/rm "$@"
EOF
cat > "${HOSTSHIMS}/cat" <<'EOF'
#!/bin/sh
. "$SANDBOX_HOSTSHIMS/privcheck"
for a in "$@"; do [ "$a" = "$SANDBOX_ENVF" ] && need_sudo cat "$a"; done
exec /bin/cat "$@"
EOF
chmod 0755 "${SHIMS}"/* "${HOSTSHIMS}"/*

ENVF="${HOST}/etc/manage.env"
export SANDBOX_ENVF="${ENVF}"
printf 'UMBREE_MANAGE_LISTEN=127.0.0.1:8788\nUMBREE_MANAGE_DATA_DIR=%s\n' "${HOST}/data" > "${ENVF}"

CFG="${W}/manage-deploy.env"
write_cfg() {
    {
        echo "RELEASE_HOST=sandbox-host"
        echo "SSH_USER=deployer"
        echo "MANAGE_DATA_DIR=${HOST}/data"
        echo "STATIC_DIR=${HOST}/static"
        echo "MANAGE_ENV_FILE=${ENVF}"
        echo "MANAGE_BIN_DIR=${HOST}/bin"
        echo "UNIT_DIR=${HOST}/units"
        echo "VHOST_DIR=${HOST}/vhosts"
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
: > "${W}/all.log"
reset_log() { cat "${LOG}" >> "${W}/all.log"; : > "${LOG}"; }
remote_calls() { grep -E '^(ssh|scp|curl)' "${LOG}" || true; }

say "help on stdout, exit 0"
ARGS=(--help); run
[ "${RC}" -eq 0 ] || die "--help exit ${RC}"
has "usage: tools/deploy-manage.sh" "${OUT}" || die "--help printed no usage: ${OUT}"
ARGS=(-h); run
[ "${RC}" -eq 0 ] || die "-h exit ${RC}"

say "an unknown flag is usage on stderr, exit 2"
ARGS=(--bogus); run
[ "${RC}" -eq 2 ] || die "unknown flag exit ${RC}"
has "usage:" "${OUT}" || die "unknown flag printed no usage: ${OUT}"
set +e
ERR_ONLY="$(env PATH="${SHIMS}:${PATH}" UMBREE_MANAGE_DEPLOY_CONFIG="${CFG}" sh "${REPO}/tools/deploy-manage.sh" --bogus 2>/dev/null)"
set -e
[ -z "${ERR_ONLY}" ] || die "unknown-flag usage went to stdout: ${ERR_ONLY}"

say "--dry-run builds, renders, prints a plan and contacts no host"
reset_log
ARGS=(--dry-run); run
[ "${RC}" -eq 0 ] || die "dry-run exit ${RC}: ${OUT}"
[ -z "$(remote_calls)" ] || die "dry-run contacted a host: $(remote_calls)"
has "go version go1.26.9" "${OUT}" || die "dry-run did not print go version: ${OUT}"
has "sha256 " "${OUT}" || die "dry-run printed no sha256: ${OUT}"
has "User=umbree-release" "${OUT}" || die "dry-run did not show the rendered unit: ${OUT}"
has "nothing copied" "${OUT}" || die "dry-run did not say it changed nothing: ${OUT}"

say "the rendered vhost and unit carry STATIC_DIR; the committed files keep the placeholder"
has "ReadWritePaths=${HOST}/data ${HOST}/static" "${OUT}" || die "rendered unit lacks the static dir: ${OUT}"
! has "<STATIC_DIR>" "${OUT}" || die "a <STATIC_DIR> survived the render: ${OUT}"
diff -r "${W}/ops.committed" "${REPO}/ops" >/dev/null || die "a run changed the committed ops files"

say "STATIC_DIR missing, relative or unclean refuses, exit 2"
for v in "" "srv/static" "/srv/../static" "/srv//static" "/srv/static/" "/srv/./static"; do
    grep -v '^STATIC_DIR=' "${CFG}" > "${CFG}.tmp"; mv "${CFG}.tmp" "${CFG}"
    [ -z "${v}" ] || echo "STATIC_DIR=${v}" >> "${CFG}"
    reset_log
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "STATIC_DIR='${v}': exit ${RC}: ${OUT}"
    has "STATIC_DIR" "${OUT}" || die "STATIC_DIR='${v}' refusal not named: ${OUT}"
    [ -z "$(calls)" ] || die "STATIC_DIR='${v}' still ran tools"
    write_cfg
done

say "every path key must be absolute and clean, exit 2"
for key in MANAGE_BIN_DIR UNIT_DIR VHOST_DIR STAGE_DIR MANAGE_DATA_DIR MANAGE_ENV_FILE; do
    for v in "rel/path" "/a/../b" "/a//b" "/a/"; do
        echo "${key}=${v}" >> "${CFG}"
        reset_log
        ARGS=(--dry-run); run
        [ "${RC}" -eq 2 ] || die "${key}='${v}': exit ${RC}: ${OUT}"
        has "${key}" "${OUT}" || die "${key}='${v}' refusal not named: ${OUT}"
        [ -z "$(calls)" ] || die "${key}='${v}' still ran tools"
        write_cfg
    done
done

say "config values with # & ' or whitespace refuse, exit 2"
for v in "/srv/a#b" "/srv/a&b" "/srv/a'b" "/srv/a b"; do
    printf 'MANAGE_DATA_DIR=%s\n' "${v}" >> "${CFG}"
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "MANAGE_DATA_DIR='${v}': exit ${RC}: ${OUT}"
    has "MANAGE_DATA_DIR" "${OUT}" || die "bad-character refusal not named: ${OUT}"
    write_cfg
done

say "MANAGE_URL must be https with a host name, exit 2"
for v in "http://release.example" "https://" "https://10.0.0.1" "release.example"; do
    echo "MANAGE_URL=${v}" >> "${CFG}"
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "MANAGE_URL='${v}': exit ${RC}: ${OUT}"
    write_cfg
done

say "each missing required key refuses with its name, exit 2"
for key in RELEASE_HOST MANAGE_DATA_DIR; do
    grep -v "^${key}=" "${CFG}" > "${CFG}.tmp"; mv "${CFG}.tmp" "${CFG}"
    reset_log
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "missing ${key}: exit ${RC}"
    has "${key}" "${OUT}" || die "missing ${key} not named: ${OUT}"
    [ -z "$(calls)" ] || die "missing ${key} still ran tools: $(calls)"
    write_cfg
done
grep -v '^SSH_USER=' "${CFG}" > "${CFG}.tmp"; mv "${CFG}.tmp" "${CFG}"
ARGS=(--dry-run); run
[ "${RC}" -eq 2 ] || die "missing SSH_USER: exit ${RC}"
has "SSH_USER" "${OUT}" || die "missing SSH_USER not named: ${OUT}"
write_cfg
for v in root "" "de ployer"; do
    echo "SSH_USER=${v}" >> "${CFG}"
    reset_log
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "SSH_USER='${v}': exit ${RC}: ${OUT}"
    has "SSH_USER" "${OUT}" || die "SSH_USER='${v}' refusal not named: ${OUT}"
    [ -z "$(calls)" ] || die "SSH_USER='${v}' still ran tools"
    write_cfg
done
for key in SERVICE_USER MANAGE_ENV_FILE MANAGE_BIN_DIR MANAGE_LISTEN; do
    echo "${key}=" >> "${CFG}"
    ARGS=(--dry-run); run
    [ "${RC}" -eq 2 ] || die "empty ${key}: exit ${RC}"
    has "${key}" "${OUT}" || die "empty ${key} not named: ${OUT}"
    write_cfg
done
mv "${CFG}" "${CFG}.away"
ARGS=(--dry-run); run
[ "${RC}" -eq 2 ] || die "missing config file: exit ${RC}"
has "${CFG}" "${OUT}" || die "missing config file not named: ${OUT}"
mv "${CFG}.away" "${CFG}"

say "the pinned Go toolchain is required, and a GO_VERSION key does not move it"
ARGS=(--dry-run); run SANDBOX_GOVERSION=go1.27.1
[ "${RC}" -ne 0 ] || die "a go1.27.1 build was accepted"
has "go1.26.9" "${OUT}" || die "toolchain refusal did not name the pin: ${OUT}"
echo "GO_VERSION=go1.27.1" >> "${CFG}"
ARGS=(--dry-run); run SANDBOX_GOVERSION=go1.27.1
[ "${RC}" -ne 0 ] || die "GO_VERSION=go1.27.1 opted out of the pin"
ARGS=(--dry-run); run
[ "${RC}" -eq 0 ] || die "GO_VERSION key changed a go1.26.9 build: ${OUT}"
write_cfg

say "unit checks: root user, surviving placeholder, dropped ProtectSystem=strict"
echo "SERVICE_USER=root" >> "${CFG}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "User=root was accepted"
has "User=" "${OUT}" || die "root refusal did not name User=: ${OUT}"
write_cfg
UNIT_SRC="${REPO}/ops/systemd/umbree-release-manage.service"
cp "${UNIT_SRC}" "${W}/unit.orig"
printf 'Environment=X=<UNFILLED>\n' >> "${UNIT_SRC}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "a surviving placeholder was accepted"
has "<UNFILLED>" "${OUT}" || die "placeholder refusal did not name it: ${OUT}"
grep -v '^ProtectSystem=strict' "${W}/unit.orig" > "${UNIT_SRC}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "a unit without ProtectSystem=strict was accepted"
has "ProtectSystem=strict" "${OUT}" || die "ProtectSystem refusal did not name it: ${OUT}"
grep -v '^ReadWritePaths=' "${W}/unit.orig" > "${UNIT_SRC}"
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "a unit without ReadWritePaths= was accepted"
cp "${W}/unit.orig" "${UNIT_SRC}"

say "listen mismatch across config, env file and vhost refuses"
echo "MANAGE_LISTEN=127.0.0.1:8799" >> "${CFG}"
reset_log
ARGS=(--dry-run); run
[ "${RC}" -ne 0 ] || die "config listen 8799 against vhost 8788 was accepted"
has "8799" "${OUT}" || die "mismatch refusal did not name the port: ${OUT}"
write_cfg
printf 'UMBREE_MANAGE_LISTEN=127.0.0.1:8790\n' > "${ENVF}"
reset_log
ARGS=(); run
[ "${RC}" -ne 0 ] || die "env file listen 8790 was accepted"
has "UMBREE_MANAGE_LISTEN" "${OUT}" || die "env mismatch refusal did not name the key: ${OUT}"
! grep -q '^scp' "${LOG}" || die "env mismatch refusal copied files"
printf 'UMBREE_MANAGE_LISTEN=127.0.0.1:8788\nUMBREE_MANAGE_TRUSTED_PROXY=127.0.0.1\n' > "${ENVF}"
ARGS=(); run
[ "${RC}" -ne 0 ] || die "a trusted proxy in the env file was accepted"
has "UMBREE_MANAGE_TRUSTED_PROXY" "${OUT}" || die "trusted-proxy refusal did not name it: ${OUT}"
printf 'UMBREE_MANAGE_LISTEN=127.0.0.1:8788\n' > "${ENVF}"

say "port preflight refuses a port held by another process"
reset_log
ARGS=(); run SANDBOX_PORT_HOLDER=python3
[ "${RC}" -ne 0 ] || die "a port held by python3 was accepted"
has "python3" "${OUT}" || die "port refusal did not name the holder: ${OUT}"
! grep -q '^scp' "${LOG}" || die "port refusal copied files"

say "a refused sudo -n fails fast, names the step, and copies nothing"
for deny in all cat ss; do
    reset_log
    ARGS=(); run SANDBOX_SUDO_DENY="${deny}"
    [ "${RC}" -ne 0 ] || die "sudo denied (${deny}) was accepted"
    has "step" "${OUT}" || die "sudo refusal (${deny}) did not name the step: ${OUT}"
    has "sudo -n" "${OUT}" || die "sudo refusal (${deny}) did not say sudo -n: ${OUT}"
    ! grep -q '^scp ' "${LOG}" || die "sudo refusal (${deny}) copied files"
done

say "an unwritable static dir refuses before any copy"
printf 'old vhost\n' > "${HOST}/vhosts/release.umbree.org.conf"
reset_log
ARGS=(); run SANDBOX_STATIC_RO=1
[ "${RC}" -ne 0 ] || die "an unwritable static dir was accepted"
has "${HOST}/static" "${OUT}" || die "static refusal did not name the dir: ${OUT}"
! grep -q '^scp' "${LOG}" || die "static refusal copied files"

say "a first deploy that fails nginx -t removes what had no previous copy"
reset_log
ARGS=(); run SANDBOX_NGINX_FAIL=1
[ "${RC}" -ne 0 ] || die "first-deploy nginx -t failure exited 0"
[ ! -e "${HOST}/bin/umbree-release-manage" ] || die "first-deploy failure left the new binary"
[ ! -e "${HOST}/units/umbree-release-manage.service" ] || die "first-deploy failure left the new unit"
grep -q '^old vhost$' "${HOST}/vhosts/release.umbree.org.conf" || die "first-deploy failure did not restore the old vhost"

say "a first deploy that fails verify removes what had no previous copy"
ARGS=(); run SANDBOX_LOGIN_FAIL=1
[ "${RC}" -ne 0 ] || die "first-deploy verify failure exited 0"
[ ! -e "${HOST}/bin/umbree-release-manage" ] || die "first-deploy verify failure left the new binary"
[ ! -e "${HOST}/units/umbree-release-manage-retain.timer" ] || die "first-deploy verify failure left the timer"
grep -n 'systemctl stop umbree-release-manage.service' "${LOG}" | tail -n1 | cut -d: -f1 > "${W}/stop.n" || true
grep -n 'systemctl disable umbree-release-manage.service' "${LOG}" | tail -n1 | cut -d: -f1 > "${W}/disable.n" || true
[ -s "${W}/stop.n" ] || die "first-deploy verify failure did not stop the service"
[ "$(cat "${W}/stop.n")" -lt "$(cat "${W}/disable.n")" ] || die "stop did not come before disable"

say "systemd below 232 warns about the ignored sandbox keys and proceeds"
reset_log
ARGS=(); run SANDBOX_SYSTEMD=229 SANDBOX_BUILD_TAG=1
[ "${RC}" -eq 0 ] || die "systemd 229 deploy exit ${RC}: ${OUT}"
has "! systemd 229" "${OUT}" || die "no systemd warning: ${OUT}"
has "ProtectSystem=strict" "${OUT}" || die "warning did not name ProtectSystem=strict: ${OUT}"
rm -f "${HOST}/bin/umbree-release-manage" "${HOST}"/units/* "${HOST}"/stage/*
printf 'old vhost\n' > "${HOST}/vhosts/release.umbree.org.conf"

say "first live deploy installs binary, units and vhost, enables the service only, and verifies"
reset_log
ARGS=(); run SANDBOX_BUILD_TAG=1
[ "${RC}" -eq 0 ] || die "first deploy exit ${RC}: ${OUT}"
grep -q 'fake umbree-release-manage 1' "${HOST}/bin/umbree-release-manage" || die "binary not installed"
grep -q 'User=umbree-release' "${HOST}/units/umbree-release-manage.service" || die "unit not installed"
[ -f "${HOST}/units/umbree-release-manage-retain.timer" ] || die "timer not installed"
grep -q '127.0.0.1:8788' "${HOST}/vhosts/release.umbree.org.conf" || die "vhost not installed"
grep -q "root  ${HOST}/static;" "${HOST}/vhosts/release.umbree.org.conf" || die "installed vhost lacks the static root"
grep -q 'systemctl enable umbree-release-manage.service' "${LOG}" || die "the service was not enabled"
! grep -qE 'systemctl enable .*(timer|retain)' "${LOG}" || die "the retain timer was enabled"
! has "! systemd" "${OUT}" || die "systemd 255 warned"
grep -q 'nginx -t' "${LOG}" || die "nginx -t not run"
grep -q 'systemctl daemon-reload' "${LOG}" || die "no daemon-reload"
grep -q 'systemctl restart umbree-release-manage.service' "${LOG}" || die "no restart"
grep -q 'systemctl reload nginx' "${LOG}" || die "no nginx reload"
grep -q 'healthz' "${LOG}" || die "no /healthz verify"
grep -q '/manage/login' "${LOG}" || die "no /manage/login verify"
cp "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1"

say "a holder with the service's truncated name but another pid is refused"
reset_log
ARGS=(); run SANDBOX_PORT_HOLDER=umbree-release-manage SANDBOX_PORT_PID=99 SANDBOX_MAINPID=42 SANDBOX_BUILD_TAG=2
[ "${RC}" -ne 0 ] || die "a foreign umbree-release- holder was accepted"
has "pid=99" "${OUT}" || die "foreign-holder refusal did not name it: ${OUT}"
! grep -q '^scp' "${LOG}" || die "foreign-holder refusal copied files"

say "the port held by the service's MainPID is a redeploy, accepted"
reset_log
ARGS=(); run SANDBOX_PORT_HOLDER=umbree-release-manage SANDBOX_PORT_PID=42 SANDBOX_MAINPID=42 SANDBOX_BUILD_TAG=2
[ "${RC}" -eq 0 ] || die "redeploy with own port holder exit ${RC}: ${OUT}"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "v2 not installed"

say "nginx -t failure restores the previous vhost and exits non-zero"
printf '\n' >> "${REPO}/ops/nginx/release.umbree.org.conf"
ARGS=(); run SANDBOX_NGINX_FAIL=1 SANDBOX_BUILD_TAG=3
[ "${RC}" -ne 0 ] || die "nginx -t failure exited 0"
cmp -s "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1" || die "previous vhost not restored after nginx -t failure"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "binary not restored after nginx -t failure"

say "a failed systemctl restart restores binary, units and vhost"
cp "${HOST}/units/umbree-release-manage.service" "${W}/unit.before"
ARGS=(); run SANDBOX_RESTART_FAIL=1 SANDBOX_BUILD_TAG=7
[ "${RC}" -ne 0 ] || die "failed restart exited 0"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "binary not restored after failed restart"
cmp -s "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1" || die "vhost not restored after failed restart"
cmp -s "${HOST}/units/umbree-release-manage.service" "${W}/unit.before" || die "unit not restored after failed restart"

say "a failed /manage/login verify restores .prev"
ARGS=(); run SANDBOX_LOGIN_FAIL=1 SANDBOX_BUILD_TAG=4
[ "${RC}" -ne 0 ] || die "failed verify exited 0"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "binary not restored after failed verify"
cmp -s "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1" || die "vhost not restored after failed verify"

say "--rollback restores binary, units and vhost"
ARGS=(); run SANDBOX_BUILD_TAG=5
[ "${RC}" -eq 0 ] || die "deploy v5 exit ${RC}: ${OUT}"
cp "${HOST}/stage/umbree-release-manage.service.prev" "${W}/unit.v2"
printf 'X=1\n' >> "${HOST}/units/umbree-release-manage.service"
reset_log
ARGS=(--rollback); run
[ "${RC}" -eq 0 ] || die "rollback exit ${RC}: ${OUT}"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "rollback did not restore the binary"
cmp -s "${HOST}/units/umbree-release-manage.service" "${W}/unit.v2" || die "rollback did not restore the unit"
cmp -s "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1" || die "rollback did not restore the vhost"
grep -q 'systemctl restart umbree-release-manage.service' "${LOG}" || die "rollback did not restart"

say "--skip-vhost installs binary and units only"
reset_log
ARGS=(--skip-vhost); run SANDBOX_BUILD_TAG=6
[ "${RC}" -eq 0 ] || die "--skip-vhost exit ${RC}: ${OUT}"
! grep -q 'release.umbree.org.conf' "${LOG}" || die "--skip-vhost touched the vhost"

say "every connection is <SSH_USER>@host, never root@"
reset_log
grep -q '^ssh-dest deployer@sandbox-host$' "${W}/all.log" || die "ssh did not connect as deployer@sandbox-host: $(grep ssh-dest "${W}/all.log" | sort -u)"
! grep -E '^(ssh|scp)-dest' "${W}/all.log" | grep -qv 'deployer@sandbox-host$' || die "a connection used another destination: $(grep -E '^(ssh|scp)-dest' "${W}/all.log" | sort -u)"
grep -q '^scp-dest deployer@sandbox-host$' "${W}/all.log" || die "scp did not target deployer@sandbox-host"

say "privileged commands carry sudo -n and unprivileged ones do not, across every run"
[ ! -s "${PRIVLOG}" ] || die "privilege split broken: $(sort -u "${PRIVLOG}" | head -n 20)"
grep -q '^sudo -n ' "${W}/all.log" || die "no sudo -n call was ever made"

say "the committed vhost: three proxied paths, no forwarded-for, no real_ip, / still try_files"
! grep -qE 'X-Forwarded-For|real_ip|set_real_ip_from' "${VHOST}" || die "the vhost carries X-Forwarded-For or real_ip"
[ "$(grep -cE 'proxy_pass +http://umbree_manage;' "${VHOST}")" -eq 3 ] || die "the vhost does not proxy exactly three locations to the upstream"
[ "$(grep -c '8788' "${VHOST}")" -eq 1 ] || die "the port is not stated once"
grep -qE '^upstream umbree_manage \{ *server 127.0.0.1:8788; *\}' "${VHOST}" || die "no upstream block"
grep -q 'location ^~ /manage/ {' "${VHOST}" || die "no ^~ /manage/ location"
grep -q 'location ^~ /api/v1/releases/ {' "${VHOST}" || die "no ^~ /api/v1/releases/ location"
grep -q 'location = /healthz {' "${VHOST}" || die "no = /healthz location"
grep -qE 'location = /manage \{ *return 301 /manage/; *\}' "${VHOST}" || die "no /manage redirect"
[ "$(grep -cE 'proxy_buffering +off;' "${VHOST}")" -eq 2 ] || die "proxy_buffering off not on both streaming prefixes"
[ "$(grep -cE 'proxy_read_timeout +1h;' "${VHOST}")" -eq 2 ] || die "proxy_read_timeout 1h not on both streaming prefixes"
awk '/location \/ \{/{f=1} f&&/try_files \$uri \$uri\/ =404;/{print; exit}' "${VHOST}" | grep -q try_files || die "location / no longer try_files"
[ "$(grep -c 'proxy_pass' "${VHOST}")" -eq 3 ] || die "an extra proxy_pass appeared"

say "public hygiene stays green with the script and its test tracked"
bash "${REPO_ROOT}/tools/public-hygiene.sh" >/dev/null || die "tools/public-hygiene.sh is red"

printf '\n✓ deploy-manage: all checks passed\n'

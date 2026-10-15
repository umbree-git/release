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
sed 's#<STATIC_DIR>#/srv/static#' "${VHOST}" > "${REPO}/ops/nginx/release.umbree.org.conf"

HOST="${W}/host"
mkdir -p "${HOST}/bin" "${HOST}/vhosts" "${HOST}/units" "${HOST}/stage" "${HOST}/etc" "${HOST}/data"
SHIMS="${W}/shims"; HOSTSHIMS="${W}/hostshims"; mkdir -p "${SHIMS}" "${HOSTSHIMS}"
LOG="${W}/calls.log"; : > "${LOG}"
export SANDBOX_LOG="${LOG}" SANDBOX_HOSTSHIMS="${HOSTSHIMS}"

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
for a in "$@"; do case "$a" in -q) ;; *:*) dst="${a#*:}" ;; *) src="$a" ;; esac; done
cp "$src" "$dst"; echo "scp $src -> $dst" >> "$SANDBOX_LOG"
EOF
cat > "${SHIMS}/ssh" <<'EOF'
#!/bin/sh
script=""; for a in "$@"; do script="$a"; done
echo "ssh: $(printf '%s' "$script" | head -c 40 | tr '\n' ' ')" >> "$SANDBOX_LOG"
PATH="$SANDBOX_HOSTSHIMS:$PATH" sh -c "$script"
EOF
cat > "${SHIMS}/curl" <<'EOF'
#!/bin/sh
echo "curl $*" >> "$SANDBOX_LOG"
exit 0
EOF
cp "${SHIMS}/curl" "${HOSTSHIMS}/curl"
cat > "${HOSTSHIMS}/curl" <<'EOF'
#!/bin/sh
echo "curl $*" >> "$SANDBOX_LOG"
case "$*" in
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
cat > "${HOSTSHIMS}/nginx" <<'EOF'
#!/bin/sh
echo "nginx $*" >> "$SANDBOX_LOG"
[ "${SANDBOX_NGINX_FAIL:-0}" = 1 ] && { echo "nginx: [emerg] bad directive" >&2; exit 1; }
exit 0
EOF
cat > "${HOSTSHIMS}/ss" <<'EOF'
#!/bin/sh
echo "ss $*" >> "$SANDBOX_LOG"
echo "State  Recv-Q Send-Q Local Address:Port Peer Address:Port Process"
[ -n "${SANDBOX_PORT_HOLDER:-}" ] && echo "LISTEN 0 4096 127.0.0.1:8788 0.0.0.0:* users:((\"${SANDBOX_PORT_HOLDER}\",pid=42,fd=3))"
exit 0
EOF
cat > "${HOSTSHIMS}/sha256sum" <<'EOF'
#!/bin/sh
if [ "$1" = -c ]; then shasum -a 256 -c -; else shasum -a 256 "$@"; fi
EOF
cat > "${HOSTSHIMS}/install" <<'EOF'
#!/bin/sh
mode=0755; src=""; dst=""
while [ $# -gt 0 ]; do case "$1" in -m) mode="$2"; shift ;; -o|-g) shift ;; *) [ -z "$src" ] && src="$1" || dst="$1" ;; esac; shift; done
cp "$src" "$dst" && chmod "$mode" "$dst"; echo "install $src -> $dst" >> "$SANDBOX_LOG"
EOF
chmod 0755 "${SHIMS}"/* "${HOSTSHIMS}"/*

ENVF="${HOST}/etc/manage.env"
printf 'UMBREE_MANAGE_LISTEN=127.0.0.1:8788\nUMBREE_MANAGE_DATA_DIR=%s\n' "${HOST}/data" > "${ENVF}"

CFG="${W}/manage-deploy.env"
write_cfg() {
    {
        echo "RELEASE_HOST=sandbox-host"
        echo "MANAGE_DATA_DIR=${HOST}/data"
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
reset_log() { : > "${LOG}"; }
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

say "--dry-run of the committed vhost warns about its unresolved placeholder, still contacts no host"
cp "${VHOST}" "${REPO}/ops/nginx/release.umbree.org.conf"
reset_log
ARGS=(--dry-run); run
[ "${RC}" -eq 0 ] || die "dry-run with committed vhost exit ${RC}: ${OUT}"
has "<STATIC_DIR>" "${OUT}" || die "dry-run did not name the unresolved vhost placeholder: ${OUT}"
[ -z "$(remote_calls)" ] || die "dry-run contacted a host: $(remote_calls)"

say "a live run refuses an unresolved vhost placeholder before contacting the host"
reset_log
ARGS=(); run
[ "${RC}" -ne 0 ] || die "live run with <STATIC_DIR> in the vhost succeeded"
[ -z "$(remote_calls)" ] || die "the refusal contacted a host: $(remote_calls)"
sed 's#<STATIC_DIR>#/srv/static#' "${VHOST}" > "${REPO}/ops/nginx/release.umbree.org.conf"

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

say "the pinned Go toolchain is required"
ARGS=(--dry-run); run SANDBOX_GOVERSION=go1.27.1
[ "${RC}" -ne 0 ] || die "a go1.27.1 build was accepted"
has "go1.26.9" "${OUT}" || die "toolchain refusal did not name the pin: ${OUT}"

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

say "first live deploy installs binary, units and vhost and verifies"
printf 'old vhost\n' > "${HOST}/vhosts/release.umbree.org.conf"
reset_log
ARGS=(); run SANDBOX_BUILD_TAG=1
[ "${RC}" -eq 0 ] || die "first deploy exit ${RC}: ${OUT}"
grep -q 'fake umbree-release-manage 1' "${HOST}/bin/umbree-release-manage" || die "binary not installed"
grep -q 'User=umbree-release' "${HOST}/units/umbree-release-manage.service" || die "unit not installed"
[ -f "${HOST}/units/umbree-release-manage-retain.timer" ] || die "timer not installed"
grep -q '127.0.0.1:8788' "${HOST}/vhosts/release.umbree.org.conf" || die "vhost not installed"
grep -q 'nginx -t' "${LOG}" || die "nginx -t not run"
grep -q 'systemctl daemon-reload' "${LOG}" || die "no daemon-reload"
grep -q 'systemctl restart umbree-release-manage.service' "${LOG}" || die "no restart"
grep -q 'systemctl reload nginx' "${LOG}" || die "no nginx reload"
grep -q 'healthz' "${LOG}" || die "no /healthz verify"
grep -q '/manage/login' "${LOG}" || die "no /manage/login verify"
cp "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1"

say "the port held by the installed service is a redeploy, accepted"
reset_log
ARGS=(); run SANDBOX_PORT_HOLDER=umbree-release-manage SANDBOX_BUILD_TAG=2
[ "${RC}" -eq 0 ] || die "redeploy with own port holder exit ${RC}: ${OUT}"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "v2 not installed"

say "nginx -t failure restores the previous vhost and exits non-zero"
printf '\n' >> "${REPO}/ops/nginx/release.umbree.org.conf"
ARGS=(); run SANDBOX_NGINX_FAIL=1 SANDBOX_BUILD_TAG=3
[ "${RC}" -ne 0 ] || die "nginx -t failure exited 0"
cmp -s "${HOST}/vhosts/release.umbree.org.conf" "${W}/vhost.v1" || die "previous vhost not restored after nginx -t failure"
grep -q 'fake umbree-release-manage 2' "${HOST}/bin/umbree-release-manage" || die "binary not restored after nginx -t failure"

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

say "the committed vhost: three proxied paths, no forwarded-for, no real_ip, / still try_files"
! grep -qE 'X-Forwarded-For|real_ip|set_real_ip_from' "${VHOST}" || die "the vhost carries X-Forwarded-For or real_ip"
[ "$(grep -cE 'proxy_pass +http://127.0.0.1:8788;' "${VHOST}")" -eq 3 ] || die "the vhost does not proxy exactly three locations"
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

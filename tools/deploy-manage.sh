#!/bin/sh
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
CONFIG="${UMBREE_MANAGE_DEPLOY_CONFIG:-${HOME}/.umbree/release/manage-deploy.env}"
NAME=umbree-release-manage
GO_PIN=go1.26.9
UNIT="${NAME}.service"
UNITS="${NAME}.service ${NAME}-retain.service ${NAME}-retain.timer"
VHOST_SRC="${ROOT}/ops/nginx/release.umbree.org.conf"
VHOST_NAME="$(basename "${VHOST_SRC}")"

usage() {
    cat <<USAGE
usage: tools/deploy-manage.sh [--dry-run] [--skip-vhost] [--rollback]

  Builds ${NAME} from this checkout, renders its units from ops/systemd/,
  installs binary, units and the vhost on the release host named in the
  operator's deploy config, restarts the service, reloads nginx and verifies.
  A failed nginx -t or verify restores the previous copies.

  --dry-run     build, render and print the plan; contacts no host
  --skip-vhost  binary and units only
  --rollback    restore the previous binary, units and vhost, then verify

  Config: \$UMBREE_MANAGE_DEPLOY_CONFIG (default ~/.umbree/release/manage-deploy.env)
USAGE
}

refuse_usage() { printf '%s %s\n' "${M_FAIL}" "$*" >&2; usage >&2; exit 2; }

pick_markers() {
    loc="${LC_ALL:-${LC_CTYPE:-${LANG:-}}}"
    fancy=0
    case "$(printf '%s' "${loc}" | tr '[:upper:]' '[:lower:]')" in *utf-8*|*utf8*) fancy=1 ;; esac
    case "${TERM:-}" in ''|dumb|linux|vt100*|vt102*|vt220*) fancy=0 ;; esac
    [ -n "${UMBREE_ICONS:-}" ] && fancy="${UMBREE_ICONS}"
    if [ "${fancy}" = 1 ]; then M_INFO='->'; M_OK='✓'; M_FAIL='✗'; M_WARN='!'
    else M_INFO='->'; M_OK='ok'; M_FAIL='!!'; M_WARN='!'; fi
}

say()  { printf '%s %s\n' "${M_INFO}" "$*"; }
ok()   { printf '%s %s\n' "${M_OK}" "$*"; }
warn() { printf '%s %s\n' "${M_WARN}" "$*" >&2; }
fail() { printf '%s %s\n' "${M_FAIL}" "$*" >&2; exit 1; }
refuse_config() { printf '%s %s\n' "${M_FAIL}" "$*" >&2; exit 2; }

cfg_get() {
    line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?$1=" "${CONFIG}" | tail -n1)"
    v="${line#*"$1"=}"
    case "${v}" in \"*\") v="${v#\"}"; v="${v%\"}" ;; esac
    printf '%s' "${v}"
}

cfg_has() { grep -qE "^[[:space:]]*(export[[:space:]]+)?$1=" "${CONFIG}"; }

cfg_value() {
    if cfg_has "$1"; then
        v="$(cfg_get "$1")"
        [ -n "${v}" ] || refuse_config "$1 is empty in ${CONFIG}"
        case "${v}" in *[\#\&\'[:space:]]*) refuse_config "$1 in ${CONFIG} holds a #, &, ' or whitespace — refused rather than quoted" ;; esac
        printf '%s' "${v}"
    elif [ -n "$2" ]; then
        printf '%s' "$2"
    else
        refuse_config "$1 is missing from ${CONFIG} — it has no default"
    fi
}

check_clean_dir() {
    case "$2" in
        /*) ;;
        *) refuse_config "$1 must be an absolute path, got $2" ;;
    esac
    case "$2/" in
        *//*|*/./*|*/../*) refuse_config "$1 must be a clean path (no //, . or ..), got $2" ;;
    esac
    case "$2" in */) [ "$2" = / ] || refuse_config "$1 must not end in /, got $2" ;; esac
}

check_url() {
    host="${2#https://}"
    case "$2" in https://?*) ;; *) refuse_config "$1 must be an https URL, got $2" ;; esac
    host="${host%%/*}"; host="${host%%:*}"
    case "${host}" in
        *[!A-Za-z0-9.-]*|'') refuse_config "$1 must name a host, got $2" ;;
        *[A-Za-z]*) ;;
        *) refuse_config "$1 must name a host, not an address, got $2" ;;
    esac
}

check_paths() {
    check_clean_dir STATIC_DIR "${STATIC_DIR}"
    check_clean_dir MANAGE_DATA_DIR "${MANAGE_DATA_DIR}"
    check_clean_dir MANAGE_ENV_FILE "${MANAGE_ENV_FILE}"
    check_clean_dir MANAGE_BIN_DIR "${MANAGE_BIN_DIR}"
    check_clean_dir UNIT_DIR "${UNIT_DIR}"
    check_clean_dir VHOST_DIR "${VHOST_DIR}"
    check_clean_dir STAGE_DIR "${STAGE_DIR}"
}

load_config() {
    [ -f "${CONFIG}" ] || refuse_config "no deploy config at ${CONFIG} — create it (RELEASE_HOST, MANAGE_DATA_DIR, STATIC_DIR, …) or set \$UMBREE_MANAGE_DEPLOY_CONFIG"
    RELEASE_HOST="$(cfg_value RELEASE_HOST '')"
    MANAGE_DATA_DIR="$(cfg_value MANAGE_DATA_DIR '')"
    STATIC_DIR="$(cfg_value STATIC_DIR '')"
    SERVICE_USER="$(cfg_value SERVICE_USER umbree-release)"
    MANAGE_ENV_FILE="$(cfg_value MANAGE_ENV_FILE /etc/umbree-release-manage/manage.env)"
    MANAGE_BIN_DIR="$(cfg_value MANAGE_BIN_DIR /usr/local/bin)"
    MANAGE_LISTEN="$(cfg_value MANAGE_LISTEN 127.0.0.1:8788)"
    MANAGE_URL="$(cfg_value MANAGE_URL https://release.umbree.org)"
    MANAGE_URL="${MANAGE_URL%/}"
    check_url MANAGE_URL "${MANAGE_URL}"
    UNIT_DIR="$(cfg_value UNIT_DIR /etc/systemd/system)"
    VHOST_DIR="$(cfg_value VHOST_DIR /etc/nginx/sites-enabled)"
    STAGE_DIR="$(cfg_value STAGE_DIR "/root/${NAME}-deploy")"
    check_paths
    PORT="${MANAGE_LISTEN##*:}"
}

check_vhost_port() {
    ports="$(sed -n -E 's#^[[:space:]]*upstream[[:space:]]+umbree_manage[[:space:]]*\{[[:space:]]*server[[:space:]]+([^;[:space:]]+);.*#\1#p' "${VHOST_SRC}" | sort -u)"
    [ -n "${ports}" ] || fail "${VHOST_NAME} has no upstream umbree_manage — the manage locations are missing"
    [ "${ports}" = "${MANAGE_LISTEN}" ] \
        || fail "MANAGE_LISTEN ${MANAGE_LISTEN} disagrees with ${VHOST_NAME}, which proxies to $(printf '%s' "${ports}" | tr '\n' ' ')"
}

render_vhost() {
    sed -e "s#<STATIC_DIR>#${STATIC_DIR}#g" "${VHOST_SRC}" > "${OUT}/${VHOST_NAME}"
    left="$(sed 's/#.*//' "${OUT}/${VHOST_NAME}" | grep -oE '<[A-Za-z_]+>' | sort -u | tr '\n' ' ' || true)"
    [ -z "${left}" ] || fail "${VHOST_NAME} keeps unfilled placeholders after render: ${left}"
    ok "vhost rendered: root ${STATIC_DIR}"
}

render_unit() {
    sed -e "s#<SERVICE_USER>#${SERVICE_USER}#g" -e "s#<MANAGE_ENV_FILE>#${MANAGE_ENV_FILE}#g" \
        -e "s#<MANAGE_BIN_DIR>#${MANAGE_BIN_DIR}#g" -e "s#<MANAGE_DATA_DIR>#${MANAGE_DATA_DIR}#g" -e "s#<STATIC_DIR>#${STATIC_DIR}#g" "$1"
}

check_unit() {
    left="$(grep -oE '<[A-Za-z_]+>' "$2" | sort -u | tr '\n' ' ' || true)"
    [ -z "${left}" ] || fail "$1 keeps unfilled placeholders after render: ${left}"
    case "$1" in *.timer) return 0 ;; esac
    user="$(sed -n 's/^User=//p' "$2" | tail -n1)"
    case "${user}" in ''|root|0) fail "$1 renders User=${user} — the service never runs as root" ;; esac
    grep -q '^ProtectSystem=strict$' "$2" || fail "$1 lacks ProtectSystem=strict"
    grep -q '^ReadWritePaths=.' "$2" || fail "$1 lacks ReadWritePaths="
}

render_units() {
    for u in ${UNITS}; do
        [ -f "${ROOT}/ops/systemd/${u}" ] || fail "missing template ops/systemd/${u}"
        render_unit "${ROOT}/ops/systemd/${u}" > "${OUT}/${u}"
        check_unit "${u}" "${OUT}/${u}"
    done
    ok "units rendered: ${UNITS}"
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
    else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

build() {
    say "$(go version)"
    have="$(go env GOVERSION)"
    [ "${have}" = "${GO_PIN}" ] || fail "go is ${have}, the deploy builds with ${GO_PIN} only — put ${GO_PIN} first on PATH"
    say "building ${NAME} for linux/amd64"
    ( cd "${ROOT}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local \
        go build -trimpath -o "${OUT}/${NAME}" "./cmd/${NAME}" ) || fail "build failed"
    SUM="$(sha256_of "${OUT}/${NAME}")"
    ok "built ${NAME}: sha256 ${SUM}"
}

print_plan() {
    rev="$(git -C "${ROOT}" rev-parse --abbrev-ref HEAD 2>/dev/null || echo '?')@$(git -C "${ROOT}" rev-parse --short HEAD 2>/dev/null || echo '?')"
    say "plan for ${RELEASE_HOST} from ${rev}:"
    printf '    preflight  %s free or held by %s MainPID; %s agrees; no UMBREE_MANAGE_TRUSTED_PROXY; %s writable by %s\n' "${PORT}" "${UNIT}" "${MANAGE_ENV_FILE}" "${STATIC_DIR}" "${SERVICE_USER}"
    printf '    binary     %s/%s  (previous kept in %s as .prev)\n' "${MANAGE_BIN_DIR}" "${NAME}" "${STAGE_DIR}"
    printf '    units      %s/{%s}  (daemon-reload, enable and restart %s; the retain timer is not enabled)\n' "${UNIT_DIR}" "$(printf '%s' "${UNITS}" | tr ' ' ',')" "${UNIT}"
    if [ "${SKIP_VHOST}" = 1 ]; then printf '    vhost      skipped (--skip-vhost)\n'
    else printf '    vhost      %s/%s  (nginx -t gates the reload)\n' "${VHOST_DIR}" "${VHOST_NAME}"; fi
    printf '    verify     is-active %s; %s/healthz; %s/manage/login 200\n' "${UNIT}" "${MANAGE_URL}" "${MANAGE_URL}"
    for u in ${UNITS}; do diff -u -L /dev/null -L "${UNIT_DIR}/${u}" /dev/null "${OUT}/${u}" || true; done
}

remote() { ssh -o BatchMode=yes -o ConnectTimeout=10 "${RELEASE_HOST}" "$1"; }

preflight_body() {
    cat <<'BODY'
[ -f "$env" ] || { echo "refuse: no env file $env on the host"; exit 1; }
l=$(grep -E '^[[:space:]]*(export[[:space:]]+)?UMBREE_MANAGE_LISTEN=' "$env" | tail -n1 | sed -e 's/^.*UMBREE_MANAGE_LISTEN=//' -e 's/["[:space:]]//g' | tr -d "'")
[ "$l" = "$listen" ] || { echo "refuse: UMBREE_MANAGE_LISTEN in $env is '$l', the deploy and the vhost use $listen"; exit 1; }
if grep -qE '^[[:space:]]*(export[[:space:]]+)?UMBREE_MANAGE_TRUSTED_PROXY=' "$env"; then
  echo "refuse: $env sets UMBREE_MANAGE_TRUSTED_PROXY, and this edge sends no X-Forwarded-For"; exit 1
fi
sudo -u "$user" test -w "$static" || { echo "refuse: $static is not writable by $user — promote could not republish"; exit 1; }
held=$(ss -ltnpH "sport = :$port" 2>/dev/null | grep -v '^State' || true)
if [ -n "$held" ]; then
  main=$(systemctl show -p MainPID --value "$unit" 2>/dev/null || echo 0)
  pids=$(printf '%s\n' "$held" | grep -oE 'pid=[0-9]+' | sort -u)
  if [ -n "$main" ] && [ "$main" != 0 ] && [ "$pids" = "pid=$main" ]; then
    echo "port $port held by $unit (MainPID $main, a redeploy)"
  else
    echo "refuse: port $port is already bound by something other than $unit (MainPID ${main:-0}): $held"; exit 1
  fi
fi
echo 'preflight passed'
BODY
}

preflight() {
    say "preflight on ${RELEASE_HOST} (read-only)"
    remote "set -e
env='${MANAGE_ENV_FILE}'; listen='${MANAGE_LISTEN}'; port='${PORT}'; unit='${UNIT}'
user='${SERVICE_USER}'; static='${STATIC_DIR}'
$(preflight_body)" || fail "preflight refused on ${RELEASE_HOST} — nothing was copied"
    sdv="$(remote "systemctl --version | sed -n '1s/^systemd \([0-9][0-9]*\).*/\1/p'")" || sdv=""
    case "${sdv}" in
        ''|*[!0-9]*) warn "could not read the systemd version on ${RELEASE_HOST}; ProtectSystem=strict and ReadWritePaths= may be ignored" ;;
        *) [ "${sdv}" -ge 232 ] || warn "systemd ${sdv} on ${RELEASE_HOST} ignores ProtectSystem=strict and ReadWritePaths= — file ownership is the control (ops/README.md §7)" ;;
    esac
}

stage() {
    say "copying to ${RELEASE_HOST}:${STAGE_DIR}/"
    remote "install -d -m 0700 '${STAGE_DIR}'" || fail "could not create ${STAGE_DIR} on ${RELEASE_HOST}"
    scp -q "${OUT}/${NAME}" "${RELEASE_HOST}:${STAGE_DIR}/${NAME}.new" || fail "scp of the binary failed"
    for u in ${UNITS}; do scp -q "${OUT}/${u}" "${RELEASE_HOST}:${STAGE_DIR}/${u}.new" || fail "scp of ${u} failed"; done
    [ "${SKIP_VHOST}" = 1 ] || scp -q "${OUT}/${VHOST_NAME}" "${RELEASE_HOST}:${STAGE_DIR}/${VHOST_NAME}.new" || fail "scp of the vhost failed"
}

pairs() {
    printf '%s %s 0755\n' "${NAME}" "${MANAGE_BIN_DIR}/${NAME}"
    for u in ${UNITS}; do printf '%s %s 0644\n' "${u}" "${UNIT_DIR}/${u}"; done
    [ "${SKIP_VHOST}" = 1 ] || printf '%s %s 0644\n' "${VHOST_NAME}" "${VHOST_DIR}/${VHOST_NAME}"
}

restore_script() {
    printf 'restore() {\n'
    pairs | while read -r f dst mode; do
        printf "  if [ -f '%s/%s.prev' ]; then install -m %s -o root -g root '%s/%s.prev' '%s' || true\n" "${STAGE_DIR}" "${f}" "${mode}" "${STAGE_DIR}" "${f}" "${dst}"
        printf "  elif [ -f '%s/%s.absent' ]; then rm -f '%s'; fi\n" "${STAGE_DIR}" "${f}" "${dst}"
    done
    printf "  [ -f '%s/%s.absent' ] && { systemctl stop %s; systemctl disable %s; } >/dev/null 2>&1\n" "${STAGE_DIR}" "${UNIT}" "${UNIT}" "${UNIT}"
    printf '  systemctl daemon-reload || true\n'
    printf "  [ -f '%s/%s' ] && systemctl restart %s || true\n" "${MANAGE_BIN_DIR}" "${NAME}" "${UNIT}"
    printf '  nginx -t >/dev/null 2>&1 && systemctl reload nginx || true\n}\n'
}

install_script() {
    printf "echo '%s  %s/%s.new' | sha256sum -c - >/dev/null || { echo 'checksum mismatch after copy'; exit 1; }\n" "${SUM}" "${STAGE_DIR}" "${NAME}"
    pairs | while read -r f dst _; do
        printf "rm -f '%s/%s.prev' '%s/%s.absent'\n" "${STAGE_DIR}" "${f}" "${STAGE_DIR}" "${f}"
        printf "if [ -f '%s' ]; then cp -p '%s' '%s/%s.prev'; else : > '%s/%s.absent'; fi\n" "${dst}" "${dst}" "${STAGE_DIR}" "${f}" "${STAGE_DIR}" "${f}"
    done
    printf 'apply() {\n'
    pairs | while read -r f dst mode; do
        printf "  install -m %s -o root -g root '%s/%s.new' '%s' || return 1\n" "${mode}" "${STAGE_DIR}" "${f}" "${dst}"
    done
    printf '  nginx -t >/dev/null 2>&1 || { nginx -t 2>&1 | tail -n 3; echo "nginx -t rejected the new config"; return 1; }\n'
    printf '  systemctl daemon-reload || return 1\n'
    printf '  systemctl enable %s || return 1\n' "${UNIT}"
    printf '  systemctl restart %s || return 1\n' "${UNIT}"
    printf '  systemctl reload nginx || return 1\n}\n'
    printf "apply || { restore; echo 'install failed — previous copies restored, new-only files removed'; exit 1; }\n"
}

verify_script() {
    printf 'verify() {\n'
    printf "  sleep 2; s=\$(systemctl is-active %s || true); [ \"\$s\" = active ] || { echo \"%s is \$s\"; return 1; }\n" "${UNIT}" "${UNIT}"
    printf "  h=\$(curl -fsS '%s/healthz') || { echo '/healthz failed'; return 1; }; echo \"/healthz: \$h\"\n" "${MANAGE_URL}"
    printf "  c=\$(curl -sS -o /dev/null -w '%%{http_code}' '%s/manage/login'); [ \"\$c\" = 200 ] || { echo \"/manage/login -> \$c\"; return 1; }; echo '/manage/login -> 200'\n}\n" "${MANAGE_URL}"
}

deploy() {
    preflight
    stage
    say "installing on ${RELEASE_HOST}"
    remote "set -e
$(restore_script)
$(verify_script)
$(install_script)
verify || { restore; echo 'verify failed — previous copies restored, new-only files removed'; exit 1; }" \
        || fail "deploy failed on ${RELEASE_HOST} — see the lines above; the host is on its previous copies"
    ok "deployed ${NAME} (sha256 ${SUM}) to ${RELEASE_HOST}, verified at ${MANAGE_URL}"
}

rollback() {
    say "rolling back on ${RELEASE_HOST} from the .prev copies in ${STAGE_DIR}"
    remote "set -e
[ -f '${STAGE_DIR}/${NAME}.prev' ] || { echo 'no ${STAGE_DIR}/${NAME}.prev to roll back to'; exit 1; }
$(restore_script)
$(verify_script)
restore
verify" || fail "rollback failed on ${RELEASE_HOST} — read the service journal on the host"
    ok "rollback complete"
}

pick_markers
DRY_RUN=0; SKIP_VHOST=0; ROLLBACK=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY_RUN=1 ;;
        --skip-vhost) SKIP_VHOST=1 ;;
        --rollback) ROLLBACK=1 ;;
        -h|--help) usage; exit 0 ;;
        *) refuse_usage "unknown argument: $a" ;;
    esac
done

load_config
check_vhost_port
if [ "${ROLLBACK}" = 1 ]; then
    [ "${DRY_RUN}" = 1 ] && { ok "dry-run: rollback would restore the .prev copies in ${STAGE_DIR}; nothing done"; exit 0; }
    rollback; exit 0
fi
OUT="$(mktemp -d)"
trap 'rm -rf "${OUT}"' EXIT
build
render_units
render_vhost
print_plan
if [ "${DRY_RUN}" = 1 ]; then
    ok "dry-run: nothing copied, nothing installed, nothing restarted"
    exit 0
fi
for tool in ssh scp; do command -v "${tool}" >/dev/null 2>&1 || fail "${tool} is required"; done
deploy

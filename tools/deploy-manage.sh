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
HELPER_NAME=umbree-deploy-helper
HOST_ROOT=/usr/local/umbree-release
HELPER="${HOST_ROOT}/bin/${HELPER_NAME}"
MANAGE_BIN_DIR="${HOST_ROOT}/bin"
DEPLOY_VERBS="preflight install-binary render activate restore"
ROLLBACK_VERBS="status restore"

usage() {
    cat <<USAGE
usage: tools/deploy-manage.sh [--dry-run] [--rollback]
       tools/deploy-manage.sh --build-helper

  Builds ${NAME} from this checkout, stages only that binary on the release
  host as the operator's non-root SSH_USER, and drives the root-owned host
  helper (${HELPER}) through sudo -n:
  preflight, install-binary, render, activate, and restore on any failure.
  A deploy verifies /healthz, /manage/login and /umbree/version.json; a
  rollback verifies /healthz and /manage/login only.

  --dry-run       build, lint the units and vhost, print the plan; contacts no host
  --rollback      helper restore of the previous binary, units and vhost, then verify
  --build-helper  build the helper bundle (helper, templates, SHA256SUMS) into
                  dist/${HELPER_NAME}/; contacts no host, reads no deploy config

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
    check_clean_dir STAGE_DIR "${STAGE_DIR}"
}

load_config() {
    [ -f "${CONFIG}" ] || refuse_config "no deploy config at ${CONFIG} — create it (RELEASE_HOST, MANAGE_DATA_DIR, STATIC_DIR, …) or set \$UMBREE_MANAGE_DEPLOY_CONFIG"
    RELEASE_HOST="$(cfg_value RELEASE_HOST '')"
    SSH_USER="$(cfg_value SSH_USER '')"
    case "${SSH_USER}" in root|0) refuse_config "SSH_USER must not be root — the deploy elevates per step with sudo -n" ;; esac
    MANAGE_DATA_DIR="$(cfg_value MANAGE_DATA_DIR '')"
    STATIC_DIR="$(cfg_value STATIC_DIR '')"
    SERVICE_USER="$(cfg_value SERVICE_USER umbree-release)"
    MANAGE_ENV_FILE="$(cfg_value MANAGE_ENV_FILE /etc/umbree-release-manage/manage.env)"
    MANAGE_LISTEN="$(cfg_value MANAGE_LISTEN 127.0.0.1:8788)"
    MANAGE_URL="$(cfg_value MANAGE_URL https://release.umbree.org)"
    MANAGE_URL="${MANAGE_URL%/}"
    check_url MANAGE_URL "${MANAGE_URL}"
    STAGE_DIR="$(cfg_value STAGE_DIR "/home/${SSH_USER}/${NAME}-deploy")"
    check_paths
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
    say "plan for ${SSH_USER}@${RELEASE_HOST} from ${rev}:"
    printf '    sudoers    sudo -n -l %s <verb> for: %s\n' "${HELPER}" "${DEPLOY_VERBS}"
    printf '    preflight  sudo -n %s preflight\n' "${HELPER}"
    printf '    stage      %s/%s.new  (the only file copied)\n' "${STAGE_DIR}" "${NAME}"
    printf '    install    sudo -n %s install-binary, render, activate  (restore on failure)\n' "${HELPER}"
    printf '    verify     is-active %s; %s/healthz; %s/manage/login 200\n' "${UNIT}" "${MANAGE_URL}" "${MANAGE_URL}"
    printf '    verify     %s/umbree/version.json 200 naming umbree  (deploy only, not --rollback)\n' "${MANAGE_URL}"
    for u in ${UNITS}; do diff -u -L /dev/null -L "lint: ${u}" /dev/null "${OUT}/${u}" || true; done
}

remote() { ssh -o BatchMode=yes -o ConnectTimeout=10 "${SSH_USER}@${RELEASE_HOST}" "$1"; }

sudoers_check() {
    say "checking the sudoers rules for: $*"
    remote "for v in $*; do
  sudo -n -l '${HELPER}' \"\$v\" >/dev/null 2>&1 || { echo \"refuse: no sudoers rule for: sudo -n ${HELPER} \$v\"; exit 1; }
done" || fail "sudoers on ${RELEASE_HOST} is missing a helper verb (named above) — nothing was copied (ops/README.md §7)"
}

preflight() {
    sudoers_check "${DEPLOY_VERBS}"
    say "preflight on ${RELEASE_HOST}"
    remote "sudo -n '${HELPER}' preflight" || fail "helper preflight refused on ${RELEASE_HOST} — nothing was copied"
}

stage() {
    say "copying ${NAME} to ${SSH_USER}@${RELEASE_HOST}:${STAGE_DIR}/"
    remote "install -d -m 0750 '${STAGE_DIR}' && rm -f '${STAGE_DIR}/${NAME}.new'" || fail "could not prepare ${STAGE_DIR} on ${RELEASE_HOST} as ${SSH_USER}"
    scp -q "${OUT}/${NAME}" "${SSH_USER}@${RELEASE_HOST}:${STAGE_DIR}/${NAME}.new" || fail "scp of the binary failed"
}

verify_script() {
    printf 'verify() {\n'
    printf "  sleep 2; s=\$(systemctl is-active %s || true); [ \"\$s\" = active ] || { echo \"%s is \$s\"; return 1; }\n" "${UNIT}" "${UNIT}"
    printf "  h=\$(curl -fsS '%s/healthz') || { echo '/healthz failed'; return 1; }; echo \"/healthz: \$h\"\n" "${MANAGE_URL}"
    printf "  c=\$(curl -sS -o /dev/null -w '%%{http_code}' '%s/manage/login'); [ \"\$c\" = 200 ] || { echo \"/manage/login -> \$c\"; return 1; }; echo '/manage/login -> 200'\n}\n" "${MANAGE_URL}"
}

verify_json_script() {
    printf 'verify_json() {\n'
    printf "  b=\$(curl -sS -w '\\n%%{http_code}' '%s/umbree/version.json'); c=\$(printf '%%s' \"\$b\" | tail -n 1)\n" "${MANAGE_URL}"
    printf "  [ \"\$c\" = 200 ] || { echo \"/umbree/version.json -> \$c\"; return 1; }\n"
    printf "  case \"\$b\" in *'\"component\":\"umbree\"'*) echo '/umbree/version.json -> 200' ;; *) echo '/umbree/version.json -> 200 without umbree'; return 1 ;; esac\n}\n"
}

deploy_script() {
    verify_script
    verify_json_script
    printf "H='%s'\n" "${HELPER}"
    cat <<'REMOTE'
sudo -n "$H" install-binary || { echo "install-binary refused; nothing was changed"; exit 1; }
for v in render activate; do
  sudo -n "$H" "$v" || { echo "$v failed; restoring"; sudo -n "$H" restore; exit 1; }
done
verify || { echo "verify failed; restoring"; sudo -n "$H" restore; exit 1; }
verify_json || { echo "version.json verify failed; restoring"; sudo -n "$H" restore; exit 1; }
REMOTE
}

deploy() {
    preflight
    stage
    say "installing on ${RELEASE_HOST} through the helper"
    remote "$(deploy_script)" \
        || fail "deploy failed on ${RELEASE_HOST} — see the lines above; the helper restored the previous copies"
    ok "deployed ${NAME} (sha256 ${SUM}) to ${RELEASE_HOST}, verified at ${MANAGE_URL}"
}

rollback() {
    sudoers_check "${ROLLBACK_VERBS}"
    say "rolling back on ${SSH_USER}@${RELEASE_HOST} through the helper"
    remote "$(verify_script)
sudo -n '${HELPER}' status | grep -q '/${NAME}: previous copy held' || { echo 'the helper holds no previous binary to roll back to'; exit 1; }
sudo -n '${HELPER}' restore
verify" || fail "rollback failed on ${RELEASE_HOST} — read the service journal on the host"
    ok "rollback complete"
}

build_helper() {
    say "$(go version)"
    have="$(go env GOVERSION)"
    [ "${have}" = "${GO_PIN}" ] || fail "go is ${have}, the helper builds with ${GO_PIN} only — put ${GO_PIN} first on PATH"
    dest="${ROOT}/dist/${HELPER_NAME}"
    rm -rf "${dest}"; mkdir -p "${dest}/templates"
    ( cd "${ROOT}" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=local \
        go build -trimpath -o "${dest}/${HELPER_NAME}" "./cmd/${HELPER_NAME}" ) || fail "helper build failed"
    for u in ${UNITS}; do cp "${ROOT}/ops/systemd/${u}" "${dest}/templates/${u}"; done
    cp "${VHOST_SRC}" "${dest}/templates/${VHOST_NAME}"
    ( cd "${dest}" && for f in "${HELPER_NAME}" templates/*; do printf '%s  %s\n' "$(sha256_of "$f")" "$f"; done > SHA256SUMS )
    sed 's/^/    /' "${dest}/SHA256SUMS"
    ok "helper bundle in ${dest}: SHA256SUMS sha256 $(sha256_of "${dest}/SHA256SUMS") — nothing contacted"
}

pick_markers
DRY_RUN=0; ROLLBACK=0; BUILD_HELPER=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY_RUN=1 ;;
        --build-helper) BUILD_HELPER=1 ;;
        --rollback) ROLLBACK=1 ;;
        -h|--help) usage; exit 0 ;;
        *) refuse_usage "unknown argument: $a" ;;
    esac
done

if [ "${BUILD_HELPER}" = 1 ]; then
    [ $# -eq 1 ] || refuse_usage "--build-helper takes no other argument"
    build_helper; exit 0
fi
load_config
check_vhost_port
if [ "${ROLLBACK}" = 1 ]; then
    [ "${DRY_RUN}" = 1 ] && { ok "dry-run: rollback would run sudo -n ${HELPER} restore; nothing done"; exit 0; }
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

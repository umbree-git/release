#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CMD="${HERE}/release.command"
[ -r "${CMD}" ] || { echo "FAIL: ${CMD} not readable"; exit 1; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
fails=0

mkdir -p "${TMP}/bin"
cat > "${TMP}/bin/launchctl" <<'STUB'
#!/bin/sh
[ "$1" = "managername" ] && echo Aqua
STUB
chmod +x "${TMP}/bin/launchctl"

printf '%s\n' '# test env' > "${TMP}/env"

if script -q -c true /dev/null >/dev/null 2>&1; then
    with_pty() { script -q -c "$(printf '%q ' "$@")" /dev/null; }
else
    with_pty() { script -q /dev/null "$@"; }
fi

run_case() {
    local name="$1" body="$2" want="$3" req log out
    req="${TMP}/request"; log="${TMP}/log"
    rm -f "$log"
    if [ "$body" = "__MISSING__" ]; then
        rm -f "$req"
    else
        printf '%s\n' "$body" > "$req"
    fi

    with_pty env \
        PATH="${TMP}/bin:${PATH}" \
        RELEASE_ENV="${TMP}/env" \
        RELEASE_REQUEST="$req" \
        RELEASE_LOG="$log" \
        UMBREE_SRC_UMBREE="${UMBREE_SRC_UMBREE:-}" \
        SSH_CONNECTION= \
        bash "${CMD}" >/dev/null 2>&1
    out="$(cat "$log" 2>/dev/null)"

    if printf '%s' "$out" | grep -q "$want"; then
        echo "ok: ${name}"
    else
        echo "FAIL: ${name} — log does not contain '${want}'"
        printf '      got: %s\n' "$(printf '%s' "$out" | tr '\n' '|')"
        fails=1
    fi

    if printf '%s' "$out" | grep -q "rkit build\|── cut:"; then
        echo "FAIL: ${name} — reached the cut loop; a guard should have stopped it"
        fails=1
    fi
}

echo "# guards behind the session check"
run_case "unknown component is refused" \
    'COMPONENTS="umbreedd"' \
    "unknown component: umbreedd"

run_case "all is refused" \
    'COMPONENTS="all"' \
    'COMPONENTS="all" is not usable here'

run_case "empty COMPONENTS is refused" \
    'COMPONENTS=""' \
    "request names no COMPONENTS"

run_case "missing request file is refused" \
    "__MISSING__" \
    "request file not readable"

run_case "unknown channel is refused" \
    'COMPONENTS="umbree"
CHANNEL="bogus"' \
    "channel must be stable or beta"

echo "# the cut-origin guard runs before the build"
FIX="${TMP}/brand/cli/code/main"; mkdir -p "${FIX}"
git -C "${FIX}" init -q >/dev/null 2>&1
UMBREE_SRC_UMBREE="${FIX}" run_case "beta with no code/beta is refused before the build" \
    'COMPONENTS="umbree"
CHANNEL="beta"' \
    "beta worktree missing"
if grep -q "→ build" "${TMP}/log" 2>/dev/null; then
    echo "FAIL: beta refusal reached → build"; fails=1
else
    echo "ok: beta refusal happened before → build"
fi

echo "# the marker-subject check accepts a beta marker"
if grep -q '"\[RELEASED: ${comp}\]"\*|"\[RELEASED: ${comp} beta\]"\*)' "${CMD}"; then
    echo "ok: marker pattern accepts [RELEASED: <comp> beta]"
else
    echo "FAIL: marker pattern does not accept [RELEASED: <comp> beta]"; fails=1
fi

echo "# the sync-back check runs in the pre-flight (static: the launcher cannot reach it here)"
for want in 'check_sync_back "${comp}" "${src}" "${origin_mode}"' \
            'check_sync_back "release repo" "$REPO_ROOT" "${origin_mode}"'; do
    if grep -qF "${want}" "${CMD}"; then echo "ok: pre-flight calls ${want}"
    else echo "FAIL: pre-flight does not call ${want}"; fails=1; fi
done

echo "# the marker is carried into dev after its push (sync_marker_into_dev)"
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fails=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1" ;; *) echo "FAIL: $1 — '$2' does not contain '$3'"; fails=1 ;; esac; }
check_lacks() { case "$2" in *"$3"*) echo "FAIL: $1 — '$2' contains '$3'"; fails=1 ;; *) echo "ok: $1" ;; esac; }

PM_FN="$(awk '/^push_marker\(\) \{/,/^}/' "${CMD}")"
case "${PM_FN}" in
    *'say "✓ ${comp} marker pushed"'*'sync_marker_into_dev "${comp}"'*)
        echo "ok: push_marker syncs dev only after the marker push succeeded" ;;
    *) echo "FAIL: push_marker does not call sync_marker_into_dev after its push"; fails=1 ;;
esac
check "sync_marker_into_dev has exactly one call site" \
    "$(grep -c 'sync_marker_into_dev "\${comp}"' "${CMD}")" "1"

FNS=""
for fn in dev_sync_fix sync_marker_into_dev merge_marker_into_dev; do
    body="$(awk "/^${fn}\\(\\) \\{/,/^}/" "${CMD}")"
    check "${fn} extracted" "$([ -n "${body}" ] && echo yes)" "yes"
    FNS="${FNS}${body}
"
done
SAY_DIE="$(grep -E '^(say|die)\(\) \{' "${CMD}")"
check "say and die extracted" "$(printf '%s\n' "${SAY_DIE}" | grep -c .)" "2"
check_lacks "the dev sync never force-pushes (--force)" "${FNS}" "--force"
check_lacks "the dev sync never force-pushes (+refspec)" "${FNS}" 'push origin "+'
HARNESS="${TMP}/sync-harness.sh"
{
    echo 'set -uo pipefail'
    echo 'GIT="${GIT_UNDER_TEST:-/usr/bin/git}"'
    printf '%s\n' "${SAY_DIE}" "${FNS}"
    echo 'sync_marker_into_dev "$1"'
} > "${HARNESS}"

G=/usr/bin/git
export GIT_CONFIG_GLOBAL="${TMP}/gitconfig"
$G config --file "${GIT_CONFIG_GLOBAL}" user.name t
$G config --file "${GIT_CONFIG_GLOBAL}" user.email t@t.invalid
$G config --file "${GIT_CONFIG_GLOBAL}" init.defaultBranch main
fresh() {
    SB_BARE="${TMP}/$1.git"; SB="${TMP}/$1"
    $G init -q --bare "${SB_BARE}"
    $G clone -q "${SB_BARE}" "${SB}" 2>/dev/null
    printf 'base\n' > "${SB}/shared.txt"
    $G -C "${SB}" add shared.txt
    $G -C "${SB}" commit -q -m seed
    $G -C "${SB}" push -q -u origin main
    $G -C "${SB}" push -q origin main:refs/heads/dev
}
bare_ref() { $G -C "${SB_BARE}" rev-parse "refs/heads/$1"; }
push_as_marker() {
    printf '%s\n' "${2:-marker}" > "${SB}/$1"
    $G -C "${SB}" add "$1"
    $G -C "${SB}" commit -q -m "[RELEASED: umbree] $1"
    $G -C "${SB}" push -q origin HEAD:refs/heads/main
}
dev_clone() {
    local o; o="$(mktemp -d "${TMP}/devclone.XXXXXX")"
    $G clone -q "${SB_BARE}" "${o}/c" 2>/dev/null
    $G -C "${o}/c" checkout -q dev
    printf '%s' "${o}/c"
}
on_dev() {
    local o; o="$(dev_clone)"
    printf '%s\n' "$2" > "${o}/$1"
    $G -C "${o}" add "$1"
    $G -C "${o}" commit -q -m "dev: $1"
    $G -C "${o}" push -q origin dev
}
sync_run() {
    out="$(cd "${SB}" && env LOG="${TMP}/sync.log" CUT_DONE="umbreed umbree" "$@" bash "${HARNESS}" umbree 2>&1)"
    rc=$?
}

fresh sync-ff
push_as_marker m1.txt
sync_run
check "ff: returns 0" "${rc}" "0"
check_contains "ff: says it synced" "${out}" "✓ umbree marker synced into dev"
check "ff: origin dev IS the marker commit" "$(bare_ref dev)" "$($G -C "${SB}" rev-parse HEAD)"

dev_before="$(bare_ref dev)"
sync_run
check "no-op: returns 0" "${rc}" "0"
check_contains "no-op: says there is nothing to sync" "${out}" "umbree marker already in dev — nothing to sync"
check_lacks "no-op: does not claim a sync" "${out}" "into dev ("
check "no-op: origin dev untouched" "$(bare_ref dev)" "${dev_before}"

on_dev ahead.txt "unreleased"
dev_before="$(bare_ref dev)"
sync_run
check "no-op, dev ahead: returns 0" "${rc}" "0"
check_contains "no-op, dev ahead: says there is nothing to sync" "${out}" "already in dev — nothing to sync"
check "no-op, dev ahead: origin dev untouched" "$(bare_ref dev)" "${dev_before}"

fresh sync-merge
on_dev devwork.txt "unreleased"
dev_before="$(bare_ref dev)"
push_as_marker marker.txt
marker="$($G -C "${SB}" rev-parse HEAD)"
refs_before="$($G -C "${SB}" for-each-ref refs/heads)"
sync_run
merged="$(bare_ref dev)"
check "merge: returns 0" "${rc}" "0"
check_contains "merge: says it merged, with the short sha" "${out}" "✓ umbree marker merged into dev ($($G -C "${SB_BARE}" rev-parse --short "${merged}"))"
check "merge: dev first parent, main second" \
    "$($G -C "${SB_BARE}" rev-list --parents -n 1 "${merged}")" "${merged} ${dev_before} ${marker}"
check "merge: fixed message, nothing else in it" \
    "$($G -C "${SB_BARE}" log -1 --format=%B "${merged}")" "Merge branch 'main' into dev"
check "merge: dev's side is present" "$($G -C "${SB_BARE}" show "${merged}:devwork.txt")" "unreleased"
check "merge: main's side is present" "$($G -C "${SB_BARE}" show "${merged}:marker.txt")" "marker"
$G -C "${SB_BARE}" merge-base --is-ancestor refs/heads/main refs/heads/dev && r=0 || r=1
check "merge: dev contains main afterwards" "${r}" "0"
check "merge: main itself is untouched" "$(bare_ref main)" "${marker}"
check "merge: no local branch moved or appeared" "$($G -C "${SB}" for-each-ref refs/heads)" "${refs_before}"
check "merge: the checkout is untouched (HEAD, clean tree)" \
    "$($G -C "${SB}" rev-parse HEAD):$($G -C "${SB}" status --porcelain)" "${marker}:"

fresh sync-conflict
on_dev shared.txt "dev side"
dev_before="$(bare_ref dev)"
push_as_marker shared.txt "main side"
sync_run
check "conflict: returns 1" "${rc}" "1"
check_contains "conflict: names the conflicted path" "${out}" "conflicts in: shared.txt"
check_contains "conflict: says nothing was pushed" "${out}" "nothing was pushed to dev"
check_contains "conflict: names the merge, never a rebase" "${out}" "git merge — never a rebase"
check_contains "conflict: names the components to drop on re-run" "${out}" "re-run with umbreed umbree dropped from COMPONENTS"
check "conflict: origin dev untouched" "$(bare_ref dev)" "${dev_before}"

fresh sync-race
on_dev devwork.txt "unreleased"
push_as_marker marker.txt
RACE="$(dev_clone)"
printf 'raced\n' > "${RACE}/race.txt"
$G -C "${RACE}" add race.txt
$G -C "${RACE}" commit -q -m "dev moves mid-cut"
raced="$($G -C "${RACE}" rev-parse HEAD)"
cat > "${TMP}/git-race" <<EOF
#!/bin/sh
if [ "\$1" = push ] && [ ! -e "${TMP}/race.done" ]; then
    case "\$*" in *refs/heads/dev*) : > "${TMP}/race.done"; $G -C "${RACE}" push -q origin dev ;; esac
fi
exec $G "\$@"
EOF
chmod +x "${TMP}/git-race"
sync_run GIT_UNDER_TEST="${TMP}/git-race"
check "race: returns 1" "${rc}" "1"
check_contains "race: says dev moved during the cut" "${out}" "dev moved during the cut, and it is never forced"
check "race: origin dev is the raced commit, not overwritten" "$(bare_ref dev)" "${raced}"

fresh sync-gone
push_as_marker marker.txt
$G -C "${SB}" remote set-url origin "${TMP}/no-such-repo.git"
sync_run
check "fetch failure: returns 1" "${rc}" "1"
check_contains "fetch failure: says it could not fetch" "${out}" "cannot fetch origin/main and origin/dev"

fresh sync-oldgit
on_dev devwork.txt "unreleased"
dev_before="$(bare_ref dev)"
push_as_marker marker.txt
printf '#!/bin/sh\n[ "$1" = --version ] && { echo "git version 2.37.1"; exit 0; }\nexec %s "$@"\n' "$G" > "${TMP}/git-old"
chmod +x "${TMP}/git-old"
sync_run GIT_UNDER_TEST="${TMP}/git-old"
check "old git: returns 1" "${rc}" "1"
check_contains "old git: names the version needed and the one found" "${out}" "needs git 2.38 or newer (merge-tree --write-tree), and ${TMP}/git-old reports 'git version 2.37.1'"
check "old git: origin dev untouched" "$(bare_ref dev)" "${dev_before}"

echo "# a stable request needs the gated bucket from the sealed config"
RGC="$(awk '/^require_gated_config\(\) \{/,/^}/' "${CMD}")"
check "require_gated_config extracted" "$([ -n "${RGC}" ] && echo yes)" "yes"
GATED_HARNESS="${TMP}/gated-harness.sh"
{
    echo 'set -uo pipefail'
    echo 'LOG=/dev/null'
    printf '%s\n' "${SAY_DIE}" "${RGC}"
    echo 'require_gated_config'
    echo 'bash -c '"'"'printf "child sees %s\n" "${UMBREE_R2_GATED_BUCKET:-nothing}"'"'"
} > "${GATED_HARNESS}"
gated_run() { out="$(env -u UMBREE_R2_GATED_BUCKET "$@" bash "${GATED_HARNESS}" 2>&1)"; rc=$?; }
gated_run CHANNEL=stable
check "stable, bucket unset: refused" "${rc}" "1"
check_contains "…names UMBREE_R2_GATED_BUCKET" "${out}" "UMBREE_R2_GATED_BUCKET"
check_contains "…before anything is built" "${out}" "nothing built"
gated_run CHANNEL=stable UMBREE_R2_GATED_BUCKET=
check "stable, bucket empty: refused" "${rc}" "1"
gated_run CHANNEL=stable UMBREE_R2_GATED_BUCKET=gated-fixture
check "stable, bucket set: passes" "${rc}" "0"
check_contains "…and release.sh inherits it" "${out}" "child sees gated-fixture"
gated_run CHANNEL=beta
check "beta, bucket unset: passes (beta is held, not gated)" "${rc}" "0"
SEALED_AT="$(grep -n 'server-config.env.age")"' "${CMD}" | head -n1 | cut -d: -f1)"
CALL_AT="$(grep -n '^require_gated_config$' "${CMD}" | head -n1 | cut -d: -f1)"
BUILD_AT="$(grep -n 'say "→ build' "${CMD}" | head -n1 | cut -d: -f1)"
check "require_gated_config runs after the sealed config, before the build" \
    "$([ -n "${CALL_AT}" ] && [ "${SEALED_AT}" -lt "${CALL_AT}" ] && [ "${CALL_AT}" -lt "${BUILD_AT}" ] && echo between)" "between"

echo "# a stable request needs the manage URL from the sealed config"
RMC="$(awk '/^require_manage_config\(\) \{/,/^}/' "${CMD}")"
check "require_manage_config extracted" "$([ -n "${RMC}" ] && echo yes)" "yes"
MANAGE_HARNESS="${TMP}/manage-harness.sh"
{
    echo 'set -uo pipefail'
    echo 'LOG=/dev/null'
    printf '%s\n' "${SAY_DIE}" "${RMC}"
    echo 'require_manage_config'
    echo 'bash -c '"'"'printf "child sees %s\n" "${UMBREE_MANAGE_URL:-nothing}"'"'"
} > "${MANAGE_HARNESS}"
manage_run() { out="$(env -u UMBREE_MANAGE_URL "$@" bash "${MANAGE_HARNESS}" 2>&1)"; rc=$?; }
manage_run CHANNEL=stable
check "stable, manage URL unset: refused" "${rc}" "1"
check_contains "…names UMBREE_MANAGE_URL" "${out}" "UMBREE_MANAGE_URL"
check_contains "…before anything is built" "${out}" "nothing built"
manage_run CHANNEL=stable UMBREE_MANAGE_URL=http://manage.invalid
check "stable, plain-http manage URL: refused" "${rc}" "1"
manage_run CHANNEL=stable UMBREE_MANAGE_URL=https://manage.invalid
check "stable, https manage URL: passes" "${rc}" "0"
check_contains "…and release.sh inherits it" "${out}" "child sees https://manage.invalid"
manage_run CHANNEL=beta
check "beta, manage URL unset: passes (the dormant beta verb registers nothing)" "${rc}" "0"
RMC_AT="$(grep -n '^require_manage_config$' "${CMD}" | head -n1 | cut -d: -f1)"
check "require_manage_config runs after the sealed config, before the build" \
    "$([ -n "${RMC_AT}" ] && [ "${SEALED_AT}" -lt "${RMC_AT}" ] && [ "${RMC_AT}" -lt "${BUILD_AT}" ] && echo between)" "between"
KEY_AT="$(grep -n '^KEYFILE="$(umask 077; mktemp' "${CMD}" | head -n1 | cut -d: -f1)"
EXPORT_AT="$(grep -n '^export UMBREE_RELEASE_KEY="${KEYFILE}"$' "${CMD}" | head -n1 | cut -d: -f1)"
check "the decrypted key reaches release.sh as UMBREE_RELEASE_KEY, before the build" \
    "$([ -n "${KEY_AT}" ] && [ -n "${EXPORT_AT}" ] && [ "${KEY_AT}" -lt "${EXPORT_AT}" ] && [ "${EXPORT_AT}" -lt "${BUILD_AT}" ] && echo between)" "between"

echo "# launcher report names row not release page"
CR="$(awk '/^cut_report\(\) \{/,/^}/' "${CMD}")"
check "cut_report extracted" "$([ -n "${CR}" ] && echo yes)" "yes"
out="$( { echo 'LOG=/dev/null'; printf '%s\n' "${SAY_DIE}" "${CR}"; echo 'cut_report umbree v0.1.8.2026.08.31.46b36734'; } \
    | UMBREE_MANAGE_URL=https://manage.invalid/ bash 2>&1)"
check_contains "launcher report names row not release page" "${out}" "https://manage.invalid/manage/production/umbree"
check_contains "…names the stamp" "${out}" "v0.1.8.2026.08.31.46b36734"
check_contains "…says it is staged, not public" "${out}" "not public"
check_lacks "…names no release page" "${out}" "releases/tag"
check_lacks "…and no GitHub" "${out}" "github.com"
check "the launcher names no GitHub Release" "$(grep -c 'GitHub Release' "${CMD}")" "0"
check "cut_report is called once, after the marker push" "$(grep -c '^ *cut_report "${comp}" "${stamp}"$' "${CMD}")" "1"
PUSH_AT="$(grep -n '^ *push_marker "${comp}"$' "${CMD}" | head -n1 | cut -d: -f1)"
REPORT_AT="$(grep -n '^ *cut_report "${comp}" "${stamp}"$' "${CMD}" | head -n1 | cut -d: -f1)"
check "…in the cut loop, after push_marker" "$([ -n "${PUSH_AT}" ] && [ -n "${REPORT_AT}" ] && [ "${PUSH_AT}" -lt "${REPORT_AT}" ] && echo after)" "after"

echo "# the session guard itself, unstubbed"
sess_log="${TMP}/sess.log"
RELEASE_ENV="${TMP}/env" RELEASE_REQUEST="${TMP}/none" RELEASE_LOG="$sess_log" \
    bash "${CMD}" >/dev/null 2>&1
if grep -q "session-domain:" "$sess_log" 2>/dev/null && ! grep -q "── cut:" "$sess_log" 2>/dev/null; then
    echo "ok: session guard reports the domain and does not reach the cut"
else
    echo "FAIL: session guard did not report a domain, or reached the cut"
    fails=1
fi

echo "# the exit sentinel is always emitted"
if grep -q "^RELEASE-EXIT:" "$sess_log" 2>/dev/null; then
    echo "ok: RELEASE-EXIT sentinel present on a refusal"
else
    echo "FAIL: no RELEASE-EXIT sentinel — a watcher would block forever"
    fails=1
fi

[ "$fails" -eq 0 ] || { echo; echo "FAILURES"; exit 1; }
echo
echo "ALL OK"

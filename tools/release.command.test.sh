#!/usr/bin/env bash
# release.command.test.sh — proves tools/release.command refuses before it cuts.
#
# The launcher's whole value is its refusals: a cut that starts in the wrong
# session, or on a component nobody meant to publish, is expensive and public.
# Every case below must die at a guard; none may reach `rkit build`.
#
# Two things make this testable. The session check reads `launchctl managername`,
# so a stub earlier on PATH stands in for a desktop session — that is also why
# the stub is the FIRST thing each case installs. And `[ -t 0 ]` demands a real
# terminal, so cases run under `script`, which supplies a pty.
#
# The session-domain refusal itself is NOT stubbed here: it is proven directly by
# running the launcher from an ordinary shell, where it must refuse on its own.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CMD="${HERE}/release.command"
[ -r "${CMD}" ] || { echo "FAIL: ${CMD} not readable"; exit 1; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
fails=0

# stub launchctl → a desktop session, so cases get PAST guard 1 to the ones under test
mkdir -p "${TMP}/bin"
cat > "${TMP}/bin/launchctl" <<'STUB'
#!/bin/sh
[ "$1" = "managername" ] && echo Aqua
STUB
chmod +x "${TMP}/bin/launchctl"

# a minimal env file: the launcher only requires it to be readable and sourceable
printf '%s\n' '# test env' > "${TMP}/env"

# with_pty <cmd…> — run under `script` so `[ -t 0 ]` passes. BSD script (macOS)
# takes the command as trailing arguments; util-linux script (Linux, where the
# CI machine runs this) takes it as one -c string. Detect by trying the flag
# the other one lacks.
if script -q -c true /dev/null >/dev/null 2>&1; then
    with_pty() { script -q -c "$(printf '%q ' "$@")" /dev/null; }
else
    with_pty() { script -q /dev/null "$@"; }
fi

# run_case <name> <request-body|__MISSING__> <expected-substring>
run_case() {
    local name="$1" body="$2" want="$3" req log out
    req="${TMP}/request"; log="${TMP}/log"
    rm -f "$log"
    if [ "$body" = "__MISSING__" ]; then
        rm -f "$req"
    else
        printf '%s\n' "$body" > "$req"
    fi

    # script gives a pty so `[ -t 0 ]` passes; PATH puts the launchctl stub first.
    # SSH_CONNECTION is cleared so the cases behind the session guard are
    # reachable when this suite itself runs over ssh (the CI machine); the
    # unstubbed session-guard case below still proves that guard.
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

    # No case may have started a build. dist/ is the only thing a build creates.
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
# A beta request whose derived code/beta does not exist: the launcher must
# refuse at the origin guard, before the sealed inputs and before `→ build`.
# UMBREE_SRC_UMBREE points at a fixture registry main with no beta sibling;
# the launcher derives its beta path from BRAND_ROOT (this repo's own
# siblings), so the refusal names whichever of the two is missing — either
# way it names "beta worktree missing" and never reaches the cut loop.
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
# push_marker is reached through a case on HEAD's subject; a beta publish
# writes "[RELEASED: <comp> beta] …", which the stable-only pattern would
# have treated as "not a marker" and died on AFTER a successful publish.
# GIT is hardcoded to /usr/bin/git, so this is pinned statically.
if grep -q '"\[RELEASED: ${comp}\]"\*|"\[RELEASED: ${comp} beta\]"\*)' "${CMD}"; then
    echo "ok: marker pattern accepts [RELEASED: <comp> beta]"
else
    echo "FAIL: marker pattern does not accept [RELEASED: <comp> beta]"; fails=1
fi

echo "# the sync-back check runs in the pre-flight (static: the launcher cannot reach it here)"
# Both calls sit in 5b beside assert_release_origin, whose real paths this
# suite cannot satisfy (they derive from this repo's own brand root), so the
# wiring is pinned by text; check_sync_back's behaviour is release_origin.test.sh's.
for want in 'check_sync_back "${comp}" "${src}" "${origin_mode}"' \
            'check_sync_back "release repo" "$REPO_ROOT" "${origin_mode}"'; do
    if grep -qF "${want}" "${CMD}"; then echo "ok: pre-flight calls ${want}"
    else echo "FAIL: pre-flight does not call ${want}"; fails=1; fi
done

echo "# the marker is carried into dev after its push (sync_marker_into_dev)"
# Clawee 2026-09-14: its guard shipped without this, the clawee cut pushed its
# marker, and claweed refused on the missing sync-back.
#
# The function is EXTRACTED, never copied: awk takes it from its definition to
# the first `}` alone in column 0 (keep that shape), and say/die are the
# launcher's own one-liners, so this runs the code that ships.
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fails=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1" ;; *) echo "FAIL: $1 — '$2' does not contain '$3'"; fails=1 ;; esac; }
check_lacks() { case "$2" in *"$3"*) echo "FAIL: $1 — '$2' contains '$3'"; fails=1 ;; *) echo "ok: $1" ;; esac; }

PM_FN="$(awk '/^push_marker\(\) \{/,/^}/' "${CMD}")"
case "${PM_FN}" in
    *'say "✓ ${comp} marker pushed"'*'sync_marker_into_dev "${comp}"'*)
        echo "ok: push_marker syncs dev only after the marker push succeeded" ;;
    *) echo "FAIL: push_marker does not call sync_marker_into_dev after its push"; fails=1 ;;
esac
# One call site, inside push_marker: so --dry-run (continues before it) and the
# identical-stamp path (never calls push_marker) cannot reach the sync.
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
# Never forced, in any spelling, on any path.
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
export GIT_CONFIG_GLOBAL="${TMP}/gitconfig"   # keep the operator's identity/hooks out
$G config --file "${GIT_CONFIG_GLOBAL}" user.name t
$G config --file "${GIT_CONFIG_GLOBAL}" user.email t@t.invalid
$G config --file "${GIT_CONFIG_GLOBAL}" init.defaultBranch main
# fresh <name> — a bare origin and the release-repo clone, main == dev, with
# shared.txt committed on both. Sets SB_BARE and SB.
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
# push_as_marker <file> [content] — what push_marker has just done when it
# calls the sync: a marker commit on main, pushed.
push_as_marker() {
    printf '%s\n' "${2:-marker}" > "${SB}/$1"
    $G -C "${SB}" add "$1"
    $G -C "${SB}" commit -q -m "[RELEASED: umbree] $1"
    $G -C "${SB}" push -q origin HEAD:refs/heads/main
}
# dev_clone — a second clone on dev, where unreleased work is made. Prints it.
dev_clone() {
    local o; o="$(mktemp -d "${TMP}/devclone.XXXXXX")"
    $G clone -q "${SB_BARE}" "${o}/c" 2>/dev/null
    $G -C "${o}/c" checkout -q dev
    printf '%s' "${o}/c"
}
# on_dev <file> <content> — unreleased work pushed to dev from another clone.
on_dev() {
    local o; o="$(dev_clone)"
    printf '%s\n' "$2" > "${o}/$1"
    $G -C "${o}" add "$1"
    $G -C "${o}" commit -q -m "dev: $1"
    $G -C "${o}" push -q origin dev
}
# sync_run [VAR=value…] — the extracted functions, in the release repo, right
# after the marker push, as push_marker calls them.
sync_run() {
    out="$(cd "${SB}" && env LOG="${TMP}/sync.log" CUT_DONE="umbreed umbree" "$@" bash "${HARNESS}" umbree 2>&1)"
    rc=$?
}

# (1) dev is an ancestor of the marker → fast-forward it.
fresh sync-ff
push_as_marker m1.txt
sync_run
check "ff: returns 0" "${rc}" "0"
check_contains "ff: says it synced" "${out}" "✓ umbree marker synced into dev"
check "ff: origin dev IS the marker commit" "$(bare_ref dev)" "$($G -C "${SB}" rev-parse HEAD)"

# (2) the marker is already in dev → nothing written.
dev_before="$(bare_ref dev)"
sync_run
check "no-op: returns 0" "${rc}" "0"
check_contains "no-op: says there is nothing to sync" "${out}" "umbree marker already in dev — nothing to sync"
check_lacks "no-op: does not claim a sync" "${out}" "into dev ("
check "no-op: origin dev untouched" "$(bare_ref dev)" "${dev_before}"

# (2b) dev AHEAD of main, and already containing the marker → still nothing.
on_dev ahead.txt "unreleased"
dev_before="$(bare_ref dev)"
sync_run
check "no-op, dev ahead: returns 0" "${rc}" "0"
check_contains "no-op, dev ahead: says there is nothing to sync" "${out}" "already in dev — nothing to sync"
check "no-op, dev ahead: origin dev untouched" "$(bare_ref dev)" "${dev_before}"

# (3) dev has diverged (unreleased work main lacks) → merged into dev.
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

# (4) the merge conflicts → dies naming the path, nothing pushed.
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

# (5) dev moves DURING the cut → the merge push is rejected → dies, never forced.
# A git wrapper plays the race for real: just before the launcher's push to
# dev, another clone pushes a new dev commit, so the merge (built on the dev
# the launcher fetched) is no longer a fast-forward of the remote.
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

# (6) origin unreachable after the push → dies; never reports a sync it did not do.
fresh sync-gone
push_as_marker marker.txt
$G -C "${SB}" remote set-url origin "${TMP}/no-such-repo.git"
sync_run
check "fetch failure: returns 1" "${rc}" "1"
check_contains "fetch failure: says it could not fetch" "${out}" "cannot fetch origin/main and origin/dev"

# (7) a git too old for merge-tree --write-tree → dies naming the version, nothing pushed.
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

echo "# the session guard itself, unstubbed"
# No stub on PATH: run from this ordinary shell. In CI or an agent session this
# is not Aqua and must refuse; if this ever runs FROM a desktop session it would
# pass guard 1 legitimately, so accept either refusal or a later guard firing —
# what must never happen is reaching the cut.
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

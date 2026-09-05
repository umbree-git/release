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

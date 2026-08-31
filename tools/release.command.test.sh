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
    script -q /dev/null env \
        PATH="${TMP}/bin:${PATH}" \
        RELEASE_ENV="${TMP}/env" \
        RELEASE_REQUEST="$req" \
        RELEASE_LOG="$log" \
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

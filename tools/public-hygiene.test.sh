#!/usr/bin/env bash
# public-hygiene.test.sh — proves the gate actually catches each class it claims.
#
# A hygiene gate that passes everything is worse than none: it converts "nobody
# checked" into "something checked and it was fine". So each pattern is proven
# against a file that trips it, in a scratch repo — never by editing this one.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GATE="${HERE}/public-hygiene.sh"
[ -r "${GATE}" ] || { echo "FAIL: ${GATE} not readable"; exit 1; }

GIT=/usr/bin/git
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
fails=0

# A scratch repo, so a probe file is TRACKED (the gate scans tracked files only)
# without touching the real one.
mkdir -p "${TMP}/repo/tools"
cp "${GATE}" "${TMP}/repo/tools/public-hygiene.sh"
cd "${TMP}/repo" || exit 1
$GIT init -q .
$GIT config user.email t@t; $GIT config user.name t

# probe <name> <file-content> <expect: catch|pass>
probe() {
    local name="$1" body="$2" expect="$3" out rc
    printf '%s\n' "$body" > probe.md
    $GIT add -A >/dev/null 2>&1
    out="$(bash tools/public-hygiene.sh 2>&1)"; rc=$?
    if [ "$expect" = "catch" ]; then
        if [ "$rc" -ne 0 ] && printf '%s' "$out" | grep -q "probe.md"; then
            echo "ok: caught ${name}"
        else
            echo "FAIL: did NOT catch ${name} (exit ${rc})"
            fails=1
        fi
    else
        if [ "$rc" -eq 0 ]; then
            echo "ok: allowed ${name}"
        else
            echo "FAIL: wrongly flagged ${name}"
            printf '%s\n' "$out" | sed 's/^/      /'
            fails=1
        fi
    fi
    rm -f probe.md; $GIT add -A >/dev/null 2>&1
}

echo "# each forbidden class is caught"
probe "internal GitHub wrapper"  'Call gh via ~/bin/ghp, never bare gh.'            catch
probe "private secrets repo"     'sealed in umbree-git/release.dp (private)'        catch
probe "internal policy tree"     'source ~/.agents/local/release.env'               catch
probe "release env file"         'release.env sets PATH'                            catch
probe "decrypt identity"         'age -d -i ~/.age/umbree-release.txt'              catch
probe "production host"          'Host: nsm.renative.com fronts everything'         catch
probe "production static path"   'root /ebs_storage/apps/x/static;'                 catch
probe "operator machine path"    'src = /Volumes/MacintoshED/Workstation/Coding/x'  catch

echo "# ordinary content is not flagged"
probe "plain prose"              'Install with the published bootstrap over TLS.'   pass
probe "public domain name"       'curl -fsSL https://release.umbree.org/x/install.sh | sh' pass
probe "generic env var names"    'Set RELEASE_HOST and STATIC_DIR before publishing.' pass

echo "# a clean tree passes"
$GIT add -A >/dev/null 2>&1
if bash tools/public-hygiene.sh >/dev/null 2>&1; then
    echo "ok: clean tree passes"
else
    echo "FAIL: clean tree does not pass"
    fails=1
fi

[ "$fails" -eq 0 ] || { echo; echo "FAILURES"; exit 1; }
echo
echo "ALL OK"

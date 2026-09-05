#!/usr/bin/env bash
# gen-version-jsonp.test.sh — the beta twin of version.js: beta.version.js is
# rendered ONLY while versions/<comp>.beta.stamp exists (from the beta catalog,
# with the local beta files as the offline fallback), is removed when it goes,
# and a stable run never writes or removes it. Runs against a scratch copy;
# curl is stubbed on PATH so no network is reached.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — missing '$3' in: $2"; fail=1;; esac; }

W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
mkdir -p "$W/repo/tools" "$W/repo/versions" "$W/bin"
cp "$HERE/gen-version-jsonp.sh" "$W/repo/tools/"
# curl stub: answers the beta catalog with a fixed manifest, 404s everything else.
cat > "$W/bin/curl" <<'EOF'
#!/bin/sh
for a in "$@"; do case "$a" in
  */umbree/beta/latest.json) printf '{\n  "component": "umbree",\n  "stamp": "v0.2.0.beta.2026.09.05.deadbeef",\n  "version": "0.2.0"\n}\n'; exit 0 ;;
esac; done
exit 22
EOF
chmod +x "$W/bin/curl"
export PATH="$W/bin:$PATH"
G="$W/repo/tools/gen-version-jsonp.sh"
printf '0.1.8\n' > "$W/repo/versions/umbree"
printf 'v0.1.8.2026.08.31.46b36734\n' > "$W/repo/versions/umbree.stamp"
printf '0.2.0\n' > "$W/repo/versions/umbree.beta"

echo "# no beta stamp"
out="$(sh "$G" --channel beta umbree 2>&1)"; r=$?
check "beta run without the stamp → 0" "$r" "0"
check_contains "…says not rendered" "$out" "beta.version.js not rendered"
[ ! -e "$W/repo/umbree/beta.version.js" ] && echo "ok: no beta.version.js written" || { echo "FAIL: beta.version.js written without a stamp"; fail=1; }

echo "# stable run (catalog unreachable → local fallback, with the stable stamp)"
sh "$G" umbree >/dev/null 2>&1; r=$?
check "stable run → 0" "$r" "0"
check "stable version.js from the local files" "$(cat "$W/repo/umbree/version.js")" '__umbreeVersion({"component":"umbree","version":"0.1.8","stamp":"v0.1.8.2026.08.31.46b36734"});'

echo "# beta stamp present"
printf 'v0.2.0.beta.2026.09.05.deadbeef\n' > "$W/repo/versions/umbree.beta.stamp"
sh "$G" --channel beta umbree >/dev/null 2>&1; r=$?
check "beta run with the stamp → 0" "$r" "0"
check "beta.version.js from the beta catalog" "$(cat "$W/repo/umbree/beta.version.js")" '__umbreeVersion({"component":"umbree","version":"0.2.0","stamp":"v0.2.0.beta.2026.09.05.deadbeef"});'
check "version.js untouched by the beta run" "$(cat "$W/repo/umbree/version.js")" '__umbreeVersion({"component":"umbree","version":"0.1.8","stamp":"v0.1.8.2026.08.31.46b36734"});'

echo "# offline beta fallback never reads versions/umbree"
UMBREE_R2_DOWNLOADS_BASE= sh "$G" --channel beta umbree >/dev/null 2>&1; r=$?
check "beta run with the mirror disabled → 0" "$r" "0"
check "…falls back to versions/umbree.beta + .beta.stamp" "$(cat "$W/repo/umbree/beta.version.js")" '__umbreeVersion({"component":"umbree","version":"0.2.0","stamp":"v0.2.0.beta.2026.09.05.deadbeef"});'

echo "# stable run never touches the twin; the sweep does"
sh "$G" umbree >/dev/null 2>&1
[ -f "$W/repo/umbree/beta.version.js" ] && echo "ok: stable run left beta.version.js alone" || { echo "FAIL: stable run removed beta.version.js"; fail=1; }
rm -f "$W/repo/versions/umbree.beta.stamp"
out="$(sh "$G" --channel beta umbree 2>&1)"
check_contains "sweep says removed stale" "$out" "removed stale: beta.version.js"
[ ! -e "$W/repo/umbree/beta.version.js" ] && echo "ok: beta.version.js removed" || { echo "FAIL: beta.version.js survived the sweep"; fail=1; }

echo "# bare invocation skips the .beta/.stamp companions"
out="$(sh "$G" 2>&1)"; r=$?
check "bare run → 0" "$r" "0"
check_contains "…renders umbree only" "$out" "wrote $W/repo/umbree/version.js"
case "$out" in *"unknown component"*) echo "FAIL: a companion file was taken for a component"; fail=1;; *) echo "ok: no companion taken for a component";; esac
sh "$G" --channel bogus umbree >/dev/null 2>&1; r=$?
check "--channel bogus → 2" "$r" "2"

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

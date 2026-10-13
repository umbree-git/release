#!/bin/sh
set -eu
HERE="$(cd "$(dirname "$0")/.." && pwd)"
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
die() { printf '\n✗ FAILED: %s\n' "$*" >&2; exit 1; }

mk() {
    mkdir -p "$1/tools/modules"
    printf '# module: %s  %s\n# since:  2026-08-25\n%s\n' "$2" "$3" "$4" > "$1/tools/modules/$2.sh"
}
mk "$W/local"  verify-checksum v2 'echo old'
mk "$W/remote" verify-checksum v3 'echo new'
mk "$W/local"  helpers v1 'echo same'
mk "$W/remote" helpers v1 'echo same'
mk "$W/local"  download v1 'echo mine'
mk "$W/remote" download v1 'echo theirs'
mk "$W/local"  sha256 v2 'echo newer-here'
mk "$W/remote" sha256 v1 'echo older-there'
mk "$W/remote" platform-detect v1 'echo fresh'
mk "$W/remote" download-r2-only v1 'echo relay-only'
mk "$W/local"  version-resolve v1 'echo local-fork-lives-in-the-template'
mk "$W/remote" version-resolve v2 'echo upstream-fixed-a-real-bug'
cat > "$W/local/tools/modules/MODULES.exclude" <<'EOF'
# module            why this product does not carry it
download-r2-only   gated relay channel this product does not have
version-resolve    local fork in tools/bootstrap.template.sh
EOF

set +e
out="$(sh "$HERE/tools/sync-modules.sh" --repo "$W/local" "$W/remote")"
rc=$?
set -e
[ "$rc" -eq 1 ] || die "expected exit 1 for a LOCAL FORK conflict, got exit $rc: $out"
case "$out" in *"verify-checksum"*"v2"*"-> v3"*"UPDATED"*) ;; *) die "no UPDATED verdict: $out" ;; esac
case "$out" in *"helpers"*"ok"*)                        ;; *) die "no ok verdict: $out" ;; esac
case "$out" in *"download"*"LOCAL FORK"*)               ;; *) die "no LOCAL FORK verdict: $out" ;; esac
case "$out" in *"sha256"*"AHEAD"*)                      ;; *) die "no AHEAD verdict: $out" ;; esac
case "$out" in *"platform-detect"*"NEW"*)               ;; *) die "no NEW verdict: $out" ;; esac
case "$out" in *"download-r2-only"*"NOT CARRIED"*)      ;; *) die "an excluded remote-only module was not reported NOT CARRIED: $out" ;; esac
case "$out" in *"version-resolve"*"NOT CARRIED"*)       ;; *) die "an excluded carried module was not reported NOT CARRIED: $out" ;; esac
case "$out" in *"version-resolve"*"UPDATED"*) die "an excluded module was reported UPDATED: $out" ;; esac
grep -q 'echo new'      "$W/local/tools/modules/verify-checksum.sh" || die "UPDATED module was not copied"
grep -q 'echo mine'     "$W/local/tools/modules/download.sh"        || die "LOCAL FORK was overwritten — it must never be"
grep -q 'echo newer-here' "$W/local/tools/modules/sha256.sh"        || die "AHEAD module was overwritten"
grep -q 'echo fresh'    "$W/local/tools/modules/platform-detect.sh" || die "NEW module was not copied"
[ ! -e "$W/local/tools/modules/download-r2-only.sh" ] \
    || die "the NEW path copied in a module recorded in MODULES.exclude"
grep -q 'echo local-fork-lives-in-the-template' "$W/local/tools/modules/version-resolve.sh" \
    || die "an excluded module was overwritten by the UPDATED path"
printf '\nALL OK — six verdicts; only UPDATED and NEW copy, and neither touches an excluded module\n'

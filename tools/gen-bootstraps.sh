#!/bin/sh
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
TEMPLATE="$ROOT/tools/bootstrap.template.sh"
[ -f "$TEMPLATE" ] || { echo "✗ missing template: $TEMPLATE" >&2; exit 1; }

MODDIR="$ROOT/tools/modules"

usage() {
    cat <<'EOF'
Usage: tools/gen-bootstraps.sh [--test-build <dir>]

Render <comp>/install.sh for every component from tools/bootstrap.template.sh,
and <comp>/beta.install.sh while versions/<comp>.beta.stamp exists. A stale
twin is removed when its stamp is gone.

  --test-build <dir>  render into <dir>/<comp>/ instead of the repository, with
                      the test seam filled: UMBREE_TEST_ALLOW_HTTP=1 accepts an
                      http://127.0.0.1:<port> downloads base and UMBREE_DL_BASE
                      overrides the per-release download base. <dir> must exist
                      and resolve (pwd -P) outside the repository. A test build
                      is never published.
  -h, --help          print this text

Environment:
  UMBREE_PUBKEY_FILE        minisign public key to bake (default umbree-release.pub,
                            then tools/testkeys/test.pub)
  UMBREE_MIN_VERSION        test-only override of the baked version floor
  UMBREE_MIN_VERSION_FILE   the file the floor is read from (default
                            versions/<comp>.stamp)
  UMBREE_R2_DOWNLOADS_BASE  the downloads base to bake; it must be https://
EOF
}

TEST_SEAM='DL_BASE="${UMBREE_DL_BASE:-}"; if [ "${UMBREE_TEST_ALLOW_HTTP:-}" = 1 ]; then ALLOW_LOOPBACK_HTTP=1; fi; case "$DL_BASE$ALLOW_LOOPBACK_HTTP" in 0) ;; *) CURL="curl -fsSL --proto =http --proto-redir =http --connect-timeout 15 --max-time 60" ;; esac'

resolve_test_dir() {
    [ -d "$1" ] || { echo "✗ --test-build: $1 is not a directory" >&2; return 2; }
    _td="$(CDPATH='' cd -- "$1" && pwd -P)"
    _root="$(CDPATH='' cd -- "$ROOT" && pwd -P)"
    case "$_td/" in
        "$_root"/*) echo "✗ --test-build: $1 resolves to $_td, inside the repository ($_root); a test build never writes into the tree" >&2; return 2 ;;
    esac
    printf '%s' "$_td"
}

OUT_ROOT="$ROOT"
SEAM=""
while [ $# -gt 0 ]; do
    case "$1" in
        -h|--help) usage; exit 0 ;;
        --test-build)
            [ $# -ge 2 ] || { { echo "✗ --test-build needs a directory"; echo; usage; } >&2; exit 2; }
            OUT_ROOT="$(resolve_test_dir "$2")" || exit 2
            SEAM="$TEST_SEAM"
            shift 2 ;;
        *) { echo "✗ unknown argument: $1"; echo; usage; } >&2; exit 2 ;;
    esac
done

MODULE_AWK='
        function scan(s, st,    i, n, c, prev) {
            n = length(s); prev = " "
            for (i = 1; i <= n; i++) {
                c = substr(s, i, 1)
                if (st == "s") { if (c == sq) st = "n"; prev = c; continue }
                if (c == "\\") { i++; prev = "x"; continue }
                if (st == "d") { if (c == "\"") st = "n"; prev = c; continue }
                if (c == sq) { st = "s"; prev = c; continue }
                if (c == "\"") { st = "d"; prev = c; continue }
                if (c == "#" && index(" \t;&|()", prev) > 0) return st
                if (c == "<" && substr(s, i + 1, 1) == "<") return "h"
                prev = c
            }
            return st
        }
        function untail(line,    h, rest) {
            h = index(line, "#")
            if (substr(line, h) ~ /^# (BEGIN|END) /) return line
            rest = substr(line, h + 1)
            if (match(rest, /[ \t]+#/)) return substr(line, 1, h + RSTART - 1)
            return line
        }
        function kept(line) {
            return line ~ /^[ \t]*#[ \t]*(shellcheck[ \t]+[^ \t]+=|noqa|type:|pragma:|exempt(\([a-z0-9][a-z0-9-]*\))?:[ \t]*[^ \t]|channel-(literal|word)-ok:[ \t]*[^ \t])/ \
                || line ~ /^# (BEGIN|END)( shared)? [a-z0-9][a-z0-9-]*([ \t]|$)/
        }
'

expand_includes() {
    awk -v moddir="$MODDIR" -v sq="'" "$MODULE_AWK"'
        /^@INCLUDE:[a-z0-9-]+@$/ {
            name = substr($0, 10, length($0) - 9 - 1)
            path = moddir "/" name ".sh"
            if ((getline probe < path) < 0) {
                printf("✗ @INCLUDE:%s@ but %s does not exist\n", name, path) > "/dev/stderr"
                exit 1
            }
            close(path)
            printf("# BEGIN %s\n", name)
            st = "n"
            while ((getline line < path) > 0) {
                if (line ~ /^# (module|needs|since):/) continue
                if (st == "n" && line ~ /^[ \t]*#/) {
                    if (!kept(line)) continue
                    line = untail(line)
                }
                st = scan(line, st)
                if (st == "h") {
                    printf("✗ module %s has a heredoc, so its comment lines cannot be told from heredoc text: %s\n", name, line) > "/dev/stderr"
                    exit 1
                }
                print line
            }
            close(path)
            printf("# END %s\n", name)
            next
        }
        { print }
    ' "$1"
}

pubfile=""
for cand in "${UMBREE_PUBKEY_FILE:-}" "$ROOT/umbree-release.pub" "$ROOT/tools/testkeys/test.pub"; do
    [ -n "$cand" ] || continue
    if [ -f "$cand" ]; then pubfile="$cand"; break; fi
done

if [ -n "$pubfile" ]; then
    PUBKEY="$(grep -v '^untrusted comment:' "$pubfile" | grep -v '^[[:space:]]*$' | tail -n1)"
    [ -n "$PUBKEY" ] || { echo "✗ could not extract a pubkey line from $pubfile" >&2; exit 1; }
    echo "→ baking pubkey from: $pubfile"
else
    PUBKEY="RWTEMP_PLACEHOLDER_REGENERATE_AFTER_ACTIVATION_xxxxxxxxxxxx"
    echo "! no pubkey file found (umbree-release.pub / tools/testkeys/test.pub)" >&2
    echo "! baking a TEMP placeholder — generated bootstrap will REFUSE to run." >&2
    echo "! create the key (age-seal + activation) and re-run." >&2
fi

min_version_of() {
    _mv_comp="$1"
    if [ -n "${UMBREE_MIN_VERSION:-}" ]; then
        _mv="${UMBREE_MIN_VERSION}"
        _mv_src="\$UMBREE_MIN_VERSION"
    else
        _mv_file="${UMBREE_MIN_VERSION_FILE:-$ROOT/versions/${_mv_comp}.stamp}"
        [ -f "$_mv_file" ] \
            || { echo "✗ missing $_mv_file — cannot bake ${_mv_comp}'s version floor (cut the component, or set UMBREE_MIN_VERSION for a test render)" >&2; exit 1; }
        _mv="$(tr -d '[:space:]' < "$_mv_file")"
        _mv_src="$_mv_file"
    fi
    case "${_mv#v}" in
        [0-9]*.[0-9]*.[0-9]*) : ;;
        *) echo "✗ ${_mv_src} holds '${_mv}', which has no numeric X.Y.Z prefix — refusing to bake an uncomparable version floor" >&2; exit 1 ;;
    esac
    printf '%s' "$_mv"
}

DOWNLOADS_BASE="${UMBREE_R2_DOWNLOADS_BASE-https://downloads.umbree.org}"
case "$DOWNLOADS_BASE" in
    https://*) ;;
    *) echo "✗ UMBREE_R2_DOWNLOADS_BASE must be an https:// base (got '$DOWNLOADS_BASE'); the installers download from nowhere else" >&2; exit 1 ;;
esac

COMPONENTS="$(cd "$ROOT" && go run ./cmd/rkit components)" || {
    echo "gen-bootstraps: could not read the component list from rkit" >&2
    exit 1
}
[ -n "$COMPONENTS" ] || { echo "gen-bootstraps: rkit returned no components" >&2; exit 1; }

render() {
    comp="$1"; channel="$2"; min_version="$3"; out="$4"
    tmp="$out.tmp.$$"
    exp="$out.exp.$$"
    expand_includes "$TEMPLATE" > "$exp"
    sed -e "s|@COMP@|$comp|g" -e "s|@PUBKEY@|$PUBKEY|g" \
        -e "s|@BRAND@|UMBREE|g" -e "s|@brand@|umbree|g" \
        -e "s|@CHANNEL@|$channel|g" -e "s|@MIN_VERSION@|$min_version|g" \
        -e "s|@DOWNLOADS_BASE@|$DOWNLOADS_BASE|g" -e "s|@TEST_SEAM@|$SEAM|g" \
        "$exp" > "$tmp"
    rm -f "$exp"
    grep -q '@INCLUDE:' "$tmp" && { rm -f "$tmp"; echo "✗ unexpanded @INCLUDE in $out" >&2; exit 1; }
    chmod +x "$tmp"
    mv -f "$tmp" "$out"
    echo "✓ wrote $out"
}

for comp in $COMPONENTS; do
    if [ "$OUT_ROOT" != "$ROOT" ] && [ -L "$OUT_ROOT/$comp" ]; then
        echo "✗ --test-build: $OUT_ROOT/$comp is a symlink; a test build writes only into its own directory" >&2
        exit 2
    fi
    mkdir -p "$OUT_ROOT/$comp"
    for channel in stable beta; do
        if [ "$channel" = beta ]; then
            beta_stamp="$ROOT/versions/${comp}.beta.stamp"
            if [ ! -f "$beta_stamp" ]; then
                stale=""
                for f in "$OUT_ROOT/$comp"/beta.*.sh "$OUT_ROOT/$comp/beta.version.js"; do
                    [ -e "$f" ] || continue
                    rm -f "$f"
                    stale="$stale $(basename "$f")"
                done
                if [ -n "$stale" ]; then
                    echo "→ $comp: no beta cycle open ($beta_stamp absent) — removed stale:$stale"
                else
                    echo "→ $comp: no beta cycle open ($beta_stamp absent) — beta twin not rendered"
                fi
                continue
            fi
            min_version="$(UMBREE_MIN_VERSION_FILE="$beta_stamp" min_version_of "$comp")"
            render "$comp" beta "$min_version" "$OUT_ROOT/$comp/beta.install.sh"
        else
            min_version="$(min_version_of "$comp")"
            render "$comp" stable "$min_version" "$OUT_ROOT/$comp/install.sh"
        fi
    done
done

#!/bin/sh
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
TEMPLATE="$ROOT/tools/bootstrap.template.sh"
[ -f "$TEMPLATE" ] || { echo "✗ missing template: $TEMPLATE" >&2; exit 1; }

MODDIR="$ROOT/tools/modules"

expand_includes() {
    awk -v moddir="$MODDIR" '
        /^@INCLUDE:[a-z0-9-]+@$/ {
            name = substr($0, 10, length($0) - 9 - 1)
            path = moddir "/" name ".sh"
            if ((getline probe < path) < 0) {
                printf("✗ @INCLUDE:%s@ but %s does not exist\n", name, path) > "/dev/stderr"
                exit 1
            }
            close(path)
            printf("# BEGIN %s\n", name)
            while ((getline line < path) > 0) {
                if (line ~ /^# (module|needs|since):/) continue
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
        -e "s|@DOWNLOADS_BASE@|$DOWNLOADS_BASE|g" \
        "$exp" > "$tmp"
    rm -f "$exp"
    grep -q '@INCLUDE:' "$tmp" && { rm -f "$tmp"; echo "✗ unexpanded @INCLUDE in $out" >&2; exit 1; }
    chmod +x "$tmp"
    mv -f "$tmp" "$out"
    echo "✓ wrote $out"
}

for comp in $COMPONENTS; do
    mkdir -p "$ROOT/$comp"
    for channel in stable beta; do
        if [ "$channel" = beta ]; then
            beta_stamp="$ROOT/versions/${comp}.beta.stamp"
            if [ ! -f "$beta_stamp" ]; then
                stale=""
                for f in "$ROOT/$comp"/beta.*.sh "$ROOT/$comp/beta.version.js"; do
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
            render "$comp" beta "$min_version" "$ROOT/$comp/beta.install.sh"
        else
            min_version="$(min_version_of "$comp")"
            render "$comp" stable "$min_version" "$ROOT/$comp/install.sh"
        fi
    done
done

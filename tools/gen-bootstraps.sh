#!/bin/sh
# gen-bootstraps.sh — generate the self-contained outer bootstraps
# (<comp>/install.sh, and while a beta cycle is open <comp>/beta.install.sh)
# from one template.
#
# Each generated file is a direct render of the template with the @COMP@,
# @PUBKEY@, @CHANNEL@, @MIN_VERSION@ and @DOWNLOADS_BASE@ substitutions
# applied. The outer bootstrap is THE TRUST ANCHOR, so the baked @PUBKEY@ must
# be the real release signing pubkey before activation.
#
# Pubkey resolution (first that exists wins):
#   1. $UMBREE_PUBKEY_FILE   (explicit override; used by the offline E2E test)
#   2. umbree-release.pub    (the REAL release signing pubkey — once minted/activated)
#   3. tools/testkeys/test.pub (the local TEST key)
#   4. none -> a clearly-marked TEMP placeholder is baked in, and the generated
#      bootstrap WILL refuse to run (the runtime guards on *TEMP*). Regenerate
#      once a real key exists.
#
# The @PUBKEY@ value is the base64 key line of a minisign .pub file (the last
# non-comment line) — exactly what `minisign -V -P <pubkey>` expects inline.
#
# VERSION FLOOR: @MIN_VERSION@ is baked from versions/<comp>.stamp (the full
# stamp of the component's last stable cut, written by tools/release.sh before
# it re-runs this script) — the runtime refuses to install anything older, so a
# stale or hostile mirror cannot roll a fresh install back. A missing stamp
# file is a REFUSAL, not an empty floor: a bootstrap without a usable floor
# cannot resolve a version at all, so shipping one is not an option.
#
#   UMBREE_MIN_VERSION        test-only override for the baked floor VALUE
#                             (mirrors UMBREE_PUBKEY_FILE). Offline tests
#                             fabricate their own release stamps.
#   UMBREE_MIN_VERSION_FILE   override for the FILE min_version_of reads. NOT
#                             test-only: the beta twin loop below sets it in
#                             PRODUCTION to versions/<comp>.beta.stamp, inside a
#                             command substitution so it cannot leak into the
#                             next component's or channel's stable render.
#
# BETA TWIN: for every component this also renders <comp>/beta.install.sh —
# the SAME template with @CHANNEL@=beta and the floor read from
# versions/<comp>.beta.stamp. That file's PRESENCE is what renders the twin:
# when it is absent the twin is not rendered, and any beta.*.sh left over from
# a since-closed cycle is DELETED — the LOCAL copy, and only that: this script
# never touches the release host, so a beta.install.sh already served from
# there keeps resolving the last beta until an operator removes it by hand
# (tools/RUNBOOK.md "Close a cycle"). Two files gate the one state, one per
# half of it: versions/<comp>.beta (the semver, tools/version.sh --seed
# creates it) and versions/<comp>.beta.stamp (the cut stamp, tools/release.sh
# --channel beta writes it) — neither is written here.
#
# @DOWNLOADS_BASE@ is the downloads mirror the twin resolves from (and the
# stable file falls back to): UMBREE_R2_DOWNLOADS_BASE, defaulting to the same
# value tools/gen-version-jsonp.sh already uses. `UMBREE_R2_DOWNLOADS_BASE=`
# (explicitly empty) bakes an empty base — the twin then refuses at runtime.
set -eu

ROOT="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
TEMPLATE="$ROOT/tools/bootstrap.template.sh"
[ -f "$TEMPLATE" ] || { echo "✗ missing template: $TEMPLATE" >&2; exit 1; }

MODDIR="$ROOT/tools/modules"

# expand_includes <template> — write <template> to stdout with every line that is
# exactly `@INCLUDE:<name>@` replaced by tools/modules/<name>.sh, wrapped in
# `# BEGIN <name>` / `# END <name>` markers and with the module's own header
# lines dropped. Runs BEFORE the sed substitution pass, so a module may contain
# @COMP@ / @BRAND@ / @brand@ like any other template text.
#
# The bootstrap is the trust anchor: it is delivered as `curl … | sh` and fetches
# no code. Modules are therefore spliced HERE, at generation time, and never
# sourced at runtime.
#
# The emitted `# BEGIN <name>` / `# END <name>` markers are LOAD-BEARING: one
# or more tools/test-*.sh scripts (e.g. tools/test-checksum-verify.sh) extract
# a module's spliced block out of a GENERATED bootstrap by matching these exact
# marker names verbatim. Renaming a module (and therefore its markers) without
# first grepping tools/test-*.sh for the old name will silently break that
# extraction.
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

# ---- resolve the pubkey -------------------------------------------------
pubfile=""
for cand in "${UMBREE_PUBKEY_FILE:-}" "$ROOT/umbree-release.pub" "$ROOT/tools/testkeys/test.pub"; do
    [ -n "$cand" ] || continue
    if [ -f "$cand" ]; then pubfile="$cand"; break; fi
done

if [ -n "$pubfile" ]; then
    # last non-empty, non-comment line = the base64 key line
    PUBKEY="$(grep -v '^untrusted comment:' "$pubfile" | grep -v '^[[:space:]]*$' | tail -n1)"
    [ -n "$PUBKEY" ] || { echo "✗ could not extract a pubkey line from $pubfile" >&2; exit 1; }
    echo "→ baking pubkey from: $pubfile"
else
    # No key file anywhere yet. Bake a TEMP placeholder — the runtime guard in
    # the template aborts on *TEMP* so these can never silently install.
    PUBKEY="RWTEMP_PLACEHOLDER_REGENERATE_AFTER_ACTIVATION_xxxxxxxxxxxx"
    echo "! no pubkey file found (umbree-release.pub / tools/testkeys/test.pub)" >&2
    echo "! baking a TEMP placeholder — generated bootstrap will REFUSE to run." >&2
    echo "! create the key (age-seal + activation) and re-run." >&2
fi

# ---- resolve the version floor ------------------------------------------
# min_version_of <comp> — the stamp to bake as @MIN_VERSION@. $UMBREE_MIN_VERSION
# overrides for tests; otherwise $UMBREE_MIN_VERSION_FILE, else
# versions/<comp>.stamp, which release.sh writes before it regenerates the
# bootstraps. Fails loudly rather than baking anything the runtime guard would
# have to reject: a bootstrap without a usable floor cannot resolve a version
# at all, so shipping one is not an option.
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
    # Validated whatever the source: the leading X.Y.Z must be numeric, because
    # that is all the runtime comparison reads and it fails closed on anything
    # else. Baking a floor the shipped installer would have to reject just turns
    # a generator bug into an install-time outage.
    case "${_mv#v}" in
        [0-9]*.[0-9]*.[0-9]*) : ;;
        *) echo "✗ ${_mv_src} holds '${_mv}', which has no numeric X.Y.Z prefix — refusing to bake an uncomparable version floor" >&2; exit 1 ;;
    esac
    printf '%s' "$_mv"
}

# ${VAR-default} (not :-): an explicit empty stays empty, exactly as the
# template treats UMBREE_DOWNLOADS_BASE at runtime.
DOWNLOADS_BASE="${UMBREE_R2_DOWNLOADS_BASE-https://downloads.umbree.org}"

# ---- generate -----------------------------------------------------------
# Components come from internal/relconfig via rkit — the single list rkit
# builds from. No fallback list: a hardcoded one here is the drift this
# indirection exists to prevent, and a silent fallback would let the
# generator under-generate while exiting 0.
COMPONENTS="$(cd "$ROOT" && go run ./cmd/rkit components)" || {
    echo "gen-bootstraps: could not read the component list from rkit" >&2
    exit 1
}
[ -n "$COMPONENTS" ] || { echo "gen-bootstraps: rkit returned no components" >&2; exit 1; }

# render <comp> <channel> <min_version> <out> — one file. Use a tmp then move
# so a partial write can't ship. expand_includes writes to its OWN redirection
# (not a pipe) so `set -e` sees its exit status — a pipeline's left-hand
# failure is otherwise invisible under plain `set -eu` and would silently ship
# a bootstrap truncated at the include point, losing the whole trust gate
# while exiting 0. The @INCLUDE: guard below still runs against the tmp file
# BEFORE the mv, to catch the OTHER failure shape: a malformed include name
# the awk regex declines to match and so passes through literally.
render() {
    comp="$1"; channel="$2"; min_version="$3"; out="$4"
    tmp="$out.tmp.$$"
    exp="$out.exp.$$"
    expand_includes "$TEMPLATE" > "$exp"
    # @COMP@ / @PUBKEY@ / @BRAND@ / @brand@ / @CHANNEL@ / @MIN_VERSION@ /
    # @DOWNLOADS_BASE@ — order doesn't matter, no value contains another's
    # placeholder.
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
    # Umbree renders ONE mode per component (install; no upgrade.sh, no
    # updater) — the twin is therefore exactly beta.install.sh. The channel
    # loop is kept in burrowee's shape so a second mode later is a one-line
    # change.
    for channel in stable beta; do
        if [ "$channel" = beta ]; then
            beta_stamp="$ROOT/versions/${comp}.beta.stamp"
            if [ ! -f "$beta_stamp" ]; then
                stale=""
                for f in "$ROOT/$comp"/beta.*.sh; do
                    [ -e "$f" ] || continue   # glob matched nothing
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

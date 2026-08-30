#!/bin/sh
# gen-bootstraps.sh — generate the self-contained outer bootstrap (umbree/install.sh)
# from one template.
#
# The generated file is a direct render of the template with the @COMP@ and
# @PUBKEY@ substitutions applied. The outer bootstrap is THE TRUST ANCHOR, so
# the baked @PUBKEY@ must be the real release signing pubkey before activation.
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
for comp in $COMPONENTS; do
    out="$ROOT/$comp/install.sh"
    mkdir -p "$ROOT/$comp"
    # @COMP@ / @PUBKEY@ / @BRAND@ / @brand@ — order doesn't matter, none of the
    # four values contains another's placeholder. Use a tmp then move so a
    # partial write can't ship.
    tmp="$out.tmp.$$"
    exp="$out.exp.$$"
    # expand_includes writes to its OWN redirection (not a pipe) so `set -e`
    # sees its exit status directly — a pipeline's left-hand failure is
    # otherwise invisible under plain `set -eu` and would silently ship a
    # bootstrap truncated at the include point, losing the whole trust gate
    # while exiting 0. The @INCLUDE: guard below still runs against the tmp
    # file BEFORE the mv, to catch the OTHER failure shape: a malformed
    # include name the awk regex declines to match and so passes through
    # literally.
    expand_includes "$TEMPLATE" > "$exp"
    sed -e "s|@COMP@|$comp|g" -e "s|@PUBKEY@|$PUBKEY|g" \
        -e "s|@BRAND@|UMBREE|g" -e "s|@brand@|umbree|g" \
        "$exp" > "$tmp"
    rm -f "$exp"
    grep -q '@INCLUDE:' "$tmp" && { rm -f "$tmp"; echo "✗ unexpanded @INCLUDE in $out" >&2; exit 1; }
    chmod +x "$tmp"
    mv -f "$tmp" "$out"
    echo "✓ wrote $out"
done

#!/bin/sh
set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
MODDIR="$ROOT/tools/modules"
LOCK="$MODDIR/MODULES.lock"
die() { printf '\n✗ FAILED: %s\n' "$*" >&2; exit 1; }

sha_of() {
    if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else die "neither shasum nor sha256sum found"; fi
}

printf '\n=== LOCK: modules match MODULES.lock ===\n'
[ -f "$LOCK" ] || die "missing $LOCK"
while read -r name version want; do
    [ -n "${name:-}" ] || continue
    case "$name" in \#*) continue ;; esac
    f="$MODDIR/$name.sh"
    [ -f "$f" ] || die "$LOCK lists '$name' but $f does not exist"
    got="$(sha_of "$f")"
    [ "$got" = "$want" ] \
        || die "$name changed without a version bump (lock says $version/$want, file is $got)
    bump the '# module:' header and re-run tools/lock-modules.sh"
    hdr="$(sed -n '1,4s/^# module:[[:space:]]*\([a-z0-9-]*\)[[:space:]]*v\([0-9][0-9]*\).*/\1 v\2/p' "$f")"
    [ "$hdr" = "$name $version" ] \
        || die "$f header says '$hdr', $LOCK says '$name $version'"
done < "$LOCK"
for f in "$MODDIR"/*.sh; do
    n="$(basename "$f" .sh)"
    grep -q "^$n " "$LOCK" || die "$f is not listed in $LOCK"
done
printf '  OK\n'

COMPONENTS="$(cd "$ROOT" && go run ./cmd/rkit components)" || die "could not read the component list from rkit"
[ -n "$COMPONENTS" ] || die "rkit returned no components"
GENERATED_REL=""
for c in $COMPONENTS; do
    GENERATED_REL="$GENERATED_REL $c/install.sh"
    [ -f "$ROOT/versions/$c.beta.stamp" ] && GENERATED_REL="$GENERATED_REL $c/beta.install.sh"
done
GENERATED_REL="${GENERATED_REL# }"

printf '\n=== DEPS: every "# needs:" is included earlier ===\n'
for relgen in $GENERATED_REL; do
    gen="$ROOT/$relgen"
    [ -f "$gen" ] || continue
    order="$(sed -n 's/^# BEGIN \([a-z0-9-]*\)$/\1/p' "$gen")"
    for mod in $order; do
        needs="$(sed -n '1,6s/^# needs:[[:space:]]*//p' "$MODDIR/$mod.sh" 2>/dev/null || true)"
        for dep in $needs; do
            seen=0
            for m in $order; do
                [ "$m" = "$dep" ] && seen=1
                [ "$m" = "$mod" ] && break
            done
            [ "$seen" = 1 ] || die "$gen includes '$mod' which needs '$dep', but '$dep' is not included before it"
        done
    done
done
printf '  OK\n'

printf '\n=== DEPS: modules calling fail/info/ok declare needs: helpers ===\n'
for f in "$MODDIR"/*.sh; do
    n="$(basename "$f" .sh)"
    [ "$n" = "helpers" ] && continue
    body="$(grep -vE '^[[:space:]]*#' "$f")"
    calls=""
    for fn in fail info ok; do
        pat="(^|[^A-Za-z0-9_])${fn}[[:space:]]+[\"\$]"
        if printf '%s\n' "$body" | grep -Eq "$pat"; then
            calls="$calls $fn"
        fi
    done
    [ -n "$calls" ] || continue
    needs="$(sed -n '1,6s/^# needs:[[:space:]]*//p' "$f")"
    case " $needs " in
        *' helpers '*) ;;
        *) die "$f calls$calls (from the helpers module) but its '# needs:' line does not list 'helpers'" ;;
    esac
done
printf '  OK\n'

printf '\n=== INCLUDED: every locked module is spliced into some generated bootstrap ===\n'
EXCLUDE="$MODDIR/MODULES.exclude"
[ -f "$EXCLUDE" ] || die "missing $EXCLUDE — the record of which modules this product deliberately does not carry"
included=""
for relgen in $GENERATED_REL; do
    gen="$ROOT/$relgen"
    [ -f "$gen" ] || continue
    included="$included $(sed -n 's/^# BEGIN \([a-z0-9-]*\)$/\1/p' "$gen" | tr '\n' ' ')"
done
excluded="$(awk '/^[[:space:]]*#/ { next } NF { print $1 }' "$EXCLUDE" | tr '\n' ' ')"
while read -r name version want; do
    [ -n "${name:-}" ] || continue
    case "$name" in \#*) continue ;; esac
    case " $included " in *" $name "*) continue ;; esac
    case " $excluded " in
        *" $name "*) printf '  not carried: %s\n' "$name" ;;
        *) die "'$name' is locked but no generated bootstrap @INCLUDEs it — that module does not ship.
    Splice it into a template, or record it in $EXCLUDE with the reason." ;;
    esac
done < "$LOCK"
for name in $excluded; do
    case " $included " in
        *" $name "*) die "$EXCLUDE says '$name' is not carried, but a generated bootstrap @INCLUDEs it" ;;
    esac
done
printf '  OK\n'

printf '\n=== GENERATOR-FAILS-CLOSED: a missing module aborts before any destination file is written ===\n'
SCRATCH="$(mktemp -d)"
mkdir -p "$SCRATCH/tools" "$SCRATCH/bin"
cp "$ROOT/tools/gen-bootstraps.sh" "$SCRATCH/tools/gen-bootstraps.sh"
{
    echo '#!/bin/sh'
    echo '@INCLUDE:does-not-exist@'
    echo 'echo hi'
} > "$SCRATCH/tools/bootstrap.template.sh"
cat > "$SCRATCH/bin/go" <<'STUBGO'
#!/bin/sh
if [ "$1" = run ] && [ "$3" = components ]; then
    echo umbree
    exit 0
fi
echo "stub go: unexpected invocation: $*" >&2
exit 1
STUBGO
chmod +x "$SCRATCH/bin/go"
scratch_log="$SCRATCH/gen.log"
if PATH="$SCRATCH/bin:$PATH" UMBREE_PUBKEY_FILE="$ROOT/tools/testkeys/test.pub" \
   sh "$SCRATCH/tools/gen-bootstraps.sh" >"$scratch_log" 2>&1
then
    rm -rf "$SCRATCH"
    die "gen-bootstraps.sh exited 0 against a template that @INCLUDEs a nonexistent module — it must fail closed"
fi
if [ -e "$SCRATCH/umbree/install.sh" ]; then
    rm -rf "$SCRATCH"
    die "a missing module still left a file at umbree/install.sh — expand_includes' failure did not stop the write (see $scratch_log)"
fi
rm -rf "$SCRATCH"
printf '  OK\n'

printf '\n=== GENERATOR: committed bootstraps are what the generator writes ===\n'
gen_log="$(sh "$ROOT/tools/gen-bootstraps.sh" 2>&1)" || die "gen-bootstraps.sh failed:
$gen_log"
dirty="$(cd "$ROOT" && git status --porcelain -- $GENERATED_REL)"
[ -z "$dirty" ] || die "regenerating changed committed bootstraps — they are stale, commit the regeneration:
$dirty"
printf '  OK\n'

printf '\nALL OK — modules locked, dependencies ordered, bootstraps current\n'

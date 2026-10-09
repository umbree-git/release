#!/bin/sh
set -eu

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT

say() { printf '\n=== %s ===\n' "$*"; }
die() { printf '\n✗ FAILED: %s\n' "$*" >&2; exit 1; }

say "STATIC: no --ignore-missing in any bootstrap"
found="$(grep -lE '^[^#]*--ignore-missing' \
    "$REPO_ROOT"/tools/bootstrap.template.sh \
    "$REPO_ROOT"/umbree/install.sh 2>/dev/null || true)"
[ -z "$found" ] \
    || die "these still verify with --ignore-missing (breaks pre-2016 hashers):
$found"
printf '  OK: no --ignore-missing\n'

say "STATIC: operator-facing verify advice selects one file by name, not -c over the sums file"
for advice in "$REPO_ROOT/site/index.html" "$REPO_ROOT/tools/release.sh"; do
    rel="${advice#"$REPO_ROOT"/}"
    [ -f "$advice" ] || { printf '  (no %s — skipped)\n' "$rel"; continue; }
    bad="$(grep -nE '(shasum|sha256sum)[^<&"]*-c[^<&"]*SHA256SUMS' "$advice" || true)"
    [ -z "$bad" ] || die "$rel still tells the reader to verify with -c over the whole SHA256SUMS.txt
(one downloaded file + a checklist naming every file = a false tampering verdict):
$bad"
    # shellcheck disable=SC2016
    grep -qE 'NO ENTRY for \\?\$f in SHA256SUMS\.txt' "$advice" \
        || die "$rel has lost the no-entry arm of the verify recipe — it must fail loudly when the downloaded name is not listed, not report success on an empty selection"
    printf '  OK: %s selects by exact name and shouts when there is none\n' "$rel"
done

say "BEHAVIOR: extracting the checksum-verify block from umbree/install.sh"
sed -n '/^# BEGIN verify-checksum/,/^# END verify-checksum/p' \
    "$REPO_ROOT/umbree/install.sh" > "$W/verify.sh"
grep -q '^# END verify-checksum' "$W/verify.sh" \
    || die "could not extract the checksum-verify block from umbree/install.sh (markers missing or renamed)"
sed -n '/^sha256_of()/,/^}/p' "$REPO_ROOT/umbree/install.sh" > "$W/sha256_of.sh"
grep -q '^sha256_of()' "$W/sha256_of.sh" \
    || die "could not extract sha256_of() from umbree/install.sh"

mkdir -p "$W/bin"
cat > "$W/bin/shasum" <<'STUB'
#!/bin/sh
for a in "$@"; do
    case "$a" in
        --ignore-missing)
            echo "Unknown option: ignore-missing" >&2
            echo "Type shasum -h for help" >&2
            exit 1 ;;
    esac
done
exec /usr/bin/shasum "$@"
STUB
chmod +x "$W/bin/shasum"
[ -x /usr/bin/shasum ] || die "no /usr/bin/shasum to back the stub"

if PATH="$W/bin:$PATH" shasum -a 256 -c --ignore-missing /dev/null >/dev/null 2>&1; then
    die "the stub accepted --ignore-missing — it does not model a pre-2016 shasum"
fi
printf '  OK: stub shasum rejects --ignore-missing, as the old one does\n'

cat > "$W/run.sh" <<'RUNNER'
#!/bin/sh
set -eu
info() { :; }
ok()   { printf 'OK: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
. "$BLOCK_DIR/sha256_of.sh"
TMP="$FIXTURE"
ZIP="$WANT_ZIP"
. "$BLOCK_DIR/verify.sh"
ok "checksum verified"
RUNNER

F="$W/fixture"
mkdir -p "$F"
printf 'the real payload\n'  > "$F/umbree-darwin-amd64.zip"
printf 'not the payload\n'   > "$F/x-umbree-darwin-amd64.zip"
{
    /usr/bin/shasum -a 256 "$F/x-umbree-darwin-amd64.zip" | sed 's| .*/| |'
    /usr/bin/shasum -a 256 "$F/umbree-darwin-amd64.zip"   | sed 's| .*/| |'
    echo "0000000000000000000000000000000000000000000000000000000000000000  umbree-linux-arm64.zip"
} > "$F/SHA256SUMS.txt"

run() {
    FIXTURE="$1" WANT_ZIP="$2" BLOCK_DIR="$W" PATH="$W/bin:$PATH" \
        sh "$W/run.sh" 2>&1
}

say "CASE: intact zip, old shasum"
out="$(run "$F" umbree-darwin-amd64.zip)" \
    || die "intact zip rejected on a pre-2016 shasum — the bug is back:
$out"
case "$out" in *"checksum verified"*) ;; *) die "no 'checksum verified' in: $out" ;; esac
printf '  OK: verified\n'

say "CASE: binary-format sums line (hash *name)"
B="$W/binfmt"; mkdir -p "$B"
cp "$F/umbree-darwin-amd64.zip" "$B/"
/usr/bin/shasum -a 256 "$B/umbree-darwin-amd64.zip" \
    | sed 's|  .*/|  *|' > "$B/SHA256SUMS.txt"
grep -q ' \*umbree-darwin-amd64.zip$' "$B/SHA256SUMS.txt" \
    || die "fixture is not in binary format: $(cat "$B/SHA256SUMS.txt")"
out="$(run "$B" umbree-darwin-amd64.zip)" \
    || die "binary-format entry not matched: $out"
printf '  OK: verified\n'

say "CASE: tampered zip aborts"
T="$W/tampered"; mkdir -p "$T"
cp "$F/SHA256SUMS.txt" "$T/"
printf 'the real payloadX\n' > "$T/umbree-darwin-amd64.zip"
if out="$(run "$T" umbree-darwin-amd64.zip)"; then
    die "tampered zip was ACCEPTED: $out"
fi
case "$out" in *"checksum mismatch"*) ;; *) die "wrong refusal for a tampered zip: $out" ;; esac
printf '  OK: aborted with "checksum mismatch"\n'

say "CASE: no entry for the downloaded zip aborts"
if out="$(run "$F" umbree-windows-amd64.zip)"; then
    die "a zip with no sums entry was ACCEPTED: $out"
fi
case "$out" in *"no checksum entry"*) ;; *) die "wrong refusal for a missing entry: $out" ;; esac
printf '  OK: aborted with "no checksum entry"\n'

say "CASE: a longer name containing the wanted one is not a match"
D="$W/decoy"; mkdir -p "$D"
cp "$F/umbree-darwin-amd64.zip" "$D/"
/usr/bin/shasum -a 256 "$F/x-umbree-darwin-amd64.zip" | sed 's| .*/| |' > "$D/SHA256SUMS.txt"
if out="$(run "$D" umbree-darwin-amd64.zip)"; then
    die "matched a DIFFERENT file's entry by substring: $out"
fi
case "$out" in *"no checksum entry"*) ;; *) die "wrong refusal for a substring-only name: $out" ;; esac
printf '  OK: aborted with "no checksum entry"\n'

printf '\nALL OK — the checksum gate works on hashers without --ignore-missing\n'

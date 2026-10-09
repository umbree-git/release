#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REAL_ROOT="$(cd "${HERE}/.." && pwd)"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 — got '$2' want '$3'"; fail=1; fi; }
check_contains() { case "$2" in *"$3"*) echo "ok: $1";; *) echo "FAIL: $1 — output does not contain '$3':"; printf '%s\n' "$2" | sed 's/^/      /'; fail=1;; esac; }
check_lacks() { case "$2" in *"$3"*) echo "FAIL: $1 — output contains '$3'"; fail=1;; *) echo "ok: $1";; esac; }

T="$(mktemp -d)"; trap 'rm -rf "$T"' EXIT
export GIT_CONFIG_GLOBAL="$T/gitconfig"
/usr/bin/git config --file "$GIT_CONFIG_GLOBAL" user.name t
/usr/bin/git config --file "$GIT_CONFIG_GLOBAL" user.email t@t
/usr/bin/git config --file "$GIT_CONFIG_GLOBAL" init.defaultBranch main
unset BETA_BRANCH UMBREE_R2_ACCOUNT UMBREE_R2_CREDS UMBREE_R2_GATED_BUCKET STUB_GATED_FAIL \
    UMBREE_MANAGE_URL UMBREE_RELEASE_KEY STUB_REGISTER STUB_STATUS STUB_STATUS_STAMP STUB_STATUS_SEQ STUB_STATUS_EXTRA

STUB="$T/stub"; mkdir -p "$STUB"
export CALLS="$T/calls.log"; : > "$CALLS"
for tool in gh ssh scp minisign; do
    printf '#!/bin/sh\necho "%s $*" >> "$CALLS"\nexit 0\n' "$tool" > "$STUB/$tool"; chmod +x "$STUB/$tool"
done
cat > "$STUB/go" <<'EOF'
#!/bin/sh
echo "go $*" >> "$CALLS"
if [ "$1" = run ] && [ "$3" = components ]; then echo umbree; echo umbreed; exit 0; fi
if [ "$1" = run ] && [ "$2" = . ] && [ "$3" = --store ] && [ "$4" = gated ]; then
    [ -z "${STUB_GATED_FAIL:-}" ] || exit 1
    while [ $# -gt 0 ]; do
        if [ "$1" = --receipt ]; then printf '{"objects":[]}\n' > "$2"; fi
        shift
    done
    exit 0
fi
if [ "$1" = run ] && [ "$2" = ./cmd/rkit ] && [ "$3" = register ]; then
    case "${STUB_REGISTER:-201}" in
        2??) echo "✓ registered (stub)"; exit 0 ;;
        *) echo "✗ rkit register: POST https://manage.invalid/api/v1/releases/register: HTTP ${STUB_REGISTER} refused" >&2; exit 1 ;;
    esac
fi
if [ "$1" = run ] && [ "$2" = ./cmd/rkit ] && [ "$3" = status ]; then
    stamp=""
    while [ $# -gt 0 ]; do
        if [ "$1" = --stamp ]; then stamp="$2"; fi
        shift
    done
    state="${STUB_STATUS:-staged}"
    if [ -n "${STUB_STATUS_SEQ:-}" ] && [ -s "$STUB_STATUS_SEQ" ]; then
        state="$(head -n1 "$STUB_STATUS_SEQ")"
        tail -n +2 "$STUB_STATUS_SEQ" > "$STUB_STATUS_SEQ.next"; mv "$STUB_STATUS_SEQ.next" "$STUB_STATUS_SEQ"
    fi
    case "$state" in
        404) echo "✗ rkit status: the service has no row for this stamp: HTTP 404" >&2; exit 1 ;;
        *) echo "row 7 $state 0.1.8 ${STUB_STATUS_STAMP:-$stamp}"
           [ -z "${STUB_STATUS_EXTRA:-}" ] || echo "$STUB_STATUS_EXTRA"
           exit 0 ;;
    esac
fi
if [ "$1" = run ] && [ "$2" = . ]; then echo "stub go: public mirror refused by the fixture" >&2; exit 1; fi
echo "stub go: unexpected invocation: $*" >&2; exit 1
EOF
chmod +x "$STUB/go"
cat > "$STUB/git" <<'EOF'
#!/bin/sh
if [ "$1" = push ]; then echo "git $*" >> "$CALLS"; fi
exec /usr/bin/git "$@"
EOF
chmod +x "$STUB/git"
export PATH="$STUB:$PATH"
export RELEASE_HOST=x STATIC_DIR=/x

REL="$T/Umbree/release/code/main"
mkdir -p "$REL"
( cd "$REAL_ROOT" && /usr/bin/git ls-files -z ) | ( cd "$REAL_ROOT" && tar --null -cf - -T - ) | ( cd "$REL" && tar -xf - )
printf '0.1.8\n' > "$REL/versions/umbree"; rm -f "$REL/versions/umbree.beta" "$REL/versions/umbree.beta.stamp"
printf 'module_gate() { :; }\n' > "$REL/tools/module_gate.sh"
/usr/bin/git -C "$REL" init -q
/usr/bin/git -C "$REL" add -A && /usr/bin/git -C "$REL" commit -q -m baseline
/usr/bin/git init -q --bare "$T/release.git"
/usr/bin/git -C "$REL" remote add origin "$T/release.git"
/usr/bin/git -C "$REL" push -q -u origin main
/usr/bin/git -C "$REL" push -q origin main:refs/heads/dev

CLI="$T/Umbree/cli/code/main"
mkdir -p "$CLI"; /usr/bin/git -C "$CLI" init -q
echo x > "$CLI/f"; /usr/bin/git -C "$CLI" add f; /usr/bin/git -C "$CLI" commit -q -m seed
/usr/bin/git init -q --bare "$T/cli.git"
/usr/bin/git -C "$CLI" remote add origin "$T/cli.git"
/usr/bin/git -C "$CLI" push -q -u origin main
/usr/bin/git -C "$CLI" push -q origin main:refs/heads/dev
export UMBREE_SRC_UMBREE="$CLI"

BETA_STAMP=v0.2.0.beta.2026.09.05.deadbeef
STABLE_STAMP=v0.1.8.2026.08.31.46b36734
for st in "$BETA_STAMP" "$STABLE_STAMP"; do
    mkdir -p "$REL/dist/$st"
    for f in umbree-darwin-arm64.zip umbree-linux-amd64.zip SHA256SUMS.txt SHA256SUMS.txt.minisig; do
        echo x > "$REL/dist/$st/$f"
    done
done

R="$REL/tools/release.sh"
run() { out="$(bash "$R" "$@" 2>&1)"; rc=$?; }

echo "# verb parsing"
run --distribute-only umbree "$STABLE_STAMP" --channel beta
check "--distribute-only … --channel beta → 2" "$rc" "2"
check_contains "…names the stable-channel verb" "$out" "stable-channel verb"
run --channel stable umbree "$STABLE_STAMP"
check "--channel stable → 2" "$rc" "2"
run --channel beta umbree
check "--channel beta without a stamp → 2 (usage)" "$rc" "2"
run --channel beta umbree "$STABLE_STAMP"
check "--channel beta with a stable-shaped stamp → 1" "$rc" "1"
check_contains "…says not a beta stamp" "$out" "not a beta stamp"
run --frobnicate
check "unknown verb → 2" "$rc" "2"
run --help
check "--help → 0" "$rc" "0"
check_contains "…prints the usage" "$out" "bash tools/release.sh --distribute-only <umbree|umbreed> <stamp> [--dry-run]"
check_contains "…and the Env list" "$out" "UMBREE_R2_BUCKET       mirror bucket (default umbree-downloads)"
check "…its first line is the title" "$(printf '%s\n' "$out" | head -n1)" "release.sh — CUT an already-built umbree|umbreed release: stage it to the gated store and register it."
check_lacks "…the stable verb's help names no GitHub Release" "$(printf '%s\n' "$out" | sed -n '/^--distribute-only/,/^--channel beta/p')" "GitHub Release on"
check_lacks "…and no /releases resolution" "$out" "/releases resolution"
check_contains "…and names --register-only" "$out" "--register-only <umbree|umbreed> <stamp>"

echo "# beta pre-flight: origin"
run --channel beta umbree "$BETA_STAMP" --dry-run
check "no code/beta under --dry-run → 1 (the guard reports, the assert refuses)" "$rc" "1"
check_contains "…names the missing beta worktree" "$out" "beta worktree missing: $T/Umbree/cli/code/beta"
check_contains "…and the assert finds no open cycle" "$out" "versions/umbree.beta not found"

echo "# beta pre-flight: version"
/usr/bin/git -C "$CLI" worktree add -q -b beta "$T/Umbree/cli/code/beta" main
/usr/bin/git -C "$T/Umbree/cli/code/beta" push -q -u origin beta
export UMBREE_SRC_UMBREE="$T/Umbree/cli/code/beta"
printf '0.1.8\n' > "$REL/versions/umbree.beta"
run --channel beta umbree "$BETA_STAMP" --dry-run
check "beta == stable → 1" "$rc" "1"
check_contains "…says must sort above" "$out" "must sort above"

echo "# beta dry run"
printf '0.2.0\n' > "$REL/versions/umbree.beta"
/usr/bin/git -C "$REL" add versions/umbree.beta
run --channel beta umbree "$BETA_STAMP" --dry-run
check "beta --dry-run with code/beta, beta 0.2.0, R2 unset → 0" "$rc" "0"
check_contains "…would refuse on R2" "$out" "REFUSE — beta is R2-only"
check_contains "…plans the beta key layout" "$out" "umbree/beta/$BETA_STAMP/umbree-darwin-arm64.zip"
check_contains "…plans no GitHub Release" "$out" "no GitHub Release"
check_lacks "…never plans gh release create" "$out" "gh release create"
last_would="$(printf '%s\n' "$out" | grep '^would:   ' | tail -n1)"
check "…latest.json is the last planned key" "$last_would" "would:   umbree/beta/latest.json (last)"
check_contains "…twins only to the host" "$out" "scp ONLY beta.install.sh + beta.version.js"
check_lacks "…beta plans no gated stage" "$out" "would: stage"
check_lacks "…and names no production channel" "$out" "production"
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" ""

echo "# beta strict: R2 required before the tag"
run --channel beta umbree "$BETA_STAMP"
check "R2 unset without --dry-run → 1" "$rc" "1"
check_contains "…says beta is R2-only" "$out" "beta is R2-only"
check "…refused before the tag: git tag -l is empty" "$(/usr/bin/git -C "$REL" tag -l)" ""

echo "# the strict origin guard bites without --dry-run"
/usr/bin/git -C "$REL" reset -q -- versions/umbree.beta; rm -f "$REL/versions/umbree.beta"
export UMBREE_SRC_UMBREE="$CLI"
run --channel beta umbree "$BETA_STAMP"
check "strict beta with the main folder as source → 1" "$rc" "1"
check_contains "…refused by the origin guard" "$out" "beta source must be the registry beta worktree"

echo "# stable dry run"
run --distribute-only umbree "$STABLE_STAMP" --dry-run
check "stable --dry-run → 0" "$rc" "0"
check_lacks "…plans no GitHub Release" "$out" "gh release"
check_lacks "…the stable rehearsal prints no ⚠ for an in-sync registry main" "$out" "⚠ umbree"
check_contains "…would refuse with no gated bucket" "$out" "would: REFUSE — UMBREE_R2_GATED_BUCKET is not set"
check_contains "…would refuse with no manage URL" "$out" "would: REFUSE — UMBREE_MANAGE_URL is not set"

echo "# the gated stage step"
GATED_PREFIX="umbree/production/$STABLE_STAMP"
CREDS="$T/r2-creds.toml"; printf 'access_key_id = "a"\nsecret_access_key = "s"\n' > "$CREDS"
KEY="$T/release-fixture.key"; printf 'untrusted comment: fixture\nRWQfixture\n' > "$KEY"
MANAGE="https://manage.invalid"
calls() { cat "$CALLS"; }
no_public_act() {
    local log="$1" label="$2"
    check_lacks "$label: no gh call" "$log" "gh "
    check_lacks "$label: no ssh call" "$log" "ssh "
    check_lacks "$label: no scp call" "$log" "scp "
    check_lacks "$label: no upload" "$log" "go run . "
    check_lacks "$label: no registration" "$log" "rkit register"
}
cutrun() {
    UMBREE_R2_GATED_BUCKET=gated-fixture UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$CREDS" \
        UMBREE_MANAGE_URL="$MANAGE" UMBREE_RELEASE_KEY="$KEY" run "$@"
}
line_of() { grep -n -- "$1" "$CALLS" | head -n1 | cut -d: -f1; }
undo_cut() {
    if /usr/bin/git -C "$REL" log -1 --format=%s | grep -q '^\[RELEASED: umbree\]'; then
        /usr/bin/git -C "$REL" reset -q --hard HEAD~1
    fi
    /usr/bin/git -C "$REL" tag -d "umbree/$STABLE_STAMP" >/dev/null 2>&1
    /usr/bin/git -C "$T/release.git" tag -d "umbree/$STABLE_STAMP" >/dev/null 2>&1
    rm -f "$REL/dist/$STABLE_STAMP/gated-receipt.json"
}

cutrun --distribute-only umbree "$STABLE_STAMP" --dry-run
check "dry run with the gated bucket and the manage URL → 0" "$rc" "0"
check "…one would: stage line per gated key" "$(printf '%s\n' "$out" | grep -c "^would: stage $GATED_PREFIX/")" "4"
for f in SHA256SUMS.txt SHA256SUMS.txt.minisig umbree-darwin-arm64.zip umbree-linux-amd64.zip; do
    check_contains "…stages $f" "$out" "would: stage $GATED_PREFIX/$f"
done
check_lacks "…no refusal when both are set" "$out" "would: REFUSE"

echo "# dry run reports stage register confirm tag marker"
order="$(printf '%s\n' "$out" | grep -oE '^would: (stage|register|confirm row|tag|marker commit)' | sed 's/^would: //' | uniq | tr '\n' ',')"
check "dry run reports stage register confirm tag marker" "$order" "stage,register,confirm row,tag,marker commit,"

echo "# dry run reports no public act"
for word in latest.json "gh release" scp mirror retention; do
    check "dry run reports no public act: no line names '$word'" "$(printf '%s\n' "$out" | grep -ci -- "$word")" "0"
done
check "…the tree is untouched" "$(/usr/bin/git -C "$REL" status --porcelain)" ""

: > "$CALLS"
run --distribute-only umbree "$STABLE_STAMP"
check "stable cut with no gated bucket → 1" "$rc" "1"
check_contains "…names UMBREE_R2_GATED_BUCKET" "$out" "UMBREE_R2_GATED_BUCKET is not set"
no_public_act "$(calls)" "unset bucket"
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" ""

: > "$CALLS"
UMBREE_R2_GATED_BUCKET="" UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$CREDS" run --distribute-only umbree "$STABLE_STAMP"
check "empty gated bucket → 1" "$rc" "1"
check_contains "…names UMBREE_R2_GATED_BUCKET" "$out" "UMBREE_R2_GATED_BUCKET is not set"
no_public_act "$(calls)" "empty bucket"

: > "$CALLS"
UMBREE_R2_GATED_BUCKET=gated-fixture run --distribute-only umbree "$STABLE_STAMP"
check "gated bucket without the R2 token → 1" "$rc" "1"
check_contains "…names the token variables" "$out" "UMBREE_R2_ACCOUNT and UMBREE_R2_CREDS"
no_public_act "$(calls)" "no token"

: > "$CALLS"
UMBREE_R2_GATED_BUCKET=gated-fixture UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$CREDS" run --distribute-only umbree "$STABLE_STAMP"
check "no manage URL → 1" "$rc" "1"
check_contains "…names UMBREE_MANAGE_URL" "$out" "UMBREE_MANAGE_URL is not set"
check_lacks "…refused before the stage" "$(calls)" "--store gated"
no_public_act "$(calls)" "no manage URL"

: > "$CALLS"
MANAGE=http://manage.invalid cutrun --distribute-only umbree "$STABLE_STAMP"
check "a plain-http manage URL → 1" "$rc" "1"
check_contains "…says it must be https" "$out" "UMBREE_MANAGE_URL must be an https:// URL"
check_lacks "…refused before the stage" "$(calls)" "--store gated"

: > "$CALLS"
KEY="$T/no-such.key" cutrun --distribute-only umbree "$STABLE_STAMP"
check "no release key → 1" "$rc" "1"
check_contains "…names UMBREE_RELEASE_KEY" "$out" "UMBREE_RELEASE_KEY"
check_lacks "…refused before the stage" "$(calls)" "--store gated"

: > "$CALLS"
STUB_GATED_FAIL=1 cutrun --distribute-only umbree "$STABLE_STAMP"
check "a failed stage → 1" "$rc" "1"
check_contains "…says nothing was registered" "$out" "nothing registered"
check_lacks "…no registration follows" "$(calls)" "rkit register"
check_lacks "…no tag push follows" "$(calls)" "git push"
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" ""
undo_cut

echo "# registration keep-control completes"
: > "$CALLS"
head_before="$(/usr/bin/git -C "$REL" rev-parse HEAD)"
floor_before="$(cat "$REL/versions/umbree.stamp")"
cutrun --distribute-only umbree "$STABLE_STAMP"
check "registration keep-control completes" "$rc" "0"
gated_call="$(grep '^go run \. --store gated' "$CALLS" | head -n1)"
check_contains "…the stage step ran" "$gated_call" "--store gated"
check_contains "…against the gated bucket" "$gated_call" "--bucket gated-fixture"
check_contains "…on the production channel" "$gated_call" "--channel production"
check_contains "…with a receipt" "$gated_call" "--receipt $REL/dist/$STABLE_STAMP/gated-receipt.json"
check_lacks "…never the public bucket" "$gated_call" "umbree-downloads"
reg_call="$(grep '^go run ./cmd/rkit register ' "$CALLS" | head -n1)"
check_contains "…registers with the manage URL" "$reg_call" "--manage-url $MANAGE"
check_contains "…signed with the release key" "$reg_call" "--sign-key $KEY"
check_contains "…from the gated receipt" "$reg_call" "--receipt $REL/dist/$STABLE_STAMP/gated-receipt.json"
check_contains "…as production" "$reg_call" "--channel production"
check_contains "…for the stamp" "$reg_call" "--stamp $STABLE_STAMP"
st_call="$(grep '^go run ./cmd/rkit status ' "$CALLS" | head -n1)"
check_contains "…reads the row back for the stamp" "$st_call" "--stamp $STABLE_STAMP"
g_at="$(line_of '^go run \. --store gated')"; r_at="$(line_of '^go run ./cmd/rkit register ')"
s_at="$(line_of '^go run ./cmd/rkit status ')"; p_at="$(line_of '^git push origin refs/tags/')"
check "…stage, register, read back, then the tag push" \
    "$([ -n "$g_at" ] && [ -n "$r_at" ] && [ -n "$s_at" ] && [ -n "$p_at" ] && [ "$g_at" -lt "$r_at" ] && [ "$r_at" -lt "$s_at" ] && [ "$s_at" -lt "$p_at" ] && echo ordered)" "ordered"
check "…one marker commit" "$(/usr/bin/git -C "$REL" rev-list --count "$head_before..HEAD")" "1"
check_contains "…the marker names the stamp" "$(/usr/bin/git -C "$REL" log -1 --format=%s)" "[RELEASED: umbree]"
check_contains "…and the stamp" "$(/usr/bin/git -C "$REL" log -1 --format=%s)" "$STABLE_STAMP"
check_contains "…the report names the stamp" "$out" "cut umbree $STABLE_STAMP"
check_contains "…and the row's manage page" "$out" "$MANAGE/manage/production/umbree"
check_contains "…and the row" "$out" "row 7"
check_contains "…and that nothing is public" "$out" "nothing is public"
check_lacks "…and no release page" "$out" "releases/tag"

echo "# tag pushed at the cut"
check "tag pushed at the cut" "$(grep -c "^git push origin refs/tags/umbree/$STABLE_STAMP\$" "$CALLS")" "1"
check "…it is on the remote" "$(/usr/bin/git -C "$T/release.git" tag -l "umbree/$STABLE_STAMP")" "umbree/$STABLE_STAMP"

echo "# stubbed cut runs no publish command"
for word in "gh " "scp " "ssh " "r2-prune" "gen-bootstraps" "gen-version-jsonp"; do
    check "stubbed cut runs no publish command: no '$word'" "$(grep -c -- "$word" "$CALLS")" "0"
done
check "…no public mirror upload" "$(grep '^go run \. ' "$CALLS" | grep -vc -- '--store gated')" "0"

echo "# cut writes no stamp floor"
check "cut writes no stamp floor" "$(cat "$REL/versions/umbree.stamp")" "$floor_before"
check "…the marker does not carry versions/umbree.stamp" \
    "$(/usr/bin/git -C "$REL" show --name-only --format= HEAD | grep -c '^versions/umbree.stamp$')" "0"
check "…nothing is left staged or modified" "$(/usr/bin/git -C "$REL" status --porcelain)" ""
undo_cut

echo "# registration 401 stops the cut"
: > "$CALLS"
head_before="$(/usr/bin/git -C "$REL" rev-parse HEAD)"
STUB_REGISTER=401 cutrun --distribute-only umbree "$STABLE_STAMP"
check "registration 401 stops the cut" "$([ "$rc" -ne 0 ] && echo stopped)" "stopped"
check_contains "…prints the status" "$out" "HTTP 401"
check_contains "…says the bytes are gated" "$out" "the bytes are in the gated store"
check_lacks "…and does not claim no row exists" "$out" "no row exists"
check_contains "…prints the re-register line" "$out" "re-register with: bash tools/release.sh --register-only umbree $STABLE_STAMP"
check "…no marker commit" "$(/usr/bin/git -C "$REL" rev-list --count "$head_before..HEAD")" "0"
check "…no tag" "$(/usr/bin/git -C "$REL" tag -l)" ""
check_lacks "…no tag push" "$(calls)" "git push"
check_lacks "…no read-back after a refusal" "$(calls)" "rkit status"
undo_cut

echo "# registration 200 without a row stops the cut"
: > "$CALLS"
STUB_STATUS=404 cutrun --distribute-only umbree "$STABLE_STAMP"
check "registration 200 without a row stops the cut" "$([ "$rc" -ne 0 ] && echo stopped)" "stopped"
check_contains "…says the row could not be read back" "$out" "could not be read back"
check_lacks "…and does not claim no row exists" "$out" "no row exists"
check_contains "…prints the re-register line" "$out" "re-register with: bash tools/release.sh --register-only umbree $STABLE_STAMP"
check "…no marker commit" "$(/usr/bin/git -C "$REL" rev-list --count "$head_before..HEAD")" "0"
check "…no tag" "$(/usr/bin/git -C "$REL" tag -l)" ""
undo_cut
for bad in "STUB_STATUS=public" "STUB_STATUS_STAMP=v0.1.7.2026.08.01.00000000" "STUB_STATUS_EXTRA='row 9 public 0.1.9 x'"; do
    : > "$CALLS"
    eval "$bad cutrun --distribute-only umbree \"\$STABLE_STAMP\""
    check "…a read-back of the wrong row stops it too ($bad)" "$([ "$rc" -ne 0 ] && echo stopped)" "stopped"
    check_contains "…and names what it read ($bad)" "$out" "reads it back as"
    check "…no marker commit ($bad)" "$(/usr/bin/git -C "$REL" rev-list --count "$head_before..HEAD")" "0"
    undo_cut
done

echo "# register-only re-registers and does nothing else"
SEQ="$T/status.seq"
: > "$CALLS"
printf '{"objects":[]}\n' > "$REL/dist/$STABLE_STAMP/gated-receipt.json"
printf '404\nstaged\n' > "$SEQ"
STUB_STATUS_SEQ="$SEQ" cutrun --register-only umbree "$STABLE_STAMP"
check "register-only → 0" "$rc" "0"
check "…one register call" "$(grep -c '^go run ./cmd/rkit register ' "$CALLS")" "1"
check "…a read before it and a read-back after it" "$(grep -c '^go run ./cmd/rkit status ' "$CALLS")" "2"
check "register-only re-registers and does nothing else" "$(grep -vc '^go run ./cmd/rkit \(register\|status\) ' "$CALLS")" "0"
check "…no marker commit" "$(/usr/bin/git -C "$REL" rev-list --count "$head_before..HEAD")" "0"
check "…no tag" "$(/usr/bin/git -C "$REL" tag -l)" ""
check_contains "…reports the row" "$out" "row 7"

echo "# register-only finds the row already staged"
: > "$CALLS"
cutrun --register-only umbree "$STABLE_STAMP"
check "register-only with the row already staged → 0" "$rc" "0"
check "…registers nothing" "$(grep -c '^go run ./cmd/rkit register ' "$CALLS")" "0"
check "…one read" "$(grep -c '^go run ./cmd/rkit status ' "$CALLS")" "1"
check_contains "…says it was already registered" "$out" "already registered as row 7"

echo "# register-only treats 409 already catalogued as registered, then reads back"
: > "$CALLS"
printf '404\nstaged\n' > "$SEQ"
STUB_REGISTER=409 STUB_STATUS_SEQ="$SEQ" cutrun --register-only umbree "$STABLE_STAMP"
check "register-only, 409 and a staged read-back → 0" "$rc" "0"
check "…read, register, read back" "$(grep -c '^go run ./cmd/rkit status ' "$CALLS"):$(grep -c '^go run ./cmd/rkit register ' "$CALLS")" "2:1"
check_contains "…reports the row" "$out" "row 7"
: > "$CALLS"
printf '404\n404\n' > "$SEQ"
STUB_REGISTER=409 STUB_STATUS_SEQ="$SEQ" cutrun --register-only umbree "$STABLE_STAMP"
check "register-only, 409 but no row reads back → 1" "$rc" "1"
for bad in "STUB_STATUS=public" "STUB_STATUS_STAMP=v0.1.7.2026.08.01.00000000"; do
    : > "$CALLS"
    eval "STUB_REGISTER=409 $bad cutrun --register-only umbree \"\$STABLE_STAMP\""
    check "register-only with a row of another state or stamp refuses ($bad)" "$rc" "1"
    check "…and registers nothing ($bad)" "$(grep -c '^go run ./cmd/rkit register ' "$CALLS")" "0"
    check_contains "…naming what it read ($bad)" "$out" "reads it back as"
done
: > "$CALLS"
printf '404\n' > "$SEQ"
STUB_REGISTER=401 STUB_STATUS_SEQ="$SEQ" cutrun --register-only umbree "$STABLE_STAMP"
check "register-only, a 401 still refuses" "$rc" "1"
check_contains "…with the status" "$out" "HTTP 401"
check "…no marker commit" "$(/usr/bin/git -C "$REL" rev-list --count "$head_before..HEAD")" "0"
rm -f "$REL/dist/$STABLE_STAMP/gated-receipt.json"
: > "$CALLS"
cutrun --register-only umbree "$STABLE_STAMP"
check "register-only with no receipt → 1" "$rc" "1"
check_contains "…names the receipt" "$out" "gated-receipt.json"
check "…nothing called" "$(grep -c . "$CALLS")" "0"
: > "$CALLS"
cutrun --register-only umbree "$BETA_STAMP"
check "register-only with a beta stamp → 1" "$rc" "1"
check "…nothing called" "$(grep -c . "$CALLS")" "0"
undo_cut

echo "# beta verb unchanged"
sha_of() { if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi; }
check "beta verb unchanged: publish_beta is byte-identical to the 01 baseline" \
    "$(awk '/^publish_beta\(\) \{/,/^}/' "$R" | sha_of)" "b5851de6642a4a1974e556b3c298ca2eceedadb32e2400c81c7ab0e12ea285e9"

echo "# distribute_only has no publish call"
STABLE_BODY=""
for fn in distribute_only distribute_dry_run distribute_preflight stage_and_register register_staged confirm_row tag_and_mark report_cut register_only; do
    body="$(awk "/^${fn}\\(\\) \\{/,/^}/" "$R")"
    check "stable verb function $fn extracted" "$([ -n "$body" ] && echo yes)" "yes"
    STABLE_BODY="$STABLE_BODY$body
"
done
for word in "release create" mirror_r2 apply_retention "scp " "ssh " gen-bootstraps gen-version-jsonp latest.json '.stamp"' r2-prune; do
    check "distribute_only has no publish call: no '$word'" "$(printf '%s' "$STABLE_BODY" | grep -cF -- "$word")" "0"
done

echo "# gated_channel_for: the one stable → production mapping"
GCF="$(awk '/^gated_channel_for\(\) \{/,/^}/' "$R")"
check "gated_channel_for extracted" "$([ -n "$GCF" ] && echo yes)" "yes"
check "stable → production" "$(bash -c "$GCF"'
gated_channel_for stable' 2>&1)" "production"
for bad in beta production "" Stable; do
    gout="$(bash -c "$GCF"'
gated_channel_for "$1"' _ "$bad" 2>&1)"; grc=$?
    check "'$bad' → non-zero" "$([ "$grc" -ne 0 ] && echo refused)" "refused"
    check_contains "…names '$bad'" "$gout" "'$bad'"
done

echo "# the sync-back check: origin/main must be contained in origin/dev"
echo m > "$REL/prior-marker.txt"; /usr/bin/git -C "$REL" add prior-marker.txt
/usr/bin/git -C "$REL" commit -q -m "[RELEASED: umbree] prior cut"
/usr/bin/git -C "$REL" push -q origin main
run --distribute-only umbree "$STABLE_STAMP"
check "release repo main not in dev → 1" "$rc" "1"
check_contains "…names the missing sync-back" "$out" "✗ release repo: origin/main is not an ancestor of dev"
check_contains "…names the fix" "$out" "a merge, never a rebase"
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" ""
run --distribute-only umbree "$STABLE_STAMP" --dry-run
check "…the same state under --dry-run reports and continues → 0" "$rc" "0"
check_contains "…as a ⚠" "$out" "⚠ release repo: origin/main is not an ancestor of dev"
/usr/bin/git -C "$REL" push -q origin main:refs/heads/dev

echo y > "$CLI/g"; /usr/bin/git -C "$CLI" add g; /usr/bin/git -C "$CLI" commit -q -m "hotfix on main"
/usr/bin/git -C "$CLI" push -q origin main
run --distribute-only umbree "$STABLE_STAMP"
check "component source main not in dev → 1" "$rc" "1"
check_contains "…names the component" "$out" "✗ umbree: origin/main is not an ancestor of dev"
check_lacks "…and not the release repo" "$out" "release repo: origin/main"
/usr/bin/git -C "$CLI" push -q origin main:refs/heads/dev

echo "# stable strict: an unpushed release repo is refused"
echo local > "$REL/local.txt"; /usr/bin/git -C "$REL" add local.txt; /usr/bin/git -C "$REL" commit -q -m local
run --distribute-only umbree "$STABLE_STAMP"
check "release repo ahead of origin → 1" "$rc" "1"
check_contains "…names the ahead state" "$out" "release repo source is 1 commit(s) ahead of origin/main"
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" ""

echo "# apply_retention: stable runs only r2-prune, beta runs both halves with KEEP unset"
AR="$T/apply-retention"; mkdir -p "$AR/stub" "$AR/root/tools/r2-mirror"
export AR_LOG="$AR/calls.log"; : > "$AR_LOG"
printf 'access_key_id = "a"\n' > "$AR/creds"
for tool in bash go; do
    printf '#!/bin/sh\necho "%s $* KEEP=${KEEP-unset} CHANNEL=${CHANNEL-} COMPONENTS=${COMPONENTS-} pwd=$(pwd)" >> "$AR_LOG"\nexit 0\n' "$tool" > "$AR/stub/$tool"
    chmod +x "$AR/stub/$tool"
done
apply_retention_in_isolation() {
    (
        unset GO_BIN
        export KEEP=1
        REPO_ROOT="$AR/root"
        eval "$(sed -n -e '/^r2_configured() {$/,/^}$/p' -e '/^apply_retention() {$/,/^}$/p' "$R")"
        PATH="$AR/stub:$PATH" apply_retention umbree "$1"
    ) >/dev/null 2>&1
}
UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$AR/creds" apply_retention_in_isolation stable
check "apply_retention stable: one call" "$(wc -l < "$AR_LOG" | tr -d ' ')" "1"
check_lacks "apply_retention stable: prune-releases.sh never runs, stable tags are never deleted" "$(cat "$AR_LOG")" "prune-releases.sh"
check_contains "apply_retention stable: r2-prune --execute runs" "$(sed -n 1p "$AR_LOG")" "go run ./cmd/r2-prune --comp umbree --channel stable --execute"
check_contains "…from the r2-mirror module" "$(sed -n 1p "$AR_LOG")" "pwd=$AR/root/tools/r2-mirror"
: > "$AR_LOG"
apply_retention_in_isolation stable
check "apply_retention stable: control, with R2 unset nothing runs" "$(wc -l < "$AR_LOG" | tr -d ' ')" "0"
: > "$AR_LOG"
UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$AR/creds" apply_retention_in_isolation beta
ar_first="$(sed -n 1p "$AR_LOG")"; ar_second="$(sed -n 2p "$AR_LOG")"
check "apply_retention beta: two calls" "$(wc -l < "$AR_LOG" | tr -d ' ')" "2"
check_contains "…prune-releases.sh --execute runs first" "$ar_first" "bash $AR/root/tools/prune-releases.sh --execute"
check_contains "…on the cut's channel and component" "$ar_first" "CHANNEL=beta COMPONENTS=umbree"
check_contains "…KEEP=1 does not reach prune-releases.sh" "$ar_first" "KEEP=unset"
check_contains "…r2-prune --execute runs second" "$ar_second" "go run ./cmd/r2-prune --comp umbree --channel beta --execute"

echo "# prose agrees"
PROSE="$(cd "$REAL_ROOT" && /usr/bin/git ls-files -- '*.md' '.release-request.example' 'tools/*.sh' 'tools/release.command' 'docs/*.txt' | grep -v '\.test\.sh$')"
check "prose agrees: the prose set is not empty" "$([ -n "$PROSE" ] && echo yes)" "yes"
prose_grep() { (cd "$REAL_ROOT" && printf '%s\n' "$PROSE" | tr '\n' '\0' | xargs -0 grep -niE -- "$1") || true; }
check "prose agrees: no retention count outside the README table" \
    "$(prose_grep 'keeps? (is )?[0-9]+|newest [0-9]+')" ""
check "prose agrees: no stale publish model" \
    "$(prose_grep 'there is no console|no console/dispatcher|GitHub Releases?[^.]*(stay|remain)[^.]*primary|publishes a GitHub Release|PUBLISH an already-staged|the cut is the publish|Today every component answers')" ""
RUNBOOK="$REAL_ROOT/tools/RUNBOOK.md"
for heading in "backfill" "admin mark-yanked" "Emergency"; do
    section="$(awk -v h="$heading" 'BEGIN { IGNORECASE = 1 } /^## / { on = (index(tolower($0), tolower(h)) > 0) } on' "$RUNBOOK")"
    check "prose agrees: RUNBOOK has a '$heading' section" "$([ -n "$section" ] && echo yes)" "yes"
    check_contains "…which says to stop serve or confirm no promote or yank is in flight" "$section" 'stop `serve`'
done

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

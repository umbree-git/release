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
unset BETA_BRANCH UMBREE_R2_ACCOUNT UMBREE_R2_CREDS UMBREE_R2_GATED_BUCKET STUB_GATED_FAIL

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
if [ "$1" = run ] && [ "$2" = . ]; then echo "stub go: public mirror refused by the fixture" >&2; exit 1; fi
echo "stub go: unexpected invocation: $*" >&2; exit 1
EOF
chmod +x "$STUB/go"
cat > "$STUB/git" <<'EOF'
#!/bin/sh
if [ "$1" = -C ] && [ "$3" = log ]; then
    echo "git $*" >> "$CALLS"
    [ -z "${STUB_GIT_LOG_FAIL:-}" ] || exit 128
fi
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
check "…its first line is the title" "$(printf '%s\n' "$out" | head -n1)" "release.sh — PUBLISH an already-staged umbree|umbreed release."

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
check_contains "…plans the GitHub Release" "$out" "gh release create umbree/$STABLE_STAMP"
check_contains "…plans the version floor" "$out" "write versions/umbree.stamp"
check_lacks "…the stable rehearsal prints no ⚠ for an in-sync registry main" "$out" "⚠ umbree"
check_contains "…would refuse with no gated bucket" "$out" "would: REFUSE — UMBREE_R2_GATED_BUCKET is not set"

echo "# the gated stage step"
GATED_PREFIX="umbree/production/$STABLE_STAMP"
CREDS="$T/r2-creds.toml"; printf 'access_key_id = "a"\nsecret_access_key = "s"\n' > "$CREDS"
calls() { cat "$CALLS"; }
no_public_act() {
    local log="$1" label="$2"
    check_lacks "$label: no gh call" "$log" "gh "
    check_lacks "$label: no ssh call" "$log" "ssh "
    check_lacks "$label: no scp call" "$log" "scp "
    check_lacks "$label: no upload" "$log" "go run ."
}

UMBREE_R2_GATED_BUCKET=gated-fixture run --distribute-only umbree "$STABLE_STAMP" --dry-run
check "dry run with the gated bucket → 0" "$rc" "0"
check "…one would: stage line per gated key" "$(printf '%s\n' "$out" | grep -c "^would: stage $GATED_PREFIX/")" "4"
for f in SHA256SUMS.txt SHA256SUMS.txt.minisig umbree-darwin-arm64.zip umbree-linux-amd64.zip; do
    check_contains "…stages $f" "$out" "would: stage $GATED_PREFIX/$f"
done
check "…no would: line names a gated manifest" "$(printf '%s\n' "$out" | grep '^would:' | grep -c 'production/.*latest\.json')" "0"
check_lacks "…no refusal when the bucket is set" "$out" "would: REFUSE"
stage_line="$(printf '%s\n' "$out" | grep -n '^would: stage ' | head -n1 | cut -d: -f1)"
gh_line="$(printf '%s\n' "$out" | grep -n '^would: gh release create' | cut -d: -f1)"
check "…the stage is planned before the GitHub Release" "$([ -n "$stage_line" ] && [ -n "$gh_line" ] && [ "$stage_line" -lt "$gh_line" ] && echo before)" "before"

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
UMBREE_R2_GATED_BUCKET=gated-fixture UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$CREDS" STUB_GATED_FAIL=1 \
    run --distribute-only umbree "$STABLE_STAMP"
check "a failed stage → 1" "$rc" "1"
check_contains "…says nothing was published" "$out" "nothing published"
check_lacks "…no GitHub Release follows" "$(calls)" "release create"
check_lacks "…no scp follows" "$(calls)" "scp "
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" ""

: > "$CALLS"
UMBREE_R2_GATED_BUCKET=gated-fixture UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$CREDS" \
    run --distribute-only umbree "$STABLE_STAMP"
gated_call="$(grep -n '^go run \. --store gated' "$CALLS" | head -n1)"
check_contains "keep-control: the stage step ran" "$gated_call" "--store gated"
check_contains "…against the gated bucket" "$gated_call" "--bucket gated-fixture"
check_contains "…on the production channel" "$gated_call" "--channel production"
check_contains "…with a receipt" "$gated_call" "--receipt $REL/dist/$STABLE_STAMP/gated-receipt.json"
check_lacks "…never the public bucket" "$gated_call" "umbree-downloads"
gated_at="${gated_call%%:*}"
release_at="$(grep -n '^gh .*release create' "$CALLS" | head -n1 | cut -d: -f1)"
check "…before the GitHub Release" "$([ -n "$gated_at" ] && [ -n "$release_at" ] && [ "$gated_at" -lt "$release_at" ] && echo before)" "before"
check "…the receipt was written" "$([ -f "$REL/dist/$STABLE_STAMP/gated-receipt.json" ] && echo yes)" "yes"
check_contains "…and the public mirror still runs after it" "$out" "R2 mirror FAILED"
/usr/bin/git -C "$REL" tag -d "umbree/$STABLE_STAMP" >/dev/null 2>&1
rm -f "$REL/dist/$STABLE_STAMP/gated-receipt.json" "$REL/dist/$STABLE_STAMP/release-notes.md"

echo "# a failed change summary stops the cut before any network act"
PREV_TAG="umbree/v0.1.7.2026.08.01.$(/usr/bin/git -C "$CLI" rev-parse --short=8 HEAD)"
/usr/bin/git -C "$REL" tag "$PREV_TAG"
: > "$CALLS"
UMBREE_R2_GATED_BUCKET=gated-fixture UMBREE_R2_ACCOUNT=acct UMBREE_R2_CREDS="$CREDS" STUB_GIT_LOG_FAIL=1 \
    run --distribute-only umbree "$STABLE_STAMP"
check "failing git log → non-zero" "$([ "$rc" -ne 0 ] && echo refused)" "refused"
check_contains "…says the summary could not be read" "$out" "cannot read the change summary"
check_lacks "…never publishes a made-up summary" "$out" "No code changes since"
check "…the failing git log was reached" "$(grep -c '^git -C .* log ' "$CALLS")" "1"
no_public_act "$(calls)" "failed change summary"
check "…no tag was created" "$(/usr/bin/git -C "$REL" tag -l)" "$PREV_TAG"
check "…no release notes were written" "$([ -f "$REL/dist/$STABLE_STAMP/release-notes.md" ] && echo yes)" ""
/usr/bin/git -C "$REL" tag -d "$PREV_TAG" >/dev/null 2>&1
rm -f "$REL/dist/$STABLE_STAMP/gated-receipt.json" "$REL/dist/$STABLE_STAMP/release-notes.md"

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

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

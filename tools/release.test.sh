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
unset BETA_BRANCH UMBREE_R2_ACCOUNT UMBREE_R2_CREDS

STUB="$T/stub"; mkdir -p "$STUB"
for tool in gh ssh scp minisign; do
    printf '#!/bin/sh\nexit 0\n' > "$STUB/$tool"; chmod +x "$STUB/$tool"
done
cat > "$STUB/go" <<'EOF'
#!/bin/sh
if [ "$1" = run ] && [ "$3" = components ]; then echo umbree; echo umbreed; exit 0; fi
echo "stub go: unexpected invocation: $*" >&2; exit 1
EOF
chmod +x "$STUB/go"
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

echo
if [ "$fail" = 0 ]; then echo "ALL OK"; else echo "TESTS FAILED"; exit 1; fi

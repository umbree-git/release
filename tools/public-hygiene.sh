#!/usr/bin/env bash
# public-hygiene.sh — refuse to ship internal facts from a PUBLIC repo.
#
# This repository is public. Everything in it is readable by anyone, forever,
# including through the git history of any commit that ever carried it. The
# rule this enforces:
#
#   A public repo names no internal tool, no internal file, no private repo,
#   no host, and no path from anyone's machine.
#
# Not because any one of them is a credential — none are — but because together
# they are a map: which wrapper to look for, which private repo holds the keys,
# which box actually serves the domain, and where on disk everything sits. The
# keys stay sealed; the map should not be handed out beside them.
#
# It is a GATE, not advice. Advice drifted: the same references were removed by
# hand and came back, because nothing failed when they did. Wire it into the
# release pre-flight so a cut refuses rather than publishes.
#
#   bash tools/public-hygiene.sh          # scan tracked files, exit 1 on a hit
#
# Scope: tracked files only. Build output, local-only files and anything
# gitignored are not published and are not scanned. An intentional exception
# goes in the allowlist below WITH its reason — never as a quietly loosened
# pattern.
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}" || exit 1
GIT=/usr/bin/git

# Each entry: <label>|<extended-regex>. The label is what a reader sees when it
# fires, so it says what is wrong, not merely what matched.
PATTERNS=(
  "internal GitHub wrapper|(^|[^A-Za-z0-9_-])(ghp|ghacct)([^A-Za-z0-9_-]|$)"
  # The name may appear bare (`.dp`) as well as qualified (`<repo>.dp`). The
  # first version of this pattern required a word character in front, so a
  # backticked bare `.dp` walked straight through it — in this repo's own
  # README, added by the same change that introduced the gate.
  "private secrets repo|(^|[^A-Za-z0-9])[A-Za-z0-9_-]*\.dp([^A-Za-z0-9]|$)"
  "internal agent/policy tree|\.agents/|agents/local"
  "internal release env file|release\.env"
  "decrypt identity path|\.age/"
  "production host|renative\.com|nsm\.[a-z]"
  "production static path|ebs_storage"
  "operator machine path|/Volumes/|MacintoshED|/Users/[a-z]+/|CODING_ROOT|Workstation/Coding"
)

# Allowlist: <path>:<pattern-label> pairs that are deliberate. Each needs a
# reason on the same line. Empty by default — an exception should be rare
# enough to argue for.
ALLOW=(
)

# ${ALLOW[@]} on an EMPTY array is an unbound-variable error under `set -u` in
# bash 3.2, which is what macOS ships — and an empty allowlist is the normal,
# desired state, so the gate would fail exactly when the repo is cleanest.
allowed() {
    local file="$1" label="$2" entry
    [ "${#ALLOW[@]}" -eq 0 ] && return 1
    for entry in "${ALLOW[@]}"; do
        [ "${entry%%|*}" = "${file}:${label}" ] && return 0
    done
    return 1
}

# Files that are themselves the gate or its test legitimately contain the
# patterns as data. Excluded by path, not by weakening the patterns.
is_self() {
    case "$1" in
        tools/public-hygiene.sh|tools/public-hygiene.test.sh) return 0 ;;
        *) return 1 ;;
    esac
}

fails=0
files="$($GIT ls-files)" || { echo "✗ cannot list tracked files"; exit 1; }

for entry in "${PATTERNS[@]}"; do
    label="${entry%%|*}"
    regex="${entry#*|}"
    while IFS= read -r file; do
        [ -n "$file" ] || continue
        is_self "$file" && continue
        # -I skips binary files; a match in one is not human-readable prose.
        hits="$($GIT grep -nIE "$regex" -- "$file" 2>/dev/null)" || continue
        [ -n "$hits" ] || continue
        allowed "$file" "$label" && continue
        if [ "$fails" -eq 0 ]; then
            echo "✗ public-hygiene: this repo is PUBLIC and these name internal facts"
            echo
        fi
        printf '  %s — %s\n' "$label" "$file"
        printf '%s\n' "$hits" | sed 's/^/      /'
        fails=1
    done <<< "$files"
done

if [ "$fails" -ne 0 ]; then
    echo
    echo "Remove them, or add a deliberate entry to ALLOW in tools/public-hygiene.sh"
    echo "with the reason. Note that removing a fact from HEAD does not remove it"
    echo "from history — treat anything already published as disclosed and mitigate"
    echo "it where it actually lives, not only here."
    exit 1
fi

echo "✓ public-hygiene: no internal tools, files, private repos, hosts or machine paths"

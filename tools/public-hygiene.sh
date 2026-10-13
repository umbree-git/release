#!/usr/bin/env bash
set -uo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}" || exit 1
GIT=/usr/bin/git

PATTERNS=(
  "internal GitHub wrapper|(^|[^A-Za-z0-9_-])(ghp|ghacct)([^A-Za-z0-9_-]|$)"
  "private secrets repo|(^|[^A-Za-z0-9])[A-Za-z0-9_-]*\.dp([^A-Za-z0-9]|$)"
  "internal agent/policy tree|\.agents/|agents/local"
  "internal release env file|release\.env"
  "decrypt identity path|\.age/"
  "production host|renative\.com|nsm\.[a-z]"
  "production static path|ebs_storage"
  "operator machine path|/Volumes/|MacintoshED|/Users/[a-z]+/|CODING_ROOT|Workstation/Coding"
)

ALLOW=(
)

allowed() {
    local file="$1" label="$2" entry
    [ "${#ALLOW[@]}" -eq 0 ] && return 1
    for entry in "${ALLOW[@]}"; do
        [ "${entry%%|*}" = "${file}:${label}" ] && return 0
    done
    return 1
}

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

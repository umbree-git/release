# Adoption: Umbree takes the shared bootstrap modules (2026-08-25)

Task 11 of the outer-bootstrap trust-chain plan (the last of three adoptions).
Umbree's outer bootstrap (`tools/bootstrap.template.sh`) carried its own copy
of the same trust chain Burrowee built shared, versioned modules for (Tasks
1–9, in `Burrowee/release/code/.worktrees/shasum-portable-checksum`), and
Clawee adopted in Task 10. This is an **adoption**, not a byte-identical
extraction: Umbree's blocks are near-twins of Burrowee's, not identical, so
the gate here is not "bytes must not change" but "every changed line is one
we can explain."

Umbree has a single component (`umbree`) with `install.sh` only — no
`upgrade.sh`, no updater, no preflight — so `tools/gen-bootstraps.sh` renders
one file from one template, in one mode.

Ten shared modules exist. Eight were adopted into Umbree's template via
`@INCLUDE:<name>@` (expanded at generation time by `tools/gen-bootstraps.sh`,
never at runtime — the bootstrap is a trust anchor delivered as `curl … | sh`
and must never fetch executable logic before the minisign gate). Two —
`download` and `version-resolve` — are recorded as **LOCAL FORK**: adopting
either would have broken or deleted working behaviour Umbree has today, so
Umbree's own blocks were left in place and the `@INCLUDE:` line was never
committed for them.

`tools/modules/download-r2-only.sh` was deleted before any of this: it mints
a console R2 URL for Burrowee's private/gated `relay` channel, and Umbree has
no gated channel at all. Running `tools/sync-modules.sh` against the Burrowee
worktree mechanically re-copies this file back in as `NEW` — the script has
no concept of a deliberately-excluded module. It was deleted again after each
such run and is not part of this commit; this is the same trap Clawee's
Task 10 adoption hit.

## Per-module verdicts

| Module | Verdict | Notes |
|---|---|---|
| `helpers` | adopted unchanged | `fail`/`info`/`ok` — byte-identical after markers |
| `sha256` | adopted (new) | `sha256_of` is **new to Umbree** — see below |
| `platform-detect` | adopted unchanged | OS/arch detection + banner — byte-identical after `@brand@`→`umbree` |
| `pubkey-guard` | adopted unchanged | TEMP/placeholder pubkey guard — byte-identical |
| `tmp-workspace` | adopted unchanged | `mktemp -d` + `trap` — byte-identical after `@brand@`→`umbree` |
| `require-minisign` | adopted, behaviour change | darwin hint gains a Homebrew-bootstrap one-liner — see below |
| `verify-signature` | adopted, behaviour change | now captures `verify_out` — see below |
| `verify-checksum` | adopted, behaviour change | **the fix** — replaces `shasum -c --ignore-missing` — see below |
| `download` | **LOCAL FORK** | shared module's ZIP name would double-prefix and 404 on every real release; its fallback is also a grant-gated R2 lookup that would delete Umbree's own no-auth downloads-mirror fallback |
| `version-resolve` | **LOCAL FORK** | shared module's PIN case is hardcoded to Burrowee's 4 components and would abort every install; it also calls `assert_version_floor` against a `$MIN_VERSION` Umbree's generator never bakes |

## Behaviour changes (exact lines, and why each is safe)

### 1. `sha256_of` — new to Umbree

Umbree had no standalone `sha256_of` helper before this change; the checksum
gate called `shasum`/`sha256sum` directly. The shared `sha256.sh` module
adds:

```sh
sha256_of() {
    if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
    elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else return 1; fi
}
```

spliced directly above the checksum gate (per the module order in the task
brief: `helpers, sha256, platform-detect, pubkey-guard, tmp-workspace,
require-minisign, verify-signature, verify-checksum, download,
version-resolve` — placed, as in Clawee's template, immediately before the
`verify-checksum` block that is its only caller). It is inert on its own —
nothing calls it until `verify-checksum` (below) is spliced in — and it uses
neither spelling's `--ignore-missing`/`--check` flags, so it is pre-2016-safe
on both macOS and Linux. Safe: pure addition, no existing call site touched.

### 2. `verify-checksum` — replaces `shasum -c --ignore-missing` (the fix)

This is the actual defect this whole effort exists to close. Before:

```sh
grep -qF "$ZIP" "$TMP/SHA256SUMS.txt" \
    || fail "no checksum entry for $ZIP — release incomplete or tampered; aborting"
if command -v shasum >/dev/null 2>&1; then
    ( cd "$TMP" && shasum -a 256 -c --ignore-missing SHA256SUMS.txt >/dev/null ) \
        || fail "checksum mismatch — aborting (zip tampered or download corrupted)"
elif command -v sha256sum >/dev/null 2>&1; then
    ( cd "$TMP" && sha256sum -c --ignore-missing SHA256SUMS.txt >/dev/null ) \
        || fail "checksum mismatch — aborting (zip tampered or download corrupted)"
else
    fail "neither shasum nor sha256sum found — cannot verify; aborting"
fi
```

After:

```sh
want="$(awk -v f="$ZIP" '{ n = $2; sub(/^\*/, "", n); if (n == f) { print $1; exit } }' "$TMP/SHA256SUMS.txt")"
[ -n "$want" ] \
    || fail "no checksum entry for $ZIP — release incomplete or tampered; aborting"
got="$(sha256_of "$TMP/$ZIP")" \
    || fail "neither shasum nor sha256sum found — cannot verify; aborting"
[ -n "$got" ] && [ "$want" = "$got" ] \
    || fail "checksum mismatch — aborting (zip tampered or download corrupted)"
```

`--ignore-missing` is a 2016-era addition (Digest::SHA 5.96 / coreutils 8.25).
On a macOS whose stock `shasum` predates that, the flag itself is rejected
("Unknown option: ignore-missing"), the command exits non-zero, and the `||`
reports that as "checksum mismatch" — accusing an intact, correctly signed
zip of tampering. This is the exact defect a real 2012 Mac mini hit on a
Burrowee gateway install on 2026-08-25, which is why this whole effort
exists; Umbree's outer bootstrap carried the identical bug (confirmed by
reading the pre-adoption `umbree/install.sh`, lines 288–296). The replacement
compares ONE hash directly (`sha256_of`, which uses neither flag) against the
line picked by exact filename match (the `awk`, handling both the plain and
binary-mode `*` prefix spellings) — strictly stricter than the substring
`grep -qF` it replaces, and it never invokes a `shasum`/`sha256sum` flag from
any particular era. Safe: this changes an error case (false-positive tamper
report on old hashers) into a correct pass, and the true-positive case (a
genuinely wrong hash) still fails exactly the same way.

### 3. `verify-signature` — now captures minisign's stdout

Before:

```sh
"$MINISIGN" -V -P "$PUBKEY" -m "$TMP/SHA256SUMS.txt" -x "$TMP/SHA256SUMS.txt.minisig" >/dev/null \
    || fail "signature verification failed — aborting (refusing to install unverified bytes)"
```

After:

```sh
verify_out="$("$MINISIGN" -V -P "$PUBKEY" -m "$TMP/SHA256SUMS.txt" -x "$TMP/SHA256SUMS.txt.minisig")" \
    || fail "signature verification failed — aborting (refusing to install unverified bytes)"
```

`verify_out` now holds minisign's stdout (the signed "Trusted comment:"
line), where before it was redirected to `/dev/null`. **Inert for Umbree**:
Burrowee's template consumes `$verify_out` in a tag-binding block right after
the signature check (comparing the release's trusted comment against the
resolved tag, to defeat a mirror silently serving an older signed release).
Umbree's template has no such block — `$verify_out` is set and never read.
Safe: capturing a command's stdout into a variable instead of discarding it
changes no control flow and produces no observable output difference (stderr
is unaffected either way, so minisign's own diagnostics on failure are still
shown).

### 4. `require-minisign` — darwin hint gains a Homebrew bootstrap line

Before:

```sh
darwin) hint="brew install minisign" ;;
```

After:

```sh
darwin) hint="install Homebrew if you don't have it, then minisign:
      /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\"
      brew install minisign" ;;
```

Purely additive advice text shown only when `minisign` is absent and the
installer is about to abort anyway (`fail` — exit 1, no further execution).
No control flow changes; the failure still happens, the message is just more
useful on a fresh Mac with no Homebrew. Safe.

## LOCAL FORK detail

### `download` — kept Umbree's own block

Two independent problems, either one alone sufficient to reject the module
as written:

**1. ZIP filename mismatch (correctness, not merely a style choice).** The
shared `download.sh` module builds:

```sh
ZIP="@brand@-${COMP}-${OS}-${ARCH}.zip"
```

Umbree has a single component whose name is itself `umbree`, so after the
generator's `@brand@`→`umbree` / `@COMP@`→`umbree` substitutions this becomes
`ZIP="umbree-umbree-${OS}-${ARCH}.zip"` — a double-prefixed name. Umbree's
actual release tooling (`tools/release.sh`, confirmed by reading it:
`compgen -G "${stage}/${comp}-*.zip"`) publishes assets named
`umbree-${OS}-${ARCH}.zip` (no doubled prefix — `COMP` is already the whole
brand-plus-component here). Adopting the shared module as-is would make every
real Umbree install request a filename that does not exist on the release,
and fail before ever reaching the trust gate. This is a hard regression, not
a behaviour trade-off.

**2. Fallback mechanism.** Once GitHub and the `GH_PROXIES` mirrors are
exhausted, the shared module's fallback is a **grant-gated** R2 lookup: it
shells out to `umbree download-url <comp> <tag> <asset>` (Burrowee's
console/device-grant mechanism — `umbree login` renews the grant), and
treats no authorized CLI on PATH as a hard failure. Umbree's own `dl()`
instead falls back, last, to `$UMBREE_DOWNLOADS_BASE` — an
operator-controlled, no-auth mirror (disabled by default; see the
`DOWNLOADS_BASE` verification below), reachable by a fresh host that has
never authenticated anything. Forcing the shared module would delete that
path outright.

Umbree's own download block is left in place in `tools/bootstrap.template.sh`,
with a comment pointing at this note; the `@INCLUDE:download@` substitution
was tried, its diff read, and reverted rather than committed.

### `version-resolve` — kept Umbree's own block

The shared `version-resolve.sh` module's PIN resolution is:

```sh
case "$COMP" in
    cli)     PIN="${@BRAND@_CLI_VERSION:-}" ;;
    gateway) PIN="${@BRAND@_GATEWAY_VERSION:-}" ;;
    edge)    PIN="${@BRAND@_EDGE_VERSION:-}" ;;
    agent)   PIN="${@BRAND@_AGENT_VERSION:-}" ;;
    *)       fail "unknown component '$COMP' — cannot resolve its version pin" ;;
esac
```

`$COMP` is a baked literal — for Umbree it is always `umbree`, which matches
none of the four arms and falls through to the `fail` case. Adopting this
module as written would make **every single Umbree install abort
immediately**, pinned or not — not a lost fallback, a total outage. This
alone is decisive.

Separately, and the same shape of problem Clawee hit: the module ends every
network-resolved (non-pinned) branch with `assert_version_floor "$TAG"`,
which reads `$MIN_VERSION` — baked by Burrowee's `tools/gen-bootstraps.sh`
from `versions/<comp>.stamp` at cut time. Umbree's generator has no such
mechanism (confirmed by reading `tools/gen-bootstraps.sh`: no `versions/`
directory read, no per-cut stamp, no `@MIN_VERSION@` placeholder anywhere in
the template). `assert_version_floor`'s own guard:

```sh
case "$MIN_VERSION" in
    ""|*@*|*PLACEHOLDER*|*TEMP*)
        fail "no version floor baked into this installer — refusing to accept a network-resolved version with nothing to check it against …" ;;
esac
```

would fire on every unpinned install even if the component-name problem
above were somehow worked around. Umbree's own resolver already does its own
anti-rollback ordering: it tries `$UMBREE_DOWNLOADS_BASE/umbree/latest.json`
(no auth, same first-party domain) *before* the third-party `GH_PROXIES`
mirrors, for the same reason Clawee's does — an on-path attacker blocking
GitHub must not be able to steer resolution to a stale third-party mirror.
The shared module's console-catalog step is a different, Burrowee-console-
specific answer that does not exist for Umbree (Umbree's carrier delegates
to the Burrowee daemon for connectivity, not to a Burrowee-style console
catalog for its own releases).

Recorded as LOCAL FORK; Umbree's own version-resolution block is left in
place, with a comment pointing at this note.

## Fork-vs-Clawee measurement (data only — no action taken)

Per the task instructions, Umbree's two retained (forked) blocks were diffed
directly against Clawee's corresponding retained blocks in
`Clawee/release/code/.worktrees/bootstrap-modules/tools/bootstrap.template.sh`,
after normalizing brand tokens (`UMBREE`/`Umbree`/`umbree` and
`CLAWEE`/`Clawee`/`clawee` both mapped to `BRAND`) so only structural
differences remain. This is measurement only: no shared Clawee/Umbree module
was created, and Clawee was not modified.

### `version-resolve`

After brand-normalization, the two blocks are **near-identical**. Both:

- resolve a pin from an env var, then fall to a `latest_tag`/GitHub API call;
- on GitHub failure, try the operator's own downloads-mirror
  `latest.json` first (anti-rollback ordering) before the third-party
  `GH_PROXIES` mirrors, in the same order, with the same guard conditions
  (`[ -z "$TAG" ] && [ -z "$DL_BASE" ] && [ -n "$DOWNLOADS_BASE" ]` /
  `... && [ -n "$GH_PROXIES" ]`);
- fail with the same shape of combined error message naming GitHub, the
  downloads mirror, and the proxy list.

Differences, in full:

1. **The PIN case statement.** Umbree has one component and one pin var
   (`UMBREE_VERSION`, no `case`); Clawee has two components (`clawee`,
   `claweed`) and a `case "$COMP"` switching between `CLAWEE_VERSION` and
   `CLAWEE_CLAWEED_VERSION`. This is a real, structural difference driven by
   component count, not wording.
2. **Naming/wording only.** Clawee's comments and log lines say "R2 mirror" /
   `downloads.clawee.org`; Umbree's say "downloads mirror" (no product-name
   literal baked into the comment, since the mirror hostname isn't in the
   template). Cosmetic — same mechanism, same variables
   (`DOWNLOADS_BASE`, `DL_BASE`, `GH_PROXIES`, `latest_tag`, `latest_stamp`).
3. **A trailing paragraph in Clawee's block only**, explaining that the
   upgrade-mode migration-line argument is not compared against the resolved
   release. This is inert prose specific to Clawee's `install.sh`/`upgrade.sh`
   dual-mode template; Umbree has no upgrade mode and no equivalent text.

### `download`

After brand-normalization, also **near-identical**. Both build `BASE`/
`MIRROR_BASE` the same way, percent-encode the tag's slash for mirror-only
URLs the same way, derive `STAMP`/`DOWNLOADS_FILE_BASE` identically, and the
`dl()` function's fallback order (primary → `GH_PROXIES` mirrors →
operator downloads mirror → fail) and error-message shape are the same.

Differences, in full:

1. **The ZIP filename** — this is the one that matters. Clawee's asset name
   is `clawee-${COMP}-${OS}-${ARCH}.zip` (brand-prefixed; `COMP` is `clawee`
   or `claweed`, distinct from the brand). Umbree's is
   `${COMP}-${OS}-${ARCH}.zip` (no prefix; `COMP` is already `umbree`). A
   single template shared between the two would need this parameterized —
   it cannot be resolved by `@brand@`/`@COMP@` substitution alone, because
   the shared module's assumption ("prefix + component are always distinct
   strings") is exactly the assumption that's false for Umbree.
2. **Naming/wording only** — same "R2 mirror" vs "downloads mirror" cosmetic
   difference as above, and the LOCAL FORK comment text itself (which names
   each product's own specific reason and so necessarily differs).

### Could one authored file serve both products?

**Not without a real generalization, but the generalization is small and
mechanical.** The two blocks are the same design solving the same problem in
the same order; nothing found here is a genuine behavioural fork the way
`download`'s R2-vs-mirror choice is a fork of the *shared* module. The two
retained blocks are themselves already near-copies of each other, kept apart
only by:

- a `case "$COMP"` in `version-resolve` whose arm count is data (one line
  per component) — this could become an `@INCLUDE`-time generated case
  built from a components list rather than requiring the module to hardcode
  Burrowee's four names, the same fix that would let both Clawee and Umbree
  (and Burrowee) use it;
- a ZIP-name template in `download` — `${PREFIX}${COMP}-${OS}-${ARCH}.zip`
  where `PREFIX` is empty for Umbree and `"clawee-"` for Clawee, resolved by
  a new `@ZIP_PREFIX@` (or similar) substitution the generator fills per
  product, the same way `@COMP@`/`@brand@` already work.

Both changes are additive parameterization of the *existing* shared-module
shape, not a rewrite — they would not, on today's evidence, need to touch
Burrowee's arm of the module at all (Burrowee already supplies its own
literal `@brand@-${COMP}` and 4-arm case, which would become the `PREFIX`/
component-list value for that product). This is a measurement, not a
recommendation: whether that generalization is worth doing — versus leaving
Clawee and Umbree each with their own near-identical LOCAL FORK block — is
a follow-on decision for someone with the full three-product picture, not
made here.

## `DOWNLOADS_BASE` — confirmed to skip cleanly when unset

Umbree's R2/downloads mirror is disabled by default
(`UMBREE_DOWNLOADS_BASE` unset → `DOWNLOADS_BASE=""`). Verified by reading
the generated `umbree/install.sh` after this adoption
(`grep -n 'DOWNLOADS_BASE' umbree/install.sh`): every use of `$DOWNLOADS_BASE`
against the network is behind an explicit `[ -n "$DOWNLOADS_BASE" ]` guard —
once in the (LOCAL FORK, unmodified) version-resolution block:

```sh
if [ -z "$TAG" ] && [ -z "$DL_BASE" ] && [ -n "$DOWNLOADS_BASE" ]; then
    info "GitHub API unreachable — trying $DOWNLOADS_BASE/$COMP/latest.json"
    ...
```

and once in the (LOCAL FORK, unmodified) download block:

```sh
if [ -z "$DL_BASE" ] && [ -n "$DOWNLOADS_BASE" ]; then
    info "mirrors failed; trying downloads mirror: $DOWNLOADS_FILE_BASE/$1"
    ...
```

`DOWNLOADS_FILE_BASE="$DOWNLOADS_BASE/$COMP/$STAMP"` is computed unconditionally
(it's a string concatenation of an empty variable, not a network call), but it
is never passed to `curl` unless the guard above passes. With
`DOWNLOADS_BASE` empty, both legs are skipped entirely — the installer never
attempts a request against an empty or malformed base URL; it falls straight
through to the existing `fail` with `${DOWNLOADS_BASE:-the downloads mirror}`
naming the mirror as unreachable/disabled in the combined error message. This
behaviour is unchanged by the adoption, since neither retained block was
edited.

## Sync verdict

`tools/sync-modules.sh` was run against both the Burrowee worktree and the
Clawee worktree after adoption; see `task-11-report.md` for the full output
of both runs. Every **adopted** module (the eight above) reports `ok` against
both — Umbree's copies match the shared module text byte-for-byte in both
directions, confirmed by matching `tools/modules/MODULES.lock` entries.
`download` and `version-resolve` are not part of that "must match" set in
the same sense — Umbree's `.sh` files for them are still on disk under
`tools/modules/` (so `lock-modules.sh` and future re-evaluation see them),
but no `@INCLUDE:` line in Umbree's template references either, matching
Clawee's Task 10 posture for the same two modules.

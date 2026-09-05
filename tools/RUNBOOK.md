# Release runbook — the hazards the tooling does not prevent

Operator notes for `tools/release.command`, `tools/release.sh` and the beta
channel. Everything a script *can* refuse, it refuses; this page is the rest.
Hosts, static paths and credentials are never written here — they come from
the operator's sealed configuration at cut time (`README.md`, "How releases are
made"). Placeholders: `<RELEASE_HOST>`, `<STATIC_DIR>`, `<downloads-base>`.

## Unpushed `main` refuses the cut

The cut-origin guard (`tools/release_origin.sh`) asserts this repo is on
`main`, clean, and `== origin/main` — in `release.command` before `rkit build`,
and again in `release.sh` (there with a tolerance for exactly the version bump
`rkit build` just staged). Any local commit that is not on `origin/main`
refuses the cut, with the count and the fix. That includes:

- a `[RELEASED]` marker from a previous cut that was never pushed
  (`release.command` pushes each marker itself; a by-hand `release.sh` does
  not — push it);
- ordinary commits made on `main` and not pushed. Push them, or move them to
  a branch. The guard is what makes the README's claim true; it does not know
  which commits are yours.

Under `--dry-run` the guard reports (`⚠`) instead of refusing, so a rehearsal
still tells you what a real cut would trip on.

## Batched cuts push the marker between components

`COMPONENTS="umbreed umbree"` is two cuts in sequence. Each publish records a
marker commit, and the launcher pushes it before the next component starts —
because the next component's cut origin check would otherwise find this repo
one commit ahead and refuse. If the push fails, the run stops after the first
component with it published and unrecorded on the remote; push the marker by
hand, then re-run with the remaining components only (the published one's tag
already exists and would be refused).

## Beta: what the twins are, and what closing does not do

`<comp>/beta.install.sh` and `<comp>/beta.version.js` are rendered only while
`versions/<comp>.beta.stamp` exists, and scp'd to `<RELEASE_HOST>:<STATIC_DIR>/<comp>/`
beside `install.sh` / `version.js`. Their names and URLs are a contract with
the cli: `umbree update` on a beta host (feature 02 of the brand-root project)
fetches `<base>/umbree/beta.version.js` and re-runs
`<base>/umbree/beta.install.sh`. Never rename or relocate them; a rename is a
breaking change to every installed beta host.

Closing a cycle (README "Beta channel") removes the two `versions/<comp>.beta*`
files, and the next `gen-bootstraps.sh` run deletes the **local** twins. **No
tool touches the served copies.** Two acceptable end states:

- **Leave them.** The served `beta.install.sh` keeps working: its resolution
  is "newest of beta-or-stable, tie to stable", the closed cycle's stable
  release wins the tie, and a beta host that runs it installs stable. The
  `<downloads-base>/<comp>/beta/latest.json` it reads still names the last beta
  (retention keeps one), which is older than stable by then.
- **Remove them** over ssh — `rm <STATIC_DIR>/<comp>/beta.install.sh
  <STATIC_DIR>/<comp>/beta.version.js` on `<RELEASE_HOST>`. A beta host's
  `umbree update` then 404s until the cli falls back to stable; prefer leaving
  them until every beta host has graduated.

Never "fix" a beta host by pointing it at `install.sh`: a channel flip is a
separate manual migration, and doing one silently migrated a fleet once
already. The twin resolving to stable *is* graduation.

## Beta: the first cut and the seed

The first beta cut of a component takes **no bump flag**: the seed
(`tools/version.sh <comp> --channel beta --seed`, stable minor + 1, patch 0) is
the version. `--bump-patch` on a first cut ships `0.2.1` and leaves `0.2.0`
never released. A seed that does not carry the cycle's minor (someone edited
the file) is a half-opened cycle: re-seed (remove the file, seed again) before
cutting, or the cut ships on the old minor. `rkit build --channel beta` refuses
outright when `versions/<comp>.beta` does not sort above `versions/<comp>`.

## Beta is R2-only

A beta publish creates no GitHub Release. `release.sh --channel beta` refuses
before the tag when `UMBREE_R2_ACCOUNT` / `UMBREE_R2_CREDS` are unset, because
there would be nothing to serve the beta from. The artifacts go to
`<downloads-base>/<comp>/beta/<stamp>/` and the catalog
`<comp>/beta/latest.json` is written **last**, so a reader never sees a catalog
naming bytes that are not there yet. The tag `<comp>/<stamp>` is pushed for
history and for retention; the stable `install.sh` never resolves it (its tag
regex has no `.beta.`).

## Retention: GitHub first, then R2

`CHANNEL=<stable|beta> bash tools/prune-releases.sh --execute` (GitHub
Releases on stable, tags on beta) **before**
`cd tools/r2-mirror && go run ./cmd/r2-prune --channel <stable|beta> --execute`.
Draining R2 first leaves GitHub tags whose bytes are gone. Keep is 10 on stable,
1 on beta; a tag or key matching neither channel's shape is ignored by both
passes, never counted, never deleted. Wiring these into a nightly job is an
operator step outside this repo.

## The stable `umbree` installer until feature 02 lands

`rkit build` ships `install/install.sh` from the component tree when it exists
(the daemon's `install/install.sh.in` already does). The cli tree carries none
until feature 02 of the brand-root project reaches `main`, so a **stable**
`umbree` build falls back to this repo's `inner/umbree/install.sh` and says so
on stderr — a copy that assumes a `service` verb the stable cli may not have.
A **beta** `umbree` build refuses the fallback. Once 02 is on `main`, delete the
fallback branch in `cmd/rkit/build.go` and `inner/umbree/` together.

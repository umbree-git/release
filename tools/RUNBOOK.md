# Release runbook — the hazards the tooling does not prevent

Operator notes for `tools/release.command`, `tools/release.sh`, the manage
service and the dormant beta tooling. Everything a script *can* refuse, it
refuses; this page is the rest. Hosts, static paths and credentials are never
written here — they come from the operator's sealed configuration
(`README.md`, "How releases are made"). Placeholders: `<RELEASE_HOST>`,
`<STATIC_DIR>`, `<downloads-base>`, `<MANAGE_HOST>`, `<MANAGE_DATA_DIR>`,
`<SERVICE_USER>`.

## A cut no longer makes anything public

Since the gated store landed, a stable cut stages the bytes to the private
gated store, registers a `staged` row with the manage service, pushes the tag
and records the marker — and stops. No GitHub Release, no public download, no
`latest.json`, no static surface. The first cut after the land is the first
one that does not go public: nothing reaches users until an operator promotes
the row in the manage console. A runbook step, habit or check that assumed
"cut = live" is wrong now; read it again before relying on it.

## Unpushed `main` refuses the cut

The cut-origin guard (`tools/release_origin.sh`) asserts this repo is on
`main`, clean, and `== origin/main` — in `release.command` before `rkit build`,
and again in `release.sh` (there with a tolerance for exactly the
`versions/<comp>` bump `rkit build` just staged; a staged
`versions/<comp>.stamp` is refused, because the floor is written after a
promote, never at a cut). Any local commit that is not on `origin/main`
refuses the cut, with the count and the fix. That includes:

- a `[RELEASED]` or `[PROMOTED]` marker that was never pushed
  (`release.command` pushes each `[RELEASED]` marker itself; a by-hand
  `release.sh` or `record-promoted.sh` does not — push it);
- ordinary commits made on `main` and not pushed. Push them, or move them to
  a branch. The guard is what makes the README's claim true; it does not know
  which commits are yours.

Under `--dry-run` the guard reports (`⚠`) instead of refusing, so a rehearsal
still tells you what a real cut would trip on.

## Batched cuts push the marker between components

`COMPONENTS="umbreed umbree"` is two cuts in sequence. Each cut records a
marker commit, and the launcher pushes it before the next component starts —
because the next component's cut origin check would otherwise find this repo
one commit ahead and refuse. If the push fails, the run stops after the first
component with it cut and unrecorded on the remote; push the marker by hand,
then re-run with the remaining components only (the cut one's tag already
exists and would be refused).

## `dev` must contain `main` — the sync-back check

The cut-origin guard also asserts, for each component source and for this
repo, that `origin/main` is an ancestor of `origin/dev` (`tools/release_origin.sh`
`check_sync_back`, `dev.md` check 4). A refusal means something that reached
`main` — a previous cut's marker, a `[PROMOTED]` marker, a hotfix, a
`dev → main` release — was never merged back down. Fix it on `dev`:
`git merge origin/main` (a merge, never a rebase — a rebase drops the merge
commits and takes `main` back out of `dev`), push, re-run. A PR merged into
`main`, a hotfix included, therefore has to be synced into `dev` **before** the
next cut from that repo, or the cut refuses.

The launcher keeps this repo's side true as it goes: after each marker push it
carries the marker into `dev` (`sync_marker_into_dev`) — a fast-forward when
`dev` has nothing `main` lacks, otherwise a merge commit `Merge branch 'main'
into dev` (dev first parent, the marker second), built with `git merge-tree
--write-tree` and `git commit-tree` so no branch or working tree is touched
(git 2.38 or newer), and pushed without force. It stops after that component,
its marker already pushed, in two cases: the merge **conflicts** (the paths
are named), or the push is refused because **`dev` moved during the cut** — it
is never forced. Then merge `main` into `dev` by hand, push, and re-run with
the cut components dropped. Clawee shipped the guard without this sync on
2026-09-14 and its second component, `claweed`, refused on the first one's
marker — which is why the two landed together here.

## Registration failed: re-register, then finish the cut by hand

`release.sh` treats any refusal from the intake, and a row that does not read
back `staged` with the cut's stamp, as a stop. At that point the bytes are in
the gated store, nothing is tagged or marked, and nothing is public. Fix the
cause the message names (an expired or wrong `UMBREE_MANAGE_URL`, the release
key, the service itself), then, from this repo with the sealed settings
loaded:

```sh
bash tools/release.sh --register-only <comp> <stamp>
```

It registers from `dist/<stamp>/gated-receipt.json`, reads the row back, and
does nothing else. Then finish the two steps the cut did not reach, exactly as
it would have:

```sh
git tag -a <comp>/<stamp> -m "<comp> <stamp>"
git push origin refs/tags/<comp>/<stamp>
git add versions/<comp>
git commit --allow-empty -m "[RELEASED: <comp>] $(date -u +%Y-%m-%d) <stamp>"
git push origin HEAD:refs/heads/main      # then merge main into dev, as above
```

Do not re-run `--distribute-only` for the same stamp: it would stage the bytes
a second time.

## After a promote: record the floor

The promote in the console writes `<comp>/latest.json` and republishes the
served installer with the promoted stamp as its version floor. This repo's
copy follows, once the public answers:

```sh
tools/promote-check.sh <comp> stable --expect <stamp>    # exit 0 = live
tools/record-promoted.sh <comp> <stamp>
git push origin HEAD:refs/heads/main                      # then merge main into dev
```

`record-promoted.sh` asks the same check itself and refuses (writing nothing)
on not yet, cannot tell, a newer version live, or an answer naming another
stamp. It needs a clean tree on `main`.

## The static republish failed

Promote and yank end by republishing `<comp>/install.sh`, `<comp>/version.js`,
the public key and the site page to the service's `--static-dest`. A failure
is a `static` error event in the promote's stream and a warning in the
service's log; the promote or yank itself stands, and installers keep
resolving the live manifest. Until the republish succeeds the served installer
still bakes the old floor — after a yank that means it refuses the successor
as below its floor. Republish from the overview's **Republish static** control,
or on `<MANAGE_HOST>` as `<SERVICE_USER>`, with the service's environment file
loaded (it carries the R2, public-base and static settings):

```sh
umbree-release-manage publish-static <comp> --data-dir <MANAGE_DATA_DIR>
```

It renders from the live manifest under the channel lock, so it is safe beside
`serve`.

## Run the admin verbs as the service user

Every `umbree-release-manage` verb that opens the catalog (`backfill`,
`retain`, `publish-static` and the `admin` verbs) runs on `<MANAGE_HOST>` as
`<SERVICE_USER>` (`sudo -u <SERVICE_USER>`), never as another account and
never as root. The verbs refuse another account before they create a lock
file, because a lock file it owned would lock `serve` out of that channel.
The same holds for anything else that opens the catalog, a `sqlite3` shell
included: SQLite's `-wal` and `-shm` files are created by whoever opens the
database first, and one owned by another account can leave the running
service unable to open its own catalog.

## `backfill` runs beside `serve`

`umbree-release-manage backfill` is a separate process on the same catalog.
Before running it, stop `serve`, or confirm in the console that no promote or
yank is in flight. The per-channel lock file serialises the writes, but a
backfill that interleaves with a promote reads a manifest the promote is about
to replace, and a stopped or idle service leaves nothing to reason about.

## `admin mark-yanked` runs beside `serve`

`umbree-release-manage admin mark-yanked <id> --reason <why>` changes the
catalog only, for a manifest an operator already pulled by hand. Before
running it, stop `serve`, or confirm in the console that no promote or yank is
in flight — the same precondition as `backfill`, for the same reason.

## Emergency: pulling the last release (yank with no successor)

The service refuses to yank a release that has no public successor with its
bytes present; no service path deletes `latest.json`. Pulling the last release
is this by-hand procedure. Before any step, stop `serve`, or confirm in the
console that no promote or yank is in flight. Then, with your own R2 tooling
and credentials (never the service's), either:

- **delete** `<comp>/latest.json` on the public bucket — every install fails
  on the missing manifest until the next promote; or
- **replace** it with the manifest of an older release that is still public
  with its bytes present, then run
  `umbree-release-manage backfill --component <comp> --data-dir <MANAGE_DATA_DIR>`
  so that row becomes current.

Then mark the pulled row, audited:

```sh
umbree-release-manage admin mark-yanked <id> --data-dir <MANAGE_DATA_DIR> --reason "<why>"
```

After a replace, republish the static surface (`publish-static <comp>`, above)
so the served installer's floor drops to the manifest's stamp — until then it
refuses the older release — and record that stamp here with
`record-promoted.sh <comp> <older stamp>`. Neither branch re-opens anything
older than the pulled release to promote: the promote floor is the newest
release that was ever public, yanked included. The fix is a new cut.

## Retention runs in the manage service

Stable retention is the manage service's: the gated and public windows that
`README.md` "Retention" states run after every registration and promote, from
the nightly `umbree-release-manage retain` timer, and from the console's clean
controls. `retain` is a separate process; it takes the same per-channel lock
file as `serve`. Stable tags are never deleted: `prune-releases.sh` refuses
`CHANNEL=stable` and an unset `CHANNEL`.

Pins are catalog rows (`umbree-release-manage admin pin <comp> <stamp>`); the
old `tools/retain-permanent` file is gone. The listing-based `r2-prune`
(`tools/r2-mirror/cmd/r2-prune`) and `prune-releases.sh` are manual tools for
the dormant beta channel only; nothing runs them for stable. `r2-prune
--protect <file>` still takes a pin list, which is the operator's to write for
that run.

## New bootstraps reach users at the first promote

The bootstraps on the static host are what users run. Until they are replaced,
the ones served there resolve GitHub Releases first and keep installing the
last GitHub Release. The regenerated bootstraps read `<comp>/latest.json` and
nothing else, so they must not go out before a promote has written that
manifest: every install would fail on the missing manifest. They reach the
static host through the manage service's static republish at the end of the
first promote, and not before; do not copy them there by hand earlier.

Existing GitHub Releases are left where they are, so a bootstrap a user cached
keeps working. Deleting them is the operator's call, after the new bootstraps
are live.

## Beta: dormant

Umbree has no beta stage (operator, 2026-10-09): every cut is stable. The beta
notes below describe tooling that is kept but unused. Unlike the stable cut,
`release.sh --channel beta` publishes at the cut, to the public bucket, with no
gated store or promote in front of it.

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
  (the beta window in README "Retention" keeps it), which is older than stable
  by then.
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
history and for retention; the stable `install.sh` never resolves it (its
stamp check has no `.beta.`). Beta retention is by hand:
`CHANNEL=beta bash tools/prune-releases.sh --execute` (tags) **before**
`cd tools/r2-mirror && go run ./cmd/r2-prune --channel beta --execute`;
draining R2 first leaves tags whose bytes are gone.

## The stable `umbree` installer until feature 02 lands

`rkit build` ships `install/install.sh` from the component tree when it exists
(the daemon's `install/install.sh.in` already does). The cli tree carries none
until feature 02 of the brand-root project reaches `main`, so a **stable**
`umbree` build falls back to this repo's `inner/umbree/install.sh` and says so
on stderr — a copy that assumes a `service` verb the stable cli may not have.
A **beta** `umbree` build refuses the fallback. Once 02 is on `main`, delete the
fallback branch in `cmd/rkit/build.go` and `inner/umbree/` together.

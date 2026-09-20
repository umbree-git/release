# release — the Umbree public install channel

- Repo: `umbree-git/release` · `gh.account = umbree-git` (several accounts are
  in play across sibling channels, so call the GitHub CLI through the
  account-scoped wrapper your environment provides — never bare `gh`, which
  uses whichever account is globally active) · Go + shell.
- **Kind `code`, on the branch spine**: `dev` is the integration point, `main`
  is the publishing branch, and there is **no permanent `beta` git branch** —
  Umbree has no beta line (registry ruling, 2026-09-12). A release repo is code
  because it carries the installers, bootstraps and tooling (`dev.md`).
  **Do not read `README.md`'s "Beta channel" as contradicting that**: that is the
  *download* channel a published build can sit on, and `tools/adopt-beta-version.sh`
  serves it. A beta **cycle** — the `beta` branch, `beta.md` — is what Umbree does
  not have.
- What it publishes: **two components, no dispatcher** — `umbree` (the CLI, a
  local SOCKS5 listener on the user's machine) and `umbreed` (the home-exit
  daemon on a server). Each binary is invoked directly.
- **Cross-brand dependency, and it is a hard one:** `umbreed` requires a running
  `burrowee-gateway` on the same host, and `umbree` pulls `burrowee-cli`. Naming
  Burrowee here is legitimate for exactly that reason — the registry's `May name`
  cell records it (`workspace.md`); no other brand family belongs in this repo.

## Layout

| Path | What lives there |
|---|---|
| `cmd/rkit` | the release kit — `rkit build` assembles and signs a cut |
| `tools/` | the cut itself: `release.command` (desktop launcher), `release.sh`, the bootstrap/JSONP generators, retention, hygiene checks |
| `tools/RUNBOOK.md` | **the hazards the tooling cannot prevent** — read before a cut |
| `tools/modules/`, `tools/lock-modules.sh`, `sync-modules.sh` | shared shell modules and their pinning |
| `versions/<component>`, `versions/<component>.stamp` | the released version and its full stamp, per component |
| `inner/`, `site/`, `ops/nginx` | the verified inner installer, the channel's pages, the serving config |
| `config/apple-account`, `config/apple-home` | which signing identity plugin this repo uses (`apple-signing.md`) |
| `umbree/`, `umbreed/` | per-component release assets and bootstraps |
| `umbree-release.pub` | the public minisign key downloads are verified against |

Tests sit beside what they test as `<name>.test.sh` for shell and `*_test.go`
for `cmd/rkit`. A change to a tool changes its test in the same commit.

## Cutting a release

The whole procedure is `README.md` → "How releases are made", with the beta
channel in "Beta channel". Four repo-specific facts that decide whether a cut
can run at all:

- **Launch `tools/release.command` in a real desktop session**, unmodified.
  Signing and notarizing are different capabilities: `rcodesign` signs in any
  session, while `notarytool` needs a per-user bootstrap namespace and, in a
  background or daemon-hosted shell, SIGTRAPs with no submission id — which
  reads like a vendor outage and is not one (`release.md`).
- **An unpushed `main` refuses the cut.** `tools/release_origin.sh` asserts this
  repo is on `main`, clean and `== origin/main`, including a `[RELEASED]` marker
  from an earlier cut that was never pushed. `--dry-run` reports instead of
  refusing, so a rehearsal still shows what a real cut would trip on.
- **A `main` not merged back into `dev` refuses the cut**, for this repo and
  each component source (`check_sync_back`: `origin/main` must be an ancestor
  of `origin/dev`). The launcher carries every marker into `dev` right after
  pushing it, so a batch passes it: a fast-forward, or a merge (no checkout, no
  force) when `dev` has diverged; it stops only on a conflict or a `dev` that
  moved mid-cut, naming the manual merge (`tools/RUNBOOK.md`).
- **Hosts, static paths and credentials are never written in this repo.** They
  come from the operator's sealed configuration at cut time; documentation uses
  `<RELEASE_HOST>`, `<STATIC_DIR>`, `<downloads-base>` (`secrets.md`).

The agent chain ends at the **cut**: build, cut, report the stamp, sync `main`
back down into `dev`. Promoting a cut to the public surface, minting invite
links and installing on a node are operator steps (`release-management.md`).

## Global policy

Load your environment's global agent policy — its hard rules and its
always-on concepts — every session and follow them; its full per-topic
guidelines are loaded on task. Your environment names where that tree lives.
This file holds only what is true of *this* repo.

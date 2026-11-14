# release — the Umbree public install channel

- Repo: `umbree-git/release` · `gh.account = umbree-git` (several accounts are
  in play across sibling channels, so call the GitHub CLI through the
  account-scoped wrapper your environment provides — never bare `gh`, which
  uses whichever account is globally active) · Go + shell.
- **Kind `code`, on the branch spine**: `dev` is the integration point, `main`
  is the publishing branch, and there is **no permanent `beta` git branch** —
  Umbree has no beta line (registry ruling, 2026-09-12) and no beta stage:
  work goes `dev` → `main` and every cut is stable (operator, 2026-10-09). A
  release repo is code because it carries the installers, bootstraps and
  tooling (`dev.md`).
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
| `cmd/umbree-release-manage`, `internal/manage/` | the manage service: the catalog intake a cut registers through, and the operator console that promotes and yanks |
| `tools/` | the cut itself: `release.command` (desktop launcher), `release.sh`, the bootstrap/JSONP generators, retention, hygiene checks |
| `tools/RUNBOOK.md` | **the hazards the tooling cannot prevent** — read before a cut |
| `tools/modules/`, `tools/lock-modules.sh`, `sync-modules.sh` | shared shell modules and their pinning |
| `versions/<component>`, `versions/<component>.stamp` | the version the last cut carried, and the stamp of the newest promoted release (the installers' version floor, written by `tools/record-promoted.sh`), per component |
| `inner/`, `site/`, `ops/nginx` | the verified inner installer, the channel's pages, the serving config |
| `config/apple-account`, `config/apple-home` | which signing identity plugin this repo uses (`apple-signing.md`) |
| `umbree/`, `umbreed/` | per-component release assets and bootstraps |
| `umbree-release.pub` | the public minisign key downloads are verified against |
| `cve-waivers.tsv` | the CVE gate's waivers, one file for both components: operator-ruled rows, each expiring within 90 days |
| `cve-gate.tsv` | the CVE gate's arguments as data, per component (header `component`, `key`, `value`, tab-separated): `source` (relative to the brand root), `gowork`, each `go` module dir (relative to the source), `waivers` (relative to this repo). Read by `rkit build`, `rkit cve-scan` and the machine's daily scan; never code, never `--report` |

Tests sit beside what they test as `<name>.test.sh` for shell and `*_test.go`
for `cmd/rkit`. A change to a tool changes its test in the same commit.

## Cutting a release

The whole procedure is `README.md` → "How releases are made", with the beta
channel in "Beta channel". Seven repo-specific facts that decide whether a cut
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
- **The cut's `go` is pinned.** Every cut builds five zips per component: the
  four stock targets and `darwin-amd64-legacy` (Intel macOS 10.15–11), which
  `rkit build` compiles through the `crypto/x509` overlay in
  `tools/legacy/darwin/` (`cmd/rkit/legacy.go`). The overlay is valid for one
  stdlib only, so `rkit build` refuses before stamping unless `go version`
  prints exactly `tools/legacy/darwin/GO_VERSION`; the env file
  `tools/release.command` sources must put that SDK first on `PATH` with
  `GOTOOLCHAIN=local`. `release.sh --distribute-only` refuses a stage without
  `<comp>-darwin-amd64-legacy.zip`. The overlay is a pinned copy of
  Burrowee's; `tools/legacy/darwin/README.md` says how to re-sync it.
- **Every stable build runs the shared CVE gate.** `rkit build` runs the
  script `CVE_GATE` names (by default the playbook's shared `cve-gate.sh`
  under `$HOME`; this repo keeps no gate of its own) once the stamp is known and
  before any compile, sign, stage, register or tag, with `--report
  dist/<stamp>/cve` followed by the component's `cve-gate.tsv` rows in file
  order — today `--gowork off --go <component source> --waivers
  cve-waivers.tsv` (`cmd/rkit/cve_gate_data.go`). The file is refused, and the
  gate does not run, on an unknown key, a row that is not exactly three
  non-empty tab-separated fields (no trimming; only `#` lines and empty lines
  are skipped), a carriage return (LF line endings only), a
  `..` segment or an absolute path, a
  component set unlike `rkit components`, or a `go.mod` under the source
  (outside `testdata`, `vendor`, `.`- and `_`-prefixed dirs) that the file does
  not list, or a listed `go` dir with none — so a module added to the cli or
  daemon repo needs a `go` row here before the next cut. Its output streams into the launcher's log; a non-zero
  exit, or a gate that is missing or not executable, fails the build with
  `cve gate: exit <n>`, and no flag skips it. The launcher's final summary
  prints each component's `cve:` line for the release task. A beta build does
  not run it and says so (operator ruling: stable cuts only). The root
  `cve-waivers.tsv` serves both components: each row is operator-ruled and
  expires at most 90 days after its `waived_at`, so it is renewed or dropped
  before then. The shipped surfaces carry no third-party browser file, so the
  call has no `--js-manifest`; a vendored third-party browser file added to
  any shipped surface needs a manifest row and a `--js-manifest` in the call.
  `CVE_GATE` is the test seam (`tools/RUNBOOK.md`).
- **The daily CVE scan is report-only.** `tools/cve-scan.sh [--report-dir
  <dir>]`, run from an export of `origin/main`, scans each component's source
  at its own `origin/main` (a `git fetch` then a `git archive` export of the
  checkout `UMBREE_SRC_<COMP>` names, else `$CODING_ROOT/Umbree/<cli|daemon>/code/main`,
  `UMBREE_BRAND_ROOT` overriding the brand folder) through `rkit cve-scan`,
  which takes `--src` explicitly and hands the gate the cut's own arguments
  from `cve-gate.tsv` (`cveGateArgs`), refusing the file as the cut does. It prints
  `umbree/<comp>: <cve: line>` per component and `waivers: <path>`, exits
  `0` clean, `1` on a finding, `2` when a scan could not run, skips a
  beta-only component, and never stages, signs, tags, registers, commits,
  pushes or moves a checkout; `RKIT` and `CVE_GATE` are its test seams
  (`tools/cve-scan.test.sh`).
- **Hosts, static paths and credentials are never written in this repo.** They
  come from the operator's sealed configuration at cut time; documentation uses
  `<RELEASE_HOST>`, `<STATIC_DIR>`, `<downloads-base>` (`secrets.md`).

**The bootstraps and the site page go live through the console, not the
cut.** `tools/bootstrap.template.sh`, `tools/modules/*.sh` and
`site/index.html` are embedded in `umbree-release-manage` (`assets.go`), which
renders `<comp>/install.sh` at each promote or static republish against the
promoted stamp; the committed `<comp>/install.sh` must equal that render
(`internal/manage/static/agreement_test.go`). A bootstrap change therefore
reaches users only once the operator deploys the console and then promotes.
A `platform-detect` v2 bootstrap promoted over a four-zip stamp sends an Intel
Mac below macOS 12 to a legacy zip that does not exist, so every pre-legacy
staged stamp is promoted or abandoned before that deploy (`tools/RUNBOOK.md`).
The site page claims the legacy build for both components, so promote the
two legacy-carrying stamps back to back.

The agent chain ends at the **cut**: build, stage the bytes to the private
gated store, register the `staged` row with the manage service and read it
back, tag, mark, report the stamp and the row, and sync `main` back down into
`dev`. Nothing an agent runs makes a release public. Promoting a cut to the
public surface, yanking, re-running retention, republishing the static
surface, minting invite links and installing on a node are operator steps
(`release-management.md`); after a promote, `tools/record-promoted.sh` records
the version floor once `tools/promote-check.sh` answers live.
Promote and yank run in the manage console (`umbree-release-manage serve`), an
authenticated operator surface: password, then a TOTP second factor, CSRF on
every write and a two-step confirm on promote and yank. Its admins are added on
the service host with `umbree-release-manage admin add`; there is no sign-up.
Agents do not sign in to it, add admins, or deploy it. Its address reaches the
cut only as the sealed `UMBREE_MANAGE_URL`, and no host is named here.

## Global policy

Load your environment's global agent policy — its hard rules and its
always-on concepts — every session and follow them; its full per-topic
guidelines are loaded on task. Your environment names where that tree lives.
This file holds only what is true of *this* repo.

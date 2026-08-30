# Umbree release channel

Public, signed, self-service install channel for the `umbree` command-line
client and the `umbreed` home-exit daemon. Every download is verified
end-to-end (minisign signature → SHA-256 → unzip → exec a verified inner
installer).

Two components are published here:

| Component | Binary | What it is | Cross-channel dependency |
|---|---|---|---|
| `umbree` | `umbree` | the Umbree command-line client | `burrowee-cli` (from `release.burrowee.com/cli`) when missing |
| `umbreed` | `umbreed` | the Umbree home-exit daemon + its boot service | none — self-contained |

There is **no universal dispatcher** — each component's binary is invoked
directly.

## Install

```sh
# Client
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbree/install.sh | sh
# Daemon (run AS YOUR USER — it escalates with sudo only for the one step that needs root)
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbreed/install.sh | sh
```

Each installer detects your OS/arch, resolves the latest published release for
that component, downloads the zip + `SHA256SUMS.txt` + `SHA256SUMS.txt.minisig`,
**verifies the minisign signature against the baked public key**, checks the
SHA-256 of the zip, then unzips and runs the inner installer. If `minisign` is
missing, the installer provides it first — through your package manager where
the installer has root (or, for a user-level install, passwordless sudo),
otherwise the official upstream 0.12 build whose SHA-256 is pinned inside the
installer itself and whose own signature is then checked against upstream's
key — and refuses to continue if neither works; it never runs an unverified
verifier.

- **umbree** lands in `$HOME/.local/bin` (override with `PREFIX`), then
  ensures `burrowee-cli` is present — installed from burrowee's own public
  channel (`release.burrowee.com`) if missing. Nothing is bundled.
  An uninstall (`UMBREE_UNINSTALL=1`) never touches your package manager; if
  it had to fetch the pinned `minisign` build to verify its payload, that
  single file stays in the bin directory afterwards.
- **umbreed** lands its binary in `$HOME/.local/bin` (override with `PREFIX`),
  then installs + loads a system boot unit (`/Library/LaunchDaemons` on
  macOS, `/etc/systemd/system` on Linux) — the only step that needs root; the
  daemon itself never runs as root. `UMBREED_NO_SERVICE=1` installs the
  binary only, skipping the service entirely. `UMBREED_UNINSTALL=1` unloads
  and removes the service, then the binary.

## Verify by hand

The signing public key lives in this repo (`umbree-release.pub`) and is
mirrored at `https://release.umbree.org/umbree-release.pub`:

```sh
minisign -V -P "$(cat umbree-release.pub | tail -n1)" \
  -m SHA256SUMS.txt -x SHA256SUMS.txt.minisig
f=<file>                                      # the file you downloaded
want=$(awk -v f="$f" '{ n = $2; sub(/^\*/, "", n); if (n == f) { print $1; exit } }' SHA256SUMS.txt)
got=$(shasum -a 256 "$f" | awk '{print $1}')  # sha256sum "$f" on Linux
if   [ -z "$want" ];        then echo "NO ENTRY for $f in SHA256SUMS.txt — do not install"
elif [ "$want" = "$got" ];  then echo "OK $f"
else                             echo "MISMATCH for $f — do not install"; fi
```

A failed signature check means the bytes are untrusted — do not install them.

The checksum block compares one digest by hand on purpose. `shasum -c
--ignore-missing` is what the installers used to run, and the stock `shasum` on
a pre-2016 macOS rejects that option outright — which read as "tampered". Its
obvious replacement is worse: `sha256sum -c` exits **0** on an empty or
malformed checklist, so a mistyped filename would report success having verified
nothing. Selecting the entry by exact name and shouting when there is none is
what the installer's own gate does.

## Pin a version

`UMBREE_VERSION` pins the release tag (`<comp>/<stamp>`) for either installer:

```sh
UMBREE_VERSION=umbree/v0.1.0.2026.07.16.aaaaaaaa \
  curl -fsSL https://release.umbree.org/umbree/install.sh | sh

UMBREE_VERSION=umbreed/v0.1.0.2026.08.30.aaaaaaaa \
  curl -fsSL https://release.umbree.org/umbreed/install.sh | sh
```

Unset → the installer resolves the newest release for that component.

## Supported platforms

| OS | arm64 | amd64 |
|---|---|---|
| macOS (darwin) | ✓ | ✓ |
| Linux | ✓ | ✓ |

Windows is not supported.

## How releases are made

Building and publishing are two separate steps:

- **`rkit build`** (Go orchestrator on
  [`release-kit`](https://github.com/burrowee-git/release-kit)) **produces**
  the artifacts: CVE gate (govulncheck), version stamp, `GOWORK=off` compile
  for all four targets, Developer-ID sign + notarize (darwin), per-target zips
  + `SHA256SUMS.txt` + minisign signature → `dist/<stamp>/`. **`--public`** is
  the standard ship path: it turns on Apple sign+notarize and forces the CVE
  gate on. The Apple account comes from `config/apple-account` (operator-local
  and untracked) unless `APPLE_ACCOUNT`/`APPLE_ACCOUNT_DIR` is already set.
- **`tools/release.sh --distribute-only <umbree|umbreed> <stamp>`** **publishes**
  a staged `dist/<stamp>/`: GitHub Release on this repo, bootstrap + `version.js`
  render, scp to the static host, `[RELEASED]` marker commit. There is no
  shell build path — `rkit build` is the only builder.

Built binaries for the private component sources (`umbree-git/cli` for
`umbree`, `umbree-git/daemon` for `umbreed`) are published as **GitHub
Release assets on this repo** (the sources are private and can't be `curl`'d
anonymously). The static bootstrap scripts are mirrored to
`release.umbree.org` (nginx + Cloudflare).

## Keys

- The minisign **public** key is committed here as `umbree-release.pub`.
- The minisign **secret** key is age-sealed in `umbree-git/release.dp`
  (private, local-only); the age identity lives at
  `~/.age/umbree-release.txt`. At cut time it is decrypted to a chmod-600
  tempfile, passed to `rkit build --sign-key`, then shredded.

- `umbree-git/release` (PUBLIC). Trunk: `main`. gh.account: `umbree-git`.
- Call gh via `~/bin/ghp`, never bare `gh`.

## Status

Built on release-kit. `umbree` is LIVE — latest cut v0.1.5 (signed+notarized,
GitHub-hosted); release.umbree.org serving from the release host (see
`ops/README.md`). `umbreed` tooling is in
place (this repo builds, tags, and publishes it) but no `umbreed` release has
been cut yet.

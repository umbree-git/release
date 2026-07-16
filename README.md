# Umbree release channel

Public, signed, self-service install channel for the `umbree` command-line
client. Every download is verified end-to-end (minisign signature → SHA-256 →
unzip → exec a verified inner installer).

One component is published here:

| Component | Binary | What it is | Cross-channel dependency |
|---|---|---|---|
| `umbree` | `umbree` | the Umbree command-line client | `burrowee-cli` (from `release.burrowee.com/cli`) when missing |

There is **no universal dispatcher** — the `umbree` binary is invoked directly.

## Install

```sh
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbree/install.sh | sh
```

The installer detects your OS/arch, resolves the latest published release,
downloads the zip + `SHA256SUMS.txt` + `SHA256SUMS.txt.minisig`, **verifies the
minisign signature against the baked public key**, checks the SHA-256 of the
zip, then unzips and runs the inner installer. `umbree` lands in
`$HOME/.local/bin` (override with `PREFIX`).

**Runtime dependency:** umbree's carrier delegates to the burrowee daemon —
the installer ensures `burrowee-cli` is present, installing it from burrowee's
own public channel (`release.burrowee.com`) if missing. Nothing is bundled.

## Verify by hand

The signing public key lives in this repo (`umbree-release.pub`) and is
mirrored at `https://release.umbree.org/umbree-release.pub`:

```sh
minisign -V -P "$(cat umbree-release.pub | tail -n1)" \
  -m SHA256SUMS.txt -x SHA256SUMS.txt.minisig
shasum -a 256 -c --ignore-missing SHA256SUMS.txt   # or sha256sum on Linux
```

A failed signature check means the bytes are untrusted — do not install them.

## Pin a version

`UMBREE_VERSION` pins the release tag (`umbree/<stamp>`):

```sh
UMBREE_VERSION=umbree/v0.1.0.2026.07.16.aaaaaaaa \
  curl -fsSL https://release.umbree.org/umbree/install.sh | sh
```

Unset → the installer resolves the newest release.

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
  + `SHA256SUMS.txt` + minisign signature → `dist/<stamp>/`.
- **`tools/release.sh --distribute-only umbree <stamp>`** **publishes** a
  staged `dist/<stamp>/`: GitHub Release on this repo, bootstrap + `version.js`
  render, scp to the static host, `[RELEASED]` marker commit. There is no
  shell build path — `rkit build` is the only builder.

Built binaries for the private component source (`umbree-git/cli`) are
published as **GitHub Release assets on this repo** (the sources are private
and can't be `curl`'d anonymously). The static bootstrap scripts are mirrored
to `release.umbree.org` (nginx + Cloudflare).

## Keys

- The minisign **public** key is committed here as `umbree-release.pub`.
- The minisign **secret** key is age-sealed in `umbree-git/release.dp`
  (private, local-only); the age identity lives at
  `~/.age/umbree-release.txt`. At cut time it is decrypted to a chmod-600
  tempfile, passed to `rkit build --sign-key`, then shredded.

- `umbree-git/release` (PUBLIC). Trunk: `main`. gh.account: `umbree-git`.
- Call gh via `~/.claude/bin/ghp`, never bare `gh`.

## Status

Built on release-kit; first cut (umbree v0.1.0) + nsm channel activation pending.

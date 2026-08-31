# Umbree release channel

Public, signed, self-service install channel for the `umbree` command-line
client and the `umbreed` home-exit daemon. Every download is verified
end-to-end (minisign signature → SHA-256 → unzip → exec a verified inner
installer).

Two components are published here:

| Component | Binary | Runs on | What it is | Cross-channel dependency |
|---|---|---|---|---|
| `umbree` | `umbree` | your machine | the command-line client — a local SOCKS5 listener that routes by your rules | `burrowee-cli` (from `release.burrowee.com/cli`), installed for you when missing |
| `umbreed` | `umbreed` | a server you control | the home-exit daemon — the far end your traffic leaves from | **`burrowee-gateway` must already be installed and running on that server.** Not installed for you |

There is **no universal dispatcher** — each component's binary is invoked
directly.

**The gateway is a hard prerequisite, not a nicety.** `umbreed` reaches the
gateway through a root-owned socket inside the gateway's own data directory. On
a server where no gateway is installed there is nothing to connect to: the
daemon starts, finds no socket, and retries — so `systemctl`/`launchctl` report
it active and `umbreed service status` reports it installed while it carries no
traffic at all. Install the gateway first (`release.burrowee.com/gateway`);
`umbreed`'s installer does not do it for you and does not check.

## Quick start

Two machines, in this order. The exit has to exist before a client can be
pointed at it.

### 1. On the server — the exit

Needs a working `burrowee-gateway` on the same box first (see the table above).

```sh
# Confirm the gateway is installed before installing anything. Check for the
# binary rather than running it — this box is already serving through it.
command -v burrowee-gateway

# Then the exit daemon. Run it AS YOUR USER, never under sudo:
# it escalates only for the two steps that need root.
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbreed/install.sh | sh

umbreed service status     # is the boot unit installed
umbreed devices list       # empty for now — no client has knocked yet
```

### 2. On your machine — the client

```sh
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbree/install.sh | sh
```

Then pair it in the control plane: open **Devices**, name the device, pick a
bundle, press **Pair**, and run the command it hands you on the machine itself:

```sh
umbree setup <bundle-b64> <passcode>
umbree                     # opens a SOCKS5 listener on 127.0.0.1:1080
```

The passcode is shown once and expires.

### 3. Pair the client to the exit

Installing both is not enough — the exit does not accept a client it has never
been told about. The first time your client reaches the exit it is recorded as
**pending**, identified by its key fingerprint, and carries no traffic yet.

Back on the server:

```sh
umbreed devices list                 # the client now shows as: pending  <fp>  <label>
umbreed devices approve <fp>         # promote it
```

`list` shows approved and pending side by side, so the fingerprint you approve
is the one you just saw arrive — compare it against what the client reports
rather than approving whatever is newest.

To undo, `umbreed devices revoke <fp>`.

### 4. Check it worked

From the client:

```sh
umbree status                      # is it running, on which ports
umbree probe example.com           # where would this domain go, and why
curl --socks5-hostname 127.0.0.1:1080 https://api.ipify.org
```

`probe` explains the route your rules produce and touches nothing. The `curl`
should answer with the exit's address rather than your own — if it answers with
your own, `probe` will already have told you a rule sends that domain direct.

## Install

```sh
# Client — on your machine
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbree/install.sh | sh
# Exit daemon — on the server, AS YOUR USER (it escalates only where it must)
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
- **umbreed** is the sudo-minimal daemon installer. Run it **as your user**,
  never under `sudo`. It escalates for exactly two steps:

  1. the root-owned `umbreed` binary in `/usr/local/bin` — a boot unit that
     runs as root must not name a path an ordinary account can rewrite, so the
     installer refuses a destination that is not root-owned and unwritable all
     the way up;
  2. the system boot unit (`/Library/LaunchDaemons` on macOS,
     `/etc/systemd/system` on Linux).

  **The daemon runs as root, and its data stays yours.** It has to: the
  gateway's register socket is root-owned inside a directory only root can
  enter, so a daemon running as you cannot open it — it would retry forever
  while every status command reported healthy. The unit therefore names no
  user, and stamps `HOME` to the account that installed it. Everything the
  daemon then creates under that home — `~/.umbree`, its logs, its data
  directory and the device trust store — is handed to you, so
  `umbreed devices list|approve` works unprivileged, as you. `sudo` is not the
  answer to a permissions error there; report it instead.

  `UMBREED_NO_SERVICE=1` installs the binary only, into `$HOME/.local/bin`
  (override with `PREFIX`), with no elevation and no unit. `UMBREED_UNINSTALL=1`
  unloads and removes the service, then the binary.

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
- **`tools/release.command`** runs those two steps, for one or more components,
  in a **desktop session** — and that is not a convenience. `rcodesign` signs in
  any session, but `notarytool` reaches Apple through frameworks that need a
  per-user bootstrap namespace: from a background or daemon-hosted shell it dies
  with no submission id, and the cut can only report `status: unknown`, which
  reads like a vendor outage and is not one. `open tools/release.command` (do not
  run it from a shell — it refuses anything but an Aqua session, a non-root user,
  a non-SSH session and a real terminal). It reads what to cut from a gitignored
  `.release-request` (copy `.release-request.example`), decrypts this channel's
  `RELEASE_HOST`/`STATIC_DIR` and signing key from the operator's sealed
  configuration, and
  pushes each `[RELEASED: <comp>]` marker before the next component starts —
  the pre-flight refuses to cut while this repo is ahead of its remote, so an
  unpushed marker aborts the following component. Output goes to `.release.log`,
  ending in `RELEASE-EXIT:<code>`.

Built binaries for the private component sources (`umbree-git/cli` for
`umbree`, `umbree-git/daemon` for `umbreed`) are published as **GitHub
Release assets on this repo** (the sources are private and can't be `curl`'d
anonymously). The static bootstrap scripts are mirrored to
`release.umbree.org` (nginx + Cloudflare).

## Keys

- The minisign **public** key is committed here as `umbree-release.pub`.
- The minisign **secret** key never appears in this repository in any form. It is
  held encrypted by the operator, decrypted only at cut time to a mode-600
  temporary file, passed to `rkit build --sign-key`, and destroyed afterwards.

- `umbree-git/release` (PUBLIC). Trunk: `main`.

## Status

Built on release-kit. Both components are LIVE — signed, notarized and
published through this repo, with release.umbree.org serving from the release
host (see `ops/README.md`).

| Component | Latest cut |
|---|---|
| `umbree` (client) | `v0.1.8.2026.08.31.46b36734` |
| `umbreed` (exit daemon) | `v0.1.1.2026.08.31.fde2705e` |

Prose lags. `versions/<component>`, the `umbreed/…` and `umbree/…` tags, and the
`[RELEASED: <component>]` marker commits are the authority on what has shipped —
this section said no `umbreed` release existed for as long as two of them did.

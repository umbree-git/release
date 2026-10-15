# Ops — release.umbree.org

Operator activation notes for the public install channel. **Every step below is
OPERATOR-ACTIVATION** — none runs as part of CI or the release script; do them
once by hand on the host, then the manage service republishes the static
surface at the end of every promote and yank (section 7, "Static
destination"). A cut never touches this host.

Host: `<RELEASE_HOST>`. Static surface: `<STATIC_DIR>` (the manage service's
`--static-dest`). Edge: Cloudflare, **Full (strict)** mode.

Both placeholders are supplied by the operator's sealed configuration and are deliberately
absent from this repo. Naming the origin here would let anyone reach it directly
and skip Cloudflare — which is the protection it is behind Cloudflare for — and
listing what else the box serves would turn one disclosure into a map.

The nginx vhost is `ops/nginx/release.umbree.org.conf`.

---

## 1. DNS — OPERATOR

Create an **A record** for `release.umbree.org` in the Cloudflare `umbree.org`
zone → the release host's origin IP, and set it **Cloudflare-proxied** (orange cloud).
Full (strict) means CF validates the origin cert, so a real cert must be in
place on the origin (step 3) before the SSL mode will succeed.

## 2. Install the vhost — OPERATOR

> **Release-host-specific:** this host's `/etc/nginx/nginx.conf` includes only
> `/etc/nginx/sites-enabled/*` — it does **NOT** include
> `/etc/nginx/conf.d/*.conf`. A file dropped under `conf.d/` is silently dead
> (nginx -t passes, reload succeeds, directives never run). It **must** go into
> `sites-enabled/`.

```sh
# OPERATOR, on the release host:
sudo cp ops/nginx/release.umbree.org.conf \
        /etc/nginx/sites-enabled/release.umbree.org.conf
sudo mkdir -p <STATIC_DIR>   # supplied by the operator
```

Do **not** add `default_server` to this vhost — another sites-enabled file
already owns `default_server` on `:443`; a duplicate fails `nginx -t`.

## 3. Issue the origin cert — OPERATOR

Issue an ECDSA cert for `release.umbree.org` via the host's snap certbot
(`/snap/bin/certbot`, version 5.6 — **not** the apt-packaged 0.31 that's on
`PATH`, which doesn't support the DNS plugin in use here):

```sh
# OPERATOR, on the release host:
sudo /snap/bin/certbot certonly \
  --dns-cloudflare \
  --dns-cloudflare-credentials <CF_CREDENTIALS_INI> \
  --key-type ecdsa \
  -d release.umbree.org
```

`<CF_CREDENTIALS_INI>` is the Cloudflare DNS API-token ini file for certbot's
DNS-01 plugin; the real path is held by the operator and never committed here.

Then point the `ssl_certificate` / `ssl_certificate_key` placeholders in the
vhost at the issued paths (`/etc/letsencrypt/live/release.umbree.org/{fullchain,privkey}.pem`
by default).

> **Release-host-specific:** this host's nginx build **rejects `TLSv1.3`** — the vhost
> pins `ssl_protocols TLSv1.2;`. Leave it; raising it to TLSv1.3 fails
> `nginx -t` and aborts the reload.

## 4. Validate + reload — OPERATOR

```sh
# OPERATOR, on the release host:
sudo nginx -t && sudo systemctl reload nginx
```

## 5. Static-dir layout

The manage service writes the static surface into `STATIC_DIR` at the end of
every promote and yank, rendered from the public manifest it just wrote, and
again on a by-hand republish:

```
<STATIC_DIR>/
├── index.html            # site/index.html
├── umbree-release.pub    # signing public key
├── umbree/
│   ├── install.sh         # the bootstrap, with the promoted stamp as its floor
│   └── version.js         # the JSONP version badge for that stamp
└── umbreed/
    ├── install.sh
    └── version.js
```

Nothing else runs on the host as part of publishing: no build and no
dispatcher. The manage service need not run on this host; when it does not,
it uploads the files over sftp with a dedicated key (section 7). The renderer is
held byte-identical to `tools/gen-bootstraps.sh` and `tools/gen-version-jsonp.sh`
by a test.

## 6. Smoke test

```sh
curl -fsSI https://release.umbree.org/                              # 200, text/html
curl -fsSI https://release.umbree.org/umbree/install.sh             # 200, text/x-shellscript
curl -fsSI https://release.umbree.org/umbree/version.js             # 200, application/javascript
curl -fsSI https://release.umbree.org/umbree-release.pub            # 200, text/plain
```

A green install path end-to-end:

```sh
curl -fsSL --proto '=https' --tlsv1.2 https://release.umbree.org/umbree/install.sh | sh
```

## 7. Manage service — OPERATOR

`umbree-release-manage serve` runs the catalog intake and the operator console.
It runs on `<RELEASE_HOST>`, beside the static surface, and binds loopback
`127.0.0.1:8788`. The vhost proxies exactly three paths to it — `/manage/`,
`/api/v1/releases/` and `/healthz` — and serves everything else from
`<STATIC_DIR>`. Its public address reaches the cut only as the sealed
`UMBREE_MANAGE_URL`, the origin `https://release.umbree.org` (the client appends
`/api/v1/releases/…`; a value ending in `/manage` registers at a 404).

The session cookies are `Secure` and scoped to `/manage`, so the console works
only over HTTPS. The only unauthenticated routes are `/healthz` and the intake
under `/api/v1/releases/`. There are no public pages and no badge: the public
manifest is the only machine pointer. The vhost sends no `X-Forwarded-For` and
has no `real_ip`; the service is run without `--trusted-proxy`, so every client
shares one sign-in budget and `admin unlock` is the recovery.

### Provision, once per deployment

In this order, on `<RELEASE_HOST>`, before the first `tools/deploy-manage.sh`:

```sh
# OPERATOR, on <RELEASE_HOST>:
sudo useradd --system --user-group --shell /usr/sbin/nologin \
    --home-dir <MANAGE_DATA_DIR> --no-create-home umbree-release
sudo install -d -o umbree-release -g umbree-release -m 0700 <MANAGE_DATA_DIR>
sudo -u umbree-release sh -c 'umask 077; head -c 32 /dev/urandom > <MANAGE_DATA_DIR>/secret.key'
sudo install -d -m 0750 -g umbree-release /etc/umbree-release-manage
sudo install -m 0640 -g umbree-release /dev/null /etc/umbree-release-manage/manage.env
sudo chgrp umbree-release <STATIC_DIR> && sudo chmod g+w <STATIC_DIR>
ss -ltn | grep ':8788 ' || echo '8788 is free'
```

- **The static directory** `<STATIC_DIR>` must be writable by `umbree-release`
  (owner or group-write) and nothing else of that account's is: promote and yank
  republish into it in-process. On a host whose systemd is older than 232,
  `ProtectSystem=strict` and `ReadWritePaths=` are ignored, so this ownership is
  the control there; the deploy warns once when it sees such a version, and its
  preflight refuses a static directory `umbree-release` cannot write.

- **The TOTP sealing key** `<MANAGE_DATA_DIR>/secret.key`: 32 random bytes, mode
  0600, owned by `umbree-release`. The service refuses to start without it and
  never creates one. It is not sealed anywhere: losing it means
  `admin reset-totp` for every admin.
- **The env file** `/etc/umbree-release-manage/manage.env`, root-owned, `0640`,
  group `umbree-release`, on the host only — never committed, never sealed. It
  holds `UMBREE_MANAGE_LISTEN=127.0.0.1:8788` and the variables below, and **no**
  `UMBREE_MANAGE_TRUSTED_PROXY`.
- **The R2 credentials** as their own `0600` file owned by `umbree-release`,
  outside `/home` (the unit sets `ProtectHome=yes`), named by `UMBREE_R2_CREDS`.
  The operator copies them; an agent never does.
- **Port 8788 must be free** (`ss -ltn`). The deploy's preflight checks it again
  and refuses a port bound by anything but `umbree-release-manage` itself.

### Deploy — `tools/deploy-manage.sh`

The operator runs it from a workstation checkout of `main`. It reads an
operator-local config file, never committed —
`$UMBREE_MANAGE_DEPLOY_CONFIG`, default `~/.umbree/release/manage-deploy.env`,
`KEY=value` lines. `MANAGE_DATA_DIR`, `STATIC_DIR`, `SERVICE_USER`,
`MANAGE_ENV_FILE` and `MANAGE_LISTEN` only feed the workstation lint and must
equal the host's `helper.conf`, which is what the host renders from:

| Key | Default | |
|---|---|---|
| `RELEASE_HOST` | none — required | the ssh host; the connection is `<SSH_USER>@<RELEASE_HOST>` |
| `SSH_USER` | none — required | the non-root deploy account on the host; `root` refuses |
| `MANAGE_DATA_DIR` | none — required | the unit's `ReadWritePaths=` |
| `STATIC_DIR` | none — required | absolute and clean; the vhost's `root` and the unit's second `ReadWritePaths=` entry. Copy it from the sealed server config; the script never decrypts |
| `SERVICE_USER` | `umbree-release` | the unit's `User=`/`Group=`; root refuses |
| `MANAGE_ENV_FILE` | `/etc/umbree-release-manage/manage.env` | the unit's `EnvironmentFile=` |
| `MANAGE_LISTEN` | `127.0.0.1:8788` | must equal the vhost's upstream and the env file's `UMBREE_MANAGE_LISTEN` |
| `MANAGE_URL` | `https://release.umbree.org` | what the verify reads; https with a host name |
| `STAGE_DIR` | `/home/<SSH_USER>/umbree-release-manage-deploy` | the deploy user's own directory, mode 0750; must equal `helper.conf` |

Every path key must be absolute and clean (no `//`, `.`, `..` or trailing `/`).
Values holding `#`, `&`, `'` or whitespace are refused rather than quoted. The
toolchain is not configurable: the build accepts `go1.26.9` only.

```sh
tools/deploy-manage.sh --build-helper  # the helper bundle, for the operator to install; contacts no host
tools/deploy-manage.sh --dry-run       # build, lint, print the plan and sha256; contacts no host
tools/deploy-manage.sh                 # the deploy
tools/deploy-manage.sh --rollback      # helper restore of the previous binary, units and vhost, then verify
```

What a deploy does, in order: build `umbree-release-manage` for linux/amd64 with
the pinned Go and print its sha256; lint the units and vhost on the workstation
(an early warning, not the control); on the host, as `<SSH_USER>`, check
`sudo -n -l` for every helper verb the run and its restore use, refusing before
anything is copied with the verb named; `sudo -n <HELPER> preflight`; copy the
binary — the only file sent — to `STAGE_DIR/umbree-release-manage.new`; then
`sudo -n <HELPER> install-binary`, `render`, `activate`; verify `is-active`,
`/healthz` and `/manage/login` 200 as `<SSH_USER>`. A failed `render`,
`activate` or verify runs `sudo -n <HELPER> restore`; a refused `install-binary`
has changed nothing.

### The host helper

`<HELPER>` is `/usr/local/umbree-release/bin/umbree-deploy-helper`, a root-owned
Go program under a root-owned tree:

```
/usr/local/umbree-release/
├── bin/umbree-deploy-helper        the helper, root 0755
├── bin/umbree-release-manage       the service binary, written only by install-binary
├── etc/manage/helper.conf          root 0644
├── etc/manage/templates/           root 0644: the three units and the vhost, from the bundle
└── var/manage-deploy/              root 0700: the .prev copies and .absent markers
```

It takes no argument but its verb, honours no environment variable and reads
only `helper.conf`. It refuses to run unless it is root, and unless the config,
the templates and every directory up to `/usr/local/umbree-release` are
root-owned and not group- or other-writable. No verb writes the helper,
`etc/manage/` or anything but its fixed destinations.

| Verb | Does |
|---|---|
| `preflight` | reads only: env file listen and no trusted proxy, `STAGE_DIR` ownership and mode, static dir writable by `SERVICE_USER`, the port free or held by the service's `MainPID`; warns on systemd older than 232 |
| `install-binary` | opens `STAGE_DIR` and then `umbree-release-manage.new` in it with `O_NOFOLLOW`; refuses unless the directory is the deploy user's, mode 0750 or narrower, and the file is a regular file of the deploy user's with one link; snapshots the installed binary, units and vhost; copies through the held descriptor to a root temp, fsyncs, re-hashes, renames |
| `render` | the units and vhost from its own templates and `helper.conf`; refuses its own output unless `User=`/`Group=` is `SERVICE_USER`, no `Exec*` has a `+`/`!`/`!!` prefix, there is no `PermissionsStartOnly`, and every directive is one the committed templates use; then `nginx -t` and `systemd-analyze verify`, restoring its files on failure |
| `activate` | `daemon-reload`, `enable` the service (never the retain timer), `restart`, nginx reload |
| `restore` | the snapshot back; on a first deploy the service is stopped, then disabled, then its files removed |
| `status` | the installed binary's sha256, the service state, what the snapshot holds |

**Install the helper, once per helper or template change**, from a reviewed
`main` commit: `tools/deploy-manage.sh --build-helper` writes
`dist/umbree-deploy-helper/` (the helper, `templates/`, `SHA256SUMS`) and prints
the sums. Copy it to the host, check it there, and install:

```sh
# OPERATOR, on <RELEASE_HOST>, in the copied bundle:
sha256sum -c SHA256SUMS
sudo install -d -o root -g root -m 0755 /usr/local/umbree-release/bin /usr/local/umbree-release/etc/manage/templates
sudo install -d -o root -g root -m 0700 /usr/local/umbree-release/var/manage-deploy
sudo install -o root -g root -m 0755 umbree-deploy-helper /usr/local/umbree-release/bin/
sudo install -o root -g root -m 0644 templates/* /usr/local/umbree-release/etc/manage/templates/
```

**Write `helper.conf` once**, root 0644, `KEY=value`:

```
SERVICE_USER=umbree-release
DEPLOY_USER=<SSH_USER>
STAGE_DIR=/home/<SSH_USER>/umbree-release-manage-deploy
MANAGE_DATA_DIR=<MANAGE_DATA_DIR>
MANAGE_ENV_FILE=/etc/umbree-release-manage/manage.env
STATIC_DIR=<STATIC_DIR>
MANAGE_LISTEN=127.0.0.1:<PORT>
```

### The deploy user, once per host

```sh
# OPERATOR, on <RELEASE_HOST>:
sudo useradd --create-home --shell /bin/sh <SSH_USER>
sudo install -d -m 0700 -o <SSH_USER> /home/<SSH_USER>/.ssh
# add the operator's deploy public key to /home/<SSH_USER>/.ssh/authorized_keys
sudo visudo -f /etc/sudoers.d/umbree-release-deploy
```

The drop-in grants the helper's verbs and nothing else — one exact line each,
no wildcard, no raw `install`, `rm`, `ss`, `cat`, `systemctl` or `nginx`:

```
<SSH_USER> ALL=(root) NOPASSWD: /usr/local/umbree-release/bin/umbree-deploy-helper preflight
<SSH_USER> ALL=(root) NOPASSWD: /usr/local/umbree-release/bin/umbree-deploy-helper install-binary
<SSH_USER> ALL=(root) NOPASSWD: /usr/local/umbree-release/bin/umbree-deploy-helper render
<SSH_USER> ALL=(root) NOPASSWD: /usr/local/umbree-release/bin/umbree-deploy-helper activate
<SSH_USER> ALL=(root) NOPASSWD: /usr/local/umbree-release/bin/umbree-deploy-helper restore
<SSH_USER> ALL=(root) NOPASSWD: /usr/local/umbree-release/bin/umbree-deploy-helper status
```

No helper verb takes a port; `<PORT>` appears only in `helper.conf`. `visudo`
refuses a drop-in that does not parse, and `sudo -n -l` as `<SSH_USER>` lists
what it may run.

**What the deploy key can still do.** `<SSH_USER>` chooses the service binary,
so whoever holds its key can **run any code as `umbree-release`** and restart
the service and reload nginx. `umbree-release` holds the catalog, the
second-factor key, the R2 credentials and write access to the static directory —
it can publish a release. It cannot become root: every unit and the vhost come
from root-owned templates and `helper.conf`, and the helper reads nothing else
the deploy user writes. Guard that key like a release key.

After the first deploy: `admin add` (below), the seal of `UMBREE_R2_GATED_BUCKET`
and `UMBREE_MANAGE_URL`, `backfill`, `retain --dry-run`, then the timer —
`tools/RUNBOOK.md`, "First deploy of the manage service".

The service reads its settings from flags or from these variables, all sealed
and never committed: `UMBREE_MANAGE_DATA_DIR`, `UMBREE_MANAGE_SECRET_KEY`,
`UMBREE_R2_ACCOUNT`, `UMBREE_R2_CREDS`, `UMBREE_R2_GATED_BUCKET`,
`UMBREE_R2_BUCKET`, `UMBREE_PUBLIC_BASE_URL`, `UMBREE_MANAGE_TRUSTED_PROXY`,
`UMBREE_MANAGE_STATIC_DEST`, `UMBREE_MANAGE_STATIC_SSH_KEY`. `--public-base-url` must be an
`https` URL with a host name. Every outbound request goes through the guard:
https only, no userinfo, no IP literal or private address, and no redirect.

Set `--trusted-proxy` to the TLS front's address as the service sees it (one IP
literal). Only then is the client taken from the rightmost `X-Forwarded-For`
entry. Unset, no forwarded header is read, every client behind the front shares
one sign-in budget, and `serve` logs one warning at start saying so. Failed
sign-ins are limited to 5 per client and name and 50 per name, per 15 minutes.

### Static destination

`--static-dest` is where promote and yank republish the static surface. Two
shapes, both sealed with the deployment, never committed:

- **An absolute directory**, when the service runs on the static host. Add it to
  the unit's `ReadWritePaths=` beside `<MANAGE_DATA_DIR>`; `ProtectSystem=strict`
  makes everything else read-only. The directory is the operator's to create;
  the service never creates it.
- **`[<user>@]<RELEASE_HOST>:<STATIC_DIR>`**, over sftp. One batch puts every
  file under a dot-temp name beside its target and only then renames each over
  the served name, so a killed or partial upload never leaves a truncated
  `install.sh` being served (the release host's sftp server must offer
  OpenSSH's `posix-rename`, as OpenSSH does). It needs
  `--static-ssh-key`, a key made for this alone on `<MANAGE_HOST>`, readable by
  `<SERVICE_USER>` and outside `/home` (the unit sets `ProtectHome=yes`). On
  `<RELEASE_HOST>` restrict it to writing under `<STATIC_DIR>`, for example a
  dedicated user with `ForceCommand internal-sftp` and a `ChrootDirectory`; it is
  never the operator's own key. Under a chroot, the directory in
  `--static-dest` is the static dir as the jailed sftp session sees it (its path
  inside the `ChrootDirectory`), not `<STATIC_DIR>` as nginx sees it; naming the
  outside path makes every republish fail at `put`. The host key
  is learned on first contact into `<MANAGE_DATA_DIR>/static_known_hosts`
  (`accept-new`) and checked strictly after that. `serve` refuses a remote dest
  without the key.

With neither set, `serve` warns once at start and every promote and yank
reports its static step as failed. A failed republish never undoes the promote
or yank. Repeat it from the console's overview, or by hand:

```sh
# OPERATOR, on <RELEASE_HOST>, as <SERVICE_USER>, with the service's environment loaded:
umbree-release-manage publish-static <component> --data-dir <MANAGE_DATA_DIR>
```

### Admins

```sh
# OPERATOR, on <MANAGE_HOST>, as <SERVICE_USER>:
umbree-release-manage admin add <name> --data-dir <MANAGE_DATA_DIR> --secret-key <SECRET_KEY_FILE>
umbree-release-manage admin list --data-dir <MANAGE_DATA_DIR>
umbree-release-manage admin reset-totp <name> --data-dir <MANAGE_DATA_DIR> --secret-key <SECRET_KEY_FILE>
umbree-release-manage admin remove <name> --data-dir <MANAGE_DATA_DIR>
umbree-release-manage admin unlock <name> --data-dir <MANAGE_DATA_DIR> --reason <why>
```

`admin add` prompts for the password on the terminal, or reads it from stdin
with `--password-stdin`. It prints the TOTP enrolment once, so scan it then.
`reset-totp` and `remove` end the admin's sessions. `unlock` clears a name's
failed sign-ins from every source, and the audit log records who did it and why.

### Retention

The service runs retention itself: the gated pass after each registration and
both passes after each promote. The counts and rules are in the top-level
`README.md` → "Retention". The nightly net is a separate unit pair,
`ops/systemd/umbree-release-manage-retain.{service,timer}`. It reads the same
environment file and runs `umbree-release-manage retain`.

**Enabling the timer can delete real bytes.** Do it only after `backfill` and
after checking both buckets' listings against
`umbree-release-manage retain --dry-run`. Then confirm the two previews in the
console.

`retain` is a separate process from `serve`, and it still cannot overlap a
promote or a yank. Both take the same per-component, per-channel lock: a
`lock.<component>.<channel>` file in `<MANAGE_DATA_DIR>`, created `0600` and
never followed through a symlink.

**Run every admin verb as the service user.** That means `retain`, `backfill`,
`publish-static`, `admin mark-yanked`, `admin pin` and `admin unpin`, and `sudo -u <SERVICE_USER>`
does it. A verb run as root or as another account refuses before it creates
anything, naming both uids, because a lock file it created would lock `serve`
out of that channel. A lock file owned by anyone but the data directory's owner
is refused too: remove it, then run the verb again as the service user. `backfill`,
`admin mark-yanked` and `admin pin|unpin` take it too. A command that waits
longer than 5 s for the lock exits non-zero, says the channel is busy, and
changes nothing on that channel.

Pins keep a release's public bytes outside the window, and both verbs are
audited:

```sh
# OPERATOR, on <MANAGE_HOST>, as <SERVICE_USER>:
umbree-release-manage admin pin <component> <stamp> --data-dir <MANAGE_DATA_DIR>
umbree-release-manage admin unpin <component> <stamp> --data-dir <MANAGE_DATA_DIR>
```

### Smoke test

```sh
curl -fsS https://release.umbree.org/healthz                  # ok
curl -fsSI https://release.umbree.org/manage/login            # 200, text/html
```

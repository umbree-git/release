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
Which host runs it is the operator's choice, and it is not named here. Its public
address reaches the cut only as the sealed `UMBREE_MANAGE_URL`. The unit template
is `ops/systemd/umbree-release-manage.service`.

The service binds loopback (`127.0.0.1:8787` by default). A TLS front on
`<MANAGE_HOST>` forwards to it. The session cookies are `Secure` and scoped to
`/manage`, so the console works only over HTTPS. The only unauthenticated routes
are `/healthz` and the intake under `/api/v1/releases/`. There are no public
pages and no badge: the public manifest is the only machine pointer.

### Provision, once per deployment

```sh
# OPERATOR, on <MANAGE_HOST>:
sudo install -d -o <SERVICE_USER> -m 0700 <MANAGE_DATA_DIR>
# The TOTP sealing key: 32 random bytes, mode 0600, an absolute path.
# The service refuses to start without it and never creates one.
sudo -u <SERVICE_USER> sh -c 'umask 077; head -c 32 /dev/urandom > <SECRET_KEY_FILE>'
```

Keep `<SECRET_KEY_FILE>` with the deployment's sealed items. A different key
cannot open the TOTP secrets in the catalog, so losing it means re-enrolling
every admin with `admin reset-totp`.

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
  dedicated user with `ForceCommand internal-sftp` and a `ChrootDirectory`; it is never the operator's own key. The host key
  is learned on first contact into `<MANAGE_DATA_DIR>/static_known_hosts`
  (`accept-new`) and checked strictly after that. `serve` refuses a remote dest
  without the key.

With neither set, `serve` warns once at start and every promote and yank
reports its static step as failed. A failed republish never undoes the promote
or yank. Repeat it from the console's overview, or by hand:

```sh
# OPERATOR, on <MANAGE_HOST>, as <SERVICE_USER>, with the service's environment loaded:
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
curl -fsS https://<MANAGE_HOST>/healthz                       # ok
curl -fsSI https://<MANAGE_HOST>/manage/login                 # 200, text/html
```

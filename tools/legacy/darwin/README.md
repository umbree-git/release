# `tools/legacy/darwin` — the darwin-amd64-legacy `crypto/x509` overlay

## What this is

Go ≥ 1.25 verifies TLS chains on darwin through `SecTrustCopyCertificateChain`,
a macOS 12 API that `crypto/x509` imports non-lazily. Every darwin binary built
with the pinned go1.26.10 toolchain therefore dies in dyld, before `main`, on
macOS 10.15 and 11:

```
dyld: Symbol not found: _SecTrustCopyCertificateChain
Abort trap: 6
```

Both Umbree binaries carry the import — `umbreed` and `umbree` alike (project
`2026-10-19-umbree-darwin-legacy`, measured with `go tool nm` on stock
`darwin/amd64` builds) — so an Intel Mac below macOS 12 could run neither.

This directory is a `go build -overlay` replacement for the three `crypto/x509`
files that reference the symbol. It swaps the Go 1.24 chain walk
(`SecTrustGetCertificateCount` + `SecTrustGetCertificateAtIndex`, present since
macOS 10.7) back in and changes nothing else: same toolchain, same module pins,
same ldflags, same signing and notarization. Only the fifth target,
`darwin-amd64-legacy`, built by `rkit build`, uses it.

## Where it comes from — a pinned copy of Burrowee's, not a fork

The three sources, the Go pin, the drift guard, the overlay writer and the
symbol rule are **byte-for-byte Burrowee's** (`burrowee-git/release`,
`tools/legacy/darwin/`; operator ruling Q3 of the project). `UPSTREAM` records
the commit they were copied from and the sha256 of each;
`tools/legacy-darwin.test.sh` fails the moment one of them differs from that
record. An edit made here instead of upstream shows up as a red suite, not as a
quiet second version of a stdlib patch. This is the same copy, from the same
commit, that Clawee's release repo carries.

Burrowee is named here because Umbree already depends on it at runtime
(`umbreed` needs `burrowee-gateway`, `umbree` pulls `burrowee-cli`); the copy
removes any cut-time dependency on a Burrowee checkout. A cut reads only these
files, so the CI machine, which has no sibling checkouts, can test it.

## Files

| File | Origin | Role |
|---|---|---|
| `_src/root_darwin.go` | upstream | `crypto/x509/root_darwin.go` with the chain loop swapped for the index walk |
| `_src/security.go` | upstream | `crypto/x509/internal/macos/security.go`: `SecTrustCopyCertificateChain` out, `SecTrustGetCertificateCount` + `SecTrustGetCertificateAtIndex` in |
| `_src/security.s` | upstream | the matching trampolines: two added, one removed |
| `GO_VERSION` | upstream | the exact `go version` the three files were derived from |
| `overlay.test.sh` | upstream | the drift guard: pinned Go, and every diff hunk against the live GOROOT names an owned symbol |
| `overlay-json.sh <out>` | upstream | writes the `-overlay` JSON (absolute GOROOT paths → `_src/`) |
| `symbols.sh` | upstream | `assert_legacy_symbols`, the post-build rule, read through `go tool nm` so it runs on the Linux CI machine too |
| `fake_goroot.sh` | **Umbree** | `legacy_fake_goroot <root>`: a GOROOT holding only the three originals the overlay replaces, for tests that fake `go` |
| `UPSTREAM` | **Umbree** | provenance and the sha256 of every upstream file |

The Go and assembly sources live in `_src/` because the Go tool skips any
directory whose name starts with `_`: beside each other they declare two
packages (`x509`, `macos`) in one directory, and `go build ./...`, `go vet
./...` and `go test ./...` would refuse the whole module. Do not move them out.
`.gitattributes` marks `_src/` vendored: they are stdlib copies and keep their
comments.

## How the build uses it

`rkit build` (`cmd/rkit/legacy.go`) builds the four stock targets through
release-kit exactly as before — they never see `-overlay` — and then the fifth:

1. before the version bump, the CVE gate or the stamp, refuses unless
   `go version` (the `go` on `PATH`, the one release-kit compiles with) prints
   exactly `GO_VERSION`;
2. refuses a legacy target other than darwin/amd64;
3. runs `overlay.test.sh` with that same `go` and refuses the build when it
   fails, so the guard, the overlay paths and the compile are one toolchain;
4. writes the `-overlay` JSON with `overlay-json.sh` and runs one `go build
   -trimpath -overlay … -ldflags <the stock ldflags>` per binary, with
   `CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 GOWORK=off`, into
   `dist/<stamp>/darwin-amd64-legacy/`;
5. runs `assert_legacy_symbols` on every binary, deleting and refusing one that
   fails, **before** it is signed;
6. on a darwin host, signs it with the same signer as the stock darwin
   binaries, after which it goes through `verify-no-env.sh`, is zipped as
   `<comp>-darwin-amd64-legacy.zip`, notarized and listed in `SHA256SUMS.txt`
   like every other zip.

`cmd/rkit/legacy_test.go` goes red if any check in steps 1, 2, 3 or 5 is
deleted, if the step 4 environment changes, or if step 6 stops signing on a
darwin host. `tools/legacy-darwin.test.sh` runs the drift guard, its two
negative cases and the symbol rule against real builds, under the pinned
toolchain.

## Not covered by the CVE gate

The CVE gate scans the module and the toolchain's stdlib as shipped; it never
sees `_src/`. A future stdlib CVE in one of these three files would be
reintroduced by the overlay without a report. The exposure is small — about
forty lines of Go 1.24 code the drift guard re-checks on every legacy build —
but it is real.

## Re-syncing from Burrowee (a Go bump, or an upstream fix)

The overlay is re-derived **upstream**, in `burrowee-git/release`, by the
procedure in its own README ("Go-bump procedure"). Then here:

1. Copy the seven upstream files over the ones in this directory.
2. Rewrite `UPSTREAM`: the new upstream commit, and a fresh `shasum -a 256`
   for each file.
3. List `tools/legacy-darwin.test.sh` and the `cmd/rkit` legacy tests in the
   feature's `tests.tsv`. Nothing runs on the workstation.
4. On the CI machine, under the new toolchain, `tools/legacy-darwin.test.sh`
   runs the drift guard (`overlay.test.sh`, every line `ok:`) with zero `SKIP`
   markers.
5. Commit all of it together — the overlay, its pin and its record never drift
   apart — and move the toolchain pin in the local release environment with it.

## Support statement

`darwin-amd64-legacy` is **best effort for Intel Macs on macOS 10.15–11**. It
exists while Apple still ships `SecTrustGetCertificateAtIndex` and the overlay
still applies to the pinned Go. When either stops, the target is dropped from
`rkit build` and a legacy host is told to move to macOS 12 — it is not fought
for. Apple Silicon below macOS 12 is not shipped at all; the bootstrap's
`platform-detect` v2 refuses it with that message.

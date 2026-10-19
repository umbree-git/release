package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/burrowee-git/release-kit/build"
	"github.com/umbree-git/release/internal/relconfig"
)

const fakeGoScript = `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE_GO_LOG"
case "$1" in
version) printf 'go version %s linux/arm64\n' "$FAKE_GO_VERSION" ;;
env) printf '%s\n' "$FAKE_GOROOT" ;;
build)
    out=""
    while [ $# -gt 0 ]; do [ "$1" = -o ] && out="$2"; shift; done
    case "$out" in
    */darwin-amd64-legacy/*) printf '%s\n' "$FAKE_LEGACY_NM" > "$out" ;;
    *) printf '%s\n' "$FAKE_STOCK_NM" > "$out" ;;
    esac ;;
tool) cat "$3" ;;
*) echo "fake go: unexpected: $*" >&2; exit 1 ;;
esac
`

const fakeSignToolScript = `#!/usr/bin/env bash
printf '%s\n' "$*" >> "$FAKE_SIGN_LOG"
`

type fakeGo struct {
	log, goroot string
}

func realRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func pinnedGoVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(realRepoRoot(t), "tools", "legacy", "darwin", "GO_VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func installFakeGo(t *testing.T) fakeGo {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	mustWriteFile(t, filepath.Join(bin, "go"), fakeGoScript)
	mustWriteFile(t, filepath.Join(bin, "modernech-sign"), fakeSignToolScript)
	for _, name := range []string{"go", "modernech-sign"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := fakeGo{log: filepath.Join(dir, "go.log"), goroot: filepath.Join(dir, "goroot")}
	fakeRoot := exec.Command("bash", "-c", `source "$1" && legacy_fake_goroot "$2"`, "_",
		filepath.Join(realRepoRoot(t), "tools", "legacy", "darwin", "fake_goroot.sh"), f.goroot)
	if out, err := fakeRoot.CombinedOutput(); err != nil {
		t.Fatalf("legacy_fake_goroot: %v\n%s", err, out)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_GO_LOG", f.log)
	t.Setenv("FAKE_SIGN_LOG", filepath.Join(dir, "sign.log"))
	t.Setenv("FAKE_GOROOT", f.goroot)
	t.Setenv("FAKE_GO_VERSION", pinnedGoVersion(t))
	t.Setenv("FAKE_STOCK_NM", "U _SecTrustCopyCertificateChain")
	t.Setenv("FAKE_LEGACY_NM", "U _SecTrustGetCertificateAtIndex")
	return f
}

func (f fakeGo) buildLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, "build ") {
			out = append(out, l)
		}
	}
	return out
}

func signLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(os.Getenv("FAKE_SIGN_LOG"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

type recordingSigner struct{ signed []string }

func (s *recordingSigner) Sign(_ context.Context, path string) error {
	s.signed = append(s.signed, path)
	return nil
}

func newLegacyBuild(t *testing.T, signer *recordingSigner) legacyBuild {
	t.Helper()
	bins, err := relconfig.Bins("umbree", "v0.1.0.2026.10.19.deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	return legacyBuild{
		RepoDir: realRepoRoot(t), SrcDir: t.TempDir(), OutDir: t.TempDir(),
		Target: relconfig.LegacyTarget(), Bins: bins, Signer: signer,
	}
}

func TestLegacyTargetIsDarwinAmd64Only(t *testing.T) {
	f := installFakeGo(t)
	for _, tgt := range []build.Target{{OS: "linux", Arch: "amd64"}, {OS: "darwin", Arch: "arm64"}} {
		signer := &recordingSigner{}
		lb := newLegacyBuild(t, signer)
		lb.Target = tgt
		_, err := compileLegacy(context.Background(), lb)
		if err == nil || !strings.Contains(err.Error(), "darwin/amd64 only") {
			t.Fatalf("target %v: err = %v, want the darwin/amd64-only refusal", tgt, err)
		}
		if len(signer.signed) != 0 {
			t.Fatalf("target %v: signed %v after the refusal", tgt, signer.signed)
		}
	}
	if lines := f.buildLines(t); len(lines) != 0 {
		t.Fatalf("a refused legacy target still compiled: %v", lines)
	}
}

func TestLegacyRefusesToolchainOffPin(t *testing.T) {
	f := installFakeGo(t)
	t.Setenv("FAKE_GO_VERSION", "go1.0.0")
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	out := t.TempDir()
	_, err := orchestrate(context.Background(), Options{
		Component: "umbree", OutDir: out, RepoDir: repo, SkipGate: true,
	})
	if err == nil || !strings.Contains(err.Error(), "pinned to "+pinnedGoVersion(t)) {
		t.Fatalf("orchestrate: err = %v, want the toolchain pin refusal", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("orchestrate wrote %v before refusing the toolchain", entries)
	}
	err = buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: repo,
		Bump: "patch", NoVulncheck: true, SignKey: filepath.Join(repo, "go.mod")})
	if err == nil || !strings.Contains(err.Error(), "pinned to "+pinnedGoVersion(t)) {
		t.Fatalf("buildRun: err = %v, want the toolchain pin refusal", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "dist")); !os.IsNotExist(statErr) {
		t.Fatal("buildRun created dist/ before refusing the toolchain")
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "versions", "umbree")); string(got) != "0.1.0\n" {
		t.Fatalf("versions/umbree = %q: the bump ran before the toolchain check", got)
	}
	if lines := f.buildLines(t); len(lines) != 0 {
		t.Fatalf("compiled with a toolchain off the pin: %v", lines)
	}
}

func TestLegacyRefusesFailedDriftGuard(t *testing.T) {
	f := installFakeGo(t)
	original := filepath.Join(f.goroot, "src", "crypto", "x509", "internal", "macos", "security.go")
	b, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, original, string(b)+"\nvar unownedHunk = 1\n")
	signer := &recordingSigner{}
	_, err = compileLegacy(context.Background(), newLegacyBuild(t, signer))
	if err == nil || !strings.Contains(err.Error(), "drift guard") {
		t.Fatalf("err = %v, want the drift guard refusal", err)
	}
	if !strings.Contains(err.Error(), "only owned hunks differ: security.go") {
		t.Fatalf("err = %v, want the guard's own output naming security.go", err)
	}
	if lines := f.buildLines(t); len(lines) != 0 {
		t.Fatalf("compiled after a failed drift guard: %v", lines)
	}
	if len(signer.signed) != 0 {
		t.Fatalf("signed %v after a failed drift guard", signer.signed)
	}
}

func TestLegacyRefusesAndDeletesBinaryWithMacOS12Symbol(t *testing.T) {
	f := installFakeGo(t)
	t.Setenv("FAKE_LEGACY_NM", "U _SecTrustCopyCertificateChain")
	signer := &recordingSigner{}
	lb := newLegacyBuild(t, signer)
	_, err := compileLegacy(context.Background(), lb)
	if err == nil || !strings.Contains(err.Error(), "_SecTrustCopyCertificateChain") {
		t.Fatalf("err = %v, want the macOS 12 symbol refusal", err)
	}
	if len(f.buildLines(t)) != 1 {
		t.Fatalf("want the one legacy build before the symbol check, got %v", f.buildLines(t))
	}
	bin := filepath.Join(lb.OutDir, "darwin-amd64-legacy", "umbree")
	if _, statErr := os.Stat(bin); !os.IsNotExist(statErr) {
		t.Fatalf("the refused binary %s was left behind (stat err %v)", bin, statErr)
	}
	if len(signer.signed) != 0 {
		t.Fatalf("signed %v although the symbol check refused it", signer.signed)
	}
}

func TestStockTargetsNeverSeeOverlay(t *testing.T) {
	f := installFakeGo(t)
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	out := t.TempDir()
	res, err := orchestrate(context.Background(), Options{
		Component: "umbree", OutDir: out, RepoDir: repo, SkipGate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := f.buildLines(t)
	if len(lines) != len(relconfig.Targets())+1 {
		t.Fatalf("want %d compiles, got %v", len(relconfig.Targets())+1, lines)
	}
	ldflags := "-ldflags -X main.version=" + res.Stamp + " "
	var legacy []string
	for _, l := range lines {
		if !strings.Contains(l, ldflags) {
			t.Errorf("compile %q lacks the stock ldflags %q", l, ldflags)
		}
		if strings.Contains(l, "/darwin-amd64-legacy/") {
			legacy = append(legacy, l)
			continue
		}
		if strings.Contains(l, "-overlay") {
			t.Errorf("a stock target saw the overlay: %q", l)
		}
	}
	if len(legacy) != 1 || !strings.Contains(legacy[0], "-overlay ") || !strings.Contains(legacy[0], "-trimpath") {
		t.Fatalf("legacy compile = %v, want one -trimpath -overlay build", legacy)
	}
}

func TestOrchestrateWritesFiveZips(t *testing.T) {
	installFakeGo(t)
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	res, err := orchestrate(context.Background(), Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo, SkipGate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, z := range res.Zips {
		got = append(got, filepath.Base(z))
	}
	slices.Sort(got)
	want := []string{
		"umbree-darwin-amd64-legacy.zip", "umbree-darwin-amd64.zip", "umbree-darwin-arm64.zip",
		"umbree-linux-amd64.zip", "umbree-linux-arm64.zip",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("zips = %v, want %v", got, want)
	}
	legacyZip := filepath.Join(filepath.Dir(res.Zips[0]), "umbree-darwin-amd64-legacy.zip")
	entries := zipNames(t, legacyZip)
	slices.Sort(entries)
	if !slices.Equal(entries, []string{"install.sh", "umbree"}) {
		t.Fatalf("%s entries = %v, want install.sh and umbree", legacyZip, entries)
	}
}

func TestLegacyArtifactIsSignedVerifiedAndNotarized(t *testing.T) {
	installFakeGo(t)
	signer := &recordingSigner{}
	lb := newLegacyBuild(t, signer)
	arts, err := compileLegacy(context.Background(), lb)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(lb.OutDir, "darwin-amd64-legacy", "umbree")
	want := []build.Artifact{{Bin: "umbree", OS: "darwin", Arch: relconfig.LegacyArch, Path: bin, Signed: true}}
	if !slices.Equal(arts, want) {
		t.Fatalf("artifacts = %+v, want %+v", arts, want)
	}
	if !slices.Equal(signer.signed, []string{bin}) {
		t.Fatalf("signed %v, want exactly %s", signer.signed, bin)
	}

	repo := t.TempDir()
	writeFixtureModule(t, repo)
	out := t.TempDir()
	res, err := orchestrate(context.Background(), Options{
		Component: "umbree", OutDir: out, RepoDir: repo, SkipGate: true, Apple: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	notarized := "notarize " + filepath.Join(out, res.Stamp, "umbree-darwin-amd64-legacy.zip")
	if !strings.Contains(signLog(t), notarized+"\n") {
		t.Fatalf("sign tool log = %q, want %q", signLog(t), notarized)
	}

	t.Setenv("FAKE_LEGACY_NM", "U _SecTrustGetCertificateAtIndex UMBREE_DATA_DIR")
	_, err = orchestrate(context.Background(), Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo, SkipGate: true,
	})
	if err == nil || !strings.Contains(err.Error(), "verify-no-env") {
		t.Fatalf("err = %v, want verify-no-env to refuse the legacy binary", err)
	}
}

func TestSumsListLegacyZip(t *testing.T) {
	installFakeGo(t)
	if _, err := exec.LookPath("minisign"); err != nil {
		t.Fatal("minisign is not on PATH: the SHA256SUMS signature cannot be checked")
	}
	keys := t.TempDir()
	pub, sec := filepath.Join(keys, "key.pub"), filepath.Join(keys, "key.sec")
	if out, err := exec.Command("minisign", "-G", "-W", "-p", pub, "-s", sec).CombinedOutput(); err != nil {
		t.Fatalf("minisign keygen: %v\n%s", err, out)
	}
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	res, err := orchestrate(context.Background(), Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo, SkipGate: true, MinisignKey: sec,
	})
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(res.Sums)
	if err != nil {
		t.Fatal(err)
	}
	legacyZip := filepath.Join(filepath.Dir(res.Sums), "umbree-darwin-amd64-legacy.zip")
	zipBytes, err := os.ReadFile(legacyZip)
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("%x  umbree-darwin-amd64-legacy.zip", sha256.Sum256(zipBytes))
	if !slices.Contains(strings.Split(strings.TrimSpace(string(sums)), "\n"), line) {
		t.Fatalf("SHA256SUMS.txt = %q, want the line %q", sums, line)
	}
	verify := exec.Command("minisign", "-V", "-p", pub, "-m", res.Sums, "-x", res.Minisig)
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("minisign -V over the sums that list the legacy zip: %v\n%s", err, out)
	}
}

func copyLegacyOverlay(t *testing.T, repo string) {
	t.Helper()
	src := filepath.Join(realRepoRoot(t), "tools", "legacy", "darwin")
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		dst := filepath.Join(repo, "tools", "legacy", "darwin", rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o755)
	})
	if err != nil {
		t.Fatalf("copy the legacy overlay into %s: %v", repo, err)
	}
}

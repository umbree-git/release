package main

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/burrowee-git/release-kit/sign"
	"github.com/umbree-git/release/internal/relconfig"
)

func TestOrchestrateBuildsMatrixIntoScratch(t *testing.T) {
	// Minimal module fixture: one main package printing a version var.
	repo := t.TempDir()
	writeFixtureModule(t, repo) // helper below: go.mod + cmd/umbree/main.go + versions/umbree
	out := t.TempDir()
	ctx := context.Background()
	res, err := orchestrate(ctx, Options{
		Component: "umbree", OutDir: out, RepoDir: repo,
		MinisignKey: testMinisignKey(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stamp == "" {
		t.Fatal("empty stamp")
	}
	// binaries for every target must exist.
	for _, tgt := range relconfig.Targets() {
		for _, b := range []string{"umbree"} {
			p := filepath.Join(out, res.Stamp, tgt.OS+"-"+tgt.Arch, b)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("missing %s: %v", p, err)
			}
		}
	}
	// one assembled zip per target, containing exactly the umbree bin and
	// install.sh — nothing else (umbree has no dispatcher, no updater).
	if len(res.Zips) != len(relconfig.Targets()) {
		t.Fatalf("got %d zips, want %d: %v", len(res.Zips), len(relconfig.Targets()), res.Zips)
	}
	wantEntries := []string{"install.sh", "umbree"}
	sort.Strings(wantEntries)
	wantInstallSh, err := os.ReadFile(filepath.Join(repo, "inner", "umbree", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, zp := range res.Zips {
		r, err := zip.OpenReader(zp)
		if err != nil {
			t.Fatalf("open %s: %v", zp, err)
		}
		var got []string
		for _, f := range r.File {
			got = append(got, f.Name)
			if f.Name == "install.sh" {
				rc, err := f.Open()
				if err != nil {
					t.Fatalf("open install.sh in %s: %v", zp, err)
				}
				data, err := io.ReadAll(rc)
				rc.Close()
				if err != nil {
					t.Fatalf("read install.sh in %s: %v", zp, err)
				}
				if string(data) != string(wantInstallSh) {
					t.Errorf("%s install.sh not byte-identical to inner/umbree/install.sh", zp)
				}
			}
		}
		r.Close()
		sort.Strings(got)
		if !reflect.DeepEqual(got, wantEntries) {
			t.Errorf("%s entries = %v, want %v", zp, got, wantEntries)
		}
	}
	if _, err := os.Stat(res.Sums); err != nil {
		t.Errorf("missing sums file %s: %v", res.Sums, err)
	}
	if res.Minisig != "" {
		if _, err := os.Stat(res.Minisig); err != nil {
			t.Errorf("Result.Minisig=%s but file missing: %v", res.Minisig, err)
		}
	}
}

// writeFixtureModule creates a self-contained module (no external deps) with a
// trivial main package (a stampable `var version string`), a versions/umbree
// semver file, a verbatim inner/umbree/install.sh, and the real
// tools/verify-no-env.sh + tools/version.sh (both location-relative, so a copy
// works unmodified inside the fixture repo), then commits it so
// relconfig.Stamp's `git rev-parse` has a HEAD to read.
func writeFixtureModule(t *testing.T, repo string) {
	t.Helper()
	write := func(rel, content string) {
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module fixture\n\ngo 1.25.0\n")
	mainSrc := "package main\n\nimport \"fmt\"\n\nvar version string\n\nfunc main() { fmt.Println(version) }\n"
	write("cmd/umbree/main.go", mainSrc)
	write("versions/umbree", "0.1.0\n")
	write("inner/umbree/install.sh", "#!/bin/sh\necho fixture-install\n")

	// tools/verify-no-env.sh and tools/version.sh both resolve REPO_ROOT from
	// their own script location (dirname $0), so a verbatim copy works inside
	// the fixture repo unmodified.
	copyReal := func(rel string) {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	copyReal("tools/verify-no-env.sh")
	copyReal("tools/version.sh")

	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repo}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "fixture")
}

// testMinisignKey generates a password-less minisign secret key in a temp dir
// and returns its path. If minisign isn't installed, it returns "" — the
// os.Stat guard in orchestrate() then skips signing rather than failing the
// whole test on a missing binary.
func testMinisignKey(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("minisign"); err != nil {
		t.Log("minisign not installed — signing portion will be skipped")
		return ""
	}
	dir := t.TempDir()
	pub := filepath.Join(dir, "key.pub")
	sec := filepath.Join(dir, "key.sec")
	if out, err := exec.Command("minisign", "-G", "-W", "-p", pub, "-s", sec).CombinedOutput(); err != nil {
		t.Fatalf("minisign keygen: %v\n%s", err, out)
	}
	return sec
}

// TestOrchestrateSkipGateBypassesCVEGate proves Options.SkipGate short-circuits
// the mandatory vulncheck.Gate. To make a real gate call deterministically fail
// regardless of host state, GOPATH is repointed at an empty dir: govulncheck is
// installed under the real GOPATH/bin (and is NOT on PATH), so
// resolveGovulncheck can't find it and Gate returns "govulncheck not found".
// The fixture module is stdlib-only, so the empty GOPATH doesn't affect the
// build itself.
func TestOrchestrateSkipGateBypassesCVEGate(t *testing.T) {
	t.Setenv("GOPATH", t.TempDir())
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	ctx := context.Background()
	key := testMinisignKey(t)

	// Gate ON (SkipGate zero value): orchestrate must abort at the CVE gate.
	if _, err := orchestrate(ctx, Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo, MinisignKey: key,
	}); err == nil || !strings.Contains(err.Error(), "cve gate") {
		t.Fatalf("gate ON: want a cve gate error, got %v", err)
	}

	// Gate SKIPPED: the same build now succeeds past the gate.
	if _, err := orchestrate(ctx, Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo, MinisignKey: key, SkipGate: true,
	}); err != nil {
		t.Fatalf("gate SKIPPED: orchestrate should bypass the gate and succeed, got %v", err)
	}
}

// TestOrchestrateFailsClosedOnMissingExplicitMinisignKey proves that when a
// NON-EMPTY Options.MinisignKey is supplied but doesn't resolve to a real
// file, orchestrate returns an error instead of silently skipping the sign
// step (leaving Result.Minisig empty and reporting success). The
// o.MinisignKey=="" fixture fallback (testMinisignKey unavailable) must
// still tolerate a missing key — see TestOrchestrateBuildsMatrixIntoScratch.
func TestOrchestrateFailsClosedOnMissingExplicitMinisignKey(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	ctx := context.Background()
	_, err := orchestrate(ctx, Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo,
		MinisignKey: filepath.Join(t.TempDir(), "nonexistent.key"), SkipGate: true,
	})
	if err == nil {
		t.Fatal("expected error for missing explicit MinisignKey, got nil")
	}
	if !strings.Contains(err.Error(), "minisign") {
		t.Fatalf("error = %q, want it to mention the minisign key", err.Error())
	}
}

func TestBuildWritesToDistStamp(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	// build with default gate SKIPPED for the fixture host: pass --no-vulncheck.
	err := buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: repo,
		NoVulncheck: true, SignKey: testMinisignKey(t)})
	if err != nil {
		t.Fatal(err)
	}
	// artifacts land under repo/dist/<stamp>/, NOT an arbitrary --out.
	stamp := mustStamp(t, repo, "umbree")
	if _, err := os.Stat(filepath.Join(repo, "dist", stamp, "umbree-linux-amd64.zip")); err != nil {
		t.Errorf("missing zip under dist/<stamp>: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "dist", stamp, "SHA256SUMS.txt")); err != nil {
		t.Errorf("missing SHA256SUMS: %v", err)
	}
}

// TestBuildRunFailsFastOnMissingSignKeyFile proves a real cut (DryRun:false)
// with a --sign-key path that doesn't exist on disk fails IMMEDIATELY, before
// any build work runs — never silently skipping the sign step and reporting
// success.
func TestBuildRunFailsFastOnMissingSignKeyFile(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	stamp := mustStamp(t, repo, "umbree")

	err := buildRun(buildOpts{
		Component: "umbree", RepoDir: repo, SrcDir: repo,
		DryRun: false, SignKey: "/nonexistent/path/key.key", NoVulncheck: true,
	})
	if err == nil {
		t.Fatal("expected error for missing --sign-key file, got nil")
	}
	if !strings.Contains(err.Error(), "sign-key") || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error = %q, want it to mention sign-key and no such file", err.Error())
	}
	// Fail-fast means no build work happened: no dist/<stamp> zips.
	if _, statErr := os.Stat(filepath.Join(repo, "dist", stamp)); !os.IsNotExist(statErr) {
		t.Fatalf("dist/%s should not exist (fail-fast before build), stat err = %v", stamp, statErr)
	}
}

// TestBuildRunBumpDryRunReverts exercises the bump+revert path end-to-end: a
// --bump-patch --dry-run build must run the revert (registered before the
// bump block) and leave versions/umbree exactly as it was committed, with no
// staged or worktree diff left behind.
func TestBuildRunBumpDryRunReverts(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo) // versions/umbree = "0.1.0\n", committed
	if err := buildRun(buildOpts{
		Component: "umbree", RepoDir: repo, SrcDir: repo,
		Bump: "patch", DryRun: true, NoVulncheck: true, SignKey: testMinisignKey(t),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(repo, "versions", "umbree"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "0.1.0\n" {
		t.Fatalf("versions/umbree = %q, want unchanged %q (revert did not fire)", got, "0.1.0\n")
	}
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "versions/umbree").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("git status --porcelain versions/umbree = %q, want clean", out)
	}
}

// TestBuildRunAbsolutizesRelativeRepoDir is the end-to-end regression test
// for the misplacement bug: a RELATIVE --repo (the "." default a real
// invocation from the release-repo root uses) must not make build.Compile's
// `-o` resolve inside the COMPONENT SOURCE worktree (cmd.Dir for `go build`
// is SrcDir, not RepoDir). RepoDir and SrcDir are deliberately two distinct
// temp dirs, and buildRun runs from a THIRD cwd (RepoDir's parent), passing
// RepoDir as a relative path (its basename) — exactly how `rkit build
// --repo=.` behaves when invoked from the release-repo root. It asserts the
// zip lands under the absolute RepoDir's dist/<stamp>/ AND that no dist/ was
// created inside SrcDir (the bug this guards against).
func TestBuildRunAbsolutizesRelativeRepoDir(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "reporoot")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixtureModule(t, repo)

	// A component source worktree distinct from RepoDir, with its own git
	// history — relconfig.Stamp reads the HEAD sha from SrcDir.
	src := filepath.Join(parent, "srcroot")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}
	mainSrc := "package main\n\nimport \"fmt\"\n\nvar version string\n\nfunc main() { fmt.Println(version) }\n"
	mustWriteFile(t, filepath.Join(src, "go.mod"), "module fixturesrc\n\ngo 1.25.0\n")
	mustWriteFile(t, filepath.Join(src, "cmd", "umbree", "main.go"), mainSrc)
	gitSrc := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", src}, args...)...)
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitSrc("init", "-q")
	gitSrc("add", "-A")
	gitSrc("commit", "-q", "-m", "fixture src")

	// cwd is parent — NEITHER repo nor src — so "reporoot" only resolves
	// correctly if buildRun absolutizes it against this cwd.
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(origWD); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}

	if err := buildRun(buildOpts{
		Component: "umbree", RepoDir: "reporoot", SrcDir: src,
		DryRun: true, NoVulncheck: true, SignKey: testMinisignKey(t),
	}); err != nil {
		t.Fatal(err)
	}

	stamp, err := relconfig.Stamp(context.Background(), filepath.Join(repo, "versions", "umbree"), src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, "dist", stamp, "umbree-linux-amd64.zip")); err != nil {
		t.Errorf("artifacts not under the absolute repo's dist/<stamp>: %v", err)
	}
	// The component SOURCE worktree must stay untouched by the build — no
	// dist/ misplaced inside it. This is the exact failure mode the bug
	// produced: `go build -o dist/<stamp>/...` resolved relative to SrcDir
	// (cmd.Dir), writing the binary into the source tree instead of the
	// release repo.
	if _, err := os.Stat(filepath.Join(src, "dist")); !os.IsNotExist(err) {
		t.Fatalf("dist/ leaked into SrcDir %s (relative --repo misplacement bug), stat err = %v", src, err)
	}
}

func TestBuildGateOnByDefaultCanBeSkipped(t *testing.T) {
	// With NoVulncheck=false and no govulncheck resolvable, the gate must RUN
	// (and here fail) — proving default-on. Then NoVulncheck=true bypasses.
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	t.Setenv("GOPATH", t.TempDir()) // make govulncheck unresolvable → gate errors
	if err := buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: repo,
		NoVulncheck: false, SignKey: testMinisignKey(t)}); err == nil {
		t.Fatal("expected default-on gate to run and fail")
	}
	if err := buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: repo,
		NoVulncheck: true, SignKey: testMinisignKey(t)}); err != nil {
		t.Fatalf("--no-vulncheck should bypass: %v", err)
	}
}

// mustStamp computes the expected dist/<stamp> directory name the same way
// buildRun/orchestrate do, so tests can assert on artifact paths without
// duplicating the stamp scheme.
func mustStamp(t *testing.T, repo, comp string) string {
	t.Helper()
	stamp, err := relconfig.Stamp(context.Background(), filepath.Join(repo, "versions", comp), repo)
	if err != nil {
		t.Fatal(err)
	}
	return stamp
}

func TestBuildAppleSelectsDevIDSignerAndNotarizes(t *testing.T) {
	// Unit-level: assert selectSigner(apple=true) returns AppleSigner{ToolPath:"modernech-sign"}
	// and notarizerFor(apple=true) returns Notarizer{ToolPath:"modernech-sign"};
	// apple=false returns AdHocSigner and a nil/skip notarizer.
	s := selectSigner(true)
	if _, ok := s.(sign.AppleSigner); !ok {
		t.Fatalf("apple signer type = %T", s)
	}
	if got := s.(sign.AppleSigner).ToolPath; got != "modernech-sign" {
		t.Fatalf("toolpath %q", got)
	}
	if selectSigner(false) == nil {
		t.Fatal("adhoc signer nil")
	}
	if _, ok := selectSigner(false).(sign.AdHocSigner); !ok {
		t.Fatal("non-apple must be adhoc")
	}
	n, do := notarizerFor(true)
	if !do || n.ToolPath != "modernech-sign" {
		t.Fatalf("notarizer %+v do=%v", n, do)
	}
	if _, do2 := notarizerFor(false); do2 {
		t.Fatal("non-apple must not notarize")
	}
}

// TestRenderInstall covers install.sh rendering directly (no compile). Two
// modes, pinned separately, because they are NOT the same code path:
//
//   - umbree (client) is a byte-verbatim copy of the repo-committed
//     inner/umbree/install.sh.
//   - umbreed (daemon) is the daemon repo's OWN install/install.sh.in,
//     substituting __UMBREED_VERSION__ for the stamp — read from srcDir, not
//     from this repo's inner/, and inner/umbreed/install.sh does not exist
//     (deleted; see the comment in renderInstall's "umbreed" case).
func TestRenderInstall(t *testing.T) {
	t.Run("umbree verbatim copy from inner/umbree/install.sh", func(t *testing.T) {
		repoDir := t.TempDir()
		mustWriteFile(t, filepath.Join(repoDir, "inner", "umbree", "install.sh"), "#!/bin/sh\necho hi from umbree\n")
		dst := filepath.Join(t.TempDir(), "out", "install.sh")
		if err := renderInstall("umbree", "v0.1.0.x", "/unused/src", repoDir, dst); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(repoDir, "inner", "umbree", "install.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("umbree install.sh = %q, want verbatim %q", got, want)
		}
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
		}
	})

	// Proves the daemon arm substitutes the stamp for the placeholder, read
	// from srcDir/install/install.sh.in — NOT from this repo's inner/, which
	// has no umbreed subdirectory at all. Breaks if renderInstall stops
	// substituting (e.g. reverts to a verbatim copy) or reads from the wrong
	// directory (e.g. repoDir instead of srcDir).
	t.Run("umbreed substitutes the stamp from srcDir's canonical template", func(t *testing.T) {
		repoDir := t.TempDir() // deliberately has no inner/umbreed — must be unused
		srcDir := t.TempDir()
		mustWriteFile(t, filepath.Join(srcDir, "install", "install.sh.in"),
			"#!/bin/sh\necho installing umbreed __UMBREED_VERSION__\n")
		dst := filepath.Join(t.TempDir(), "out", "install.sh")
		stamp := "v0.2.0.2026.08.30.deadbeef"
		if err := renderInstall("umbreed", stamp, srcDir, repoDir, dst); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(got), stamp) {
			t.Fatalf("rendered install.sh = %q, want it to contain the stamp %q", got, stamp)
		}
		if strings.Contains(string(got), "__UMBREED_VERSION__") {
			t.Fatalf("rendered install.sh = %q, placeholder was not substituted", got)
		}
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, want 0755", fi.Mode().Perm())
		}
	})

	t.Run("umbree missing install.sh names the inner path", func(t *testing.T) {
		repoDir := t.TempDir()
		dst := filepath.Join(t.TempDir(), "install.sh")
		err := renderInstall("umbree", "v0", "/unused/src", repoDir, dst)
		if err == nil {
			t.Fatal("expected error for a missing inner/umbree/install.sh")
		}
		wantPath := filepath.Join(repoDir, "inner", "umbree", "install.sh")
		if !strings.Contains(err.Error(), wantPath) {
			t.Fatalf("error = %q, want it to name the missing path %q", err.Error(), wantPath)
		}
	})

	// The daemon arm's real failure mode: no daemon checkout resolved. The
	// error must name both the missing template path AND the env var that
	// would point at it — the operator has no other way to know what to set.
	t.Run("umbreed missing template names the path and the env var", func(t *testing.T) {
		repoDir := t.TempDir()
		srcDir := t.TempDir() // no install/install.sh.in inside it
		dst := filepath.Join(t.TempDir(), "install.sh")
		err := renderInstall("umbreed", "v0", srcDir, repoDir, dst)
		if err == nil {
			t.Fatal("expected error for a missing install/install.sh.in")
		}
		wantPath := filepath.Join(srcDir, "install", "install.sh.in")
		if !strings.Contains(err.Error(), wantPath) {
			t.Fatalf("error = %q, want it to name the missing path %q", err.Error(), wantPath)
		}
		if !strings.Contains(err.Error(), "UMBREE_SRC_UMBREED") {
			t.Fatalf("error = %q, want it to name UMBREE_SRC_UMBREED", err.Error())
		}
	})

	t.Run("unknown component is rejected, not silently rendered", func(t *testing.T) {
		repoDir := t.TempDir()
		dst := filepath.Join(t.TempDir(), "install.sh")
		err := renderInstall("nope", "v0", "/unused/src", repoDir, dst)
		if err == nil {
			t.Fatal("expected error for an unrecognized component")
		}
		if !strings.Contains(err.Error(), "nope") {
			t.Fatalf("error = %q, want it to name the component %q", err.Error(), "nope")
		}
	})
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestOrchestrateAbortsOnForbiddenEnvLiteral proves the env-literal guard
// (parity with release.sh's step 2 verify-no-env.sh call) actually aborts
// the cut when a built binary embeds a forbidden config-env literal — the
// happy path (a clean fixture build) never exercises the abort branch.
// Exercised THROUGH orchestrate (preferred over a standalone unit test on
// the guard script) so the assertion covers the real wiring: guard runs
// after compile, before install.sh/assemble/sign, and its failure surfaces
// as an orchestrate error mentioning "verify-no-env".
func TestOrchestrateAbortsOnForbiddenEnvLiteral(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo)

	// Inject one of tools/verify-no-env.sh's forbidden literals
	// (UMBREE_DATA_DIR/UMBREE_SOCKET/UMBREE_SPAWN_HELPER/mustEnv) into the
	// umbree main package's source so the compiled binary embeds it as a
	// string constant `strings` can find.
	forbidden := "package main\n\nimport \"fmt\"\n\nvar version string\n\nfunc main() { fmt.Println(version, \"UMBREE_DATA_DIR\") }\n"
	mainGo := filepath.Join(repo, "cmd", "umbree", "main.go")
	if err := os.WriteFile(mainGo, []byte(forbidden), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "-C", repo, "commit", "-aqm", "inject forbidden literal")
	commit.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	ctx := context.Background()
	_, err := orchestrate(ctx, Options{
		Component: "umbree", OutDir: t.TempDir(), RepoDir: repo,
		MinisignKey: testMinisignKey(t), SkipGate: true,
	})
	if err == nil {
		t.Fatal("expected orchestrate to abort on a forbidden env literal, got nil error")
	}
	if !strings.Contains(err.Error(), "verify-no-env") {
		t.Fatalf("error = %q, want it to mention verify-no-env", err.Error())
	}
}

// TestSrcDirFromEnv pins that a component's source comes from its env var.
func TestSrcDirFromEnv(t *testing.T) {
	t.Setenv("UMBREE_SRC_UMBREED", "/tmp/daemon")
	got, err := srcDirFor("umbreed")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/tmp/daemon" {
		t.Fatalf("srcDirFor = %q", got)
	}
}

// TestSrcDirRefusesWithoutEnv pins that there is NO compiled-in default. This
// repo is public: a default would have to be an absolute path on one
// machine, which is exactly what shipped before.
func TestSrcDirRefusesWithoutEnv(t *testing.T) {
	t.Setenv("UMBREE_SRC_UMBREE", "")
	_, err := srcDirFor("umbree")
	if err == nil {
		t.Fatal("srcDirFor invented a default source path")
	}
	if !strings.Contains(err.Error(), "UMBREE_SRC_UMBREE") {
		t.Fatalf("refusal does not name the variable to set: %v", err)
	}
}

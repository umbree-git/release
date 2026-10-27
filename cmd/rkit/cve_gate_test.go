package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/relconfig"
)

const stubGateScript = `#!/usr/bin/env bash
printf '%s\n' "$@" > "$STUB_GATE_ARGV"
while [ $# -gt 0 ]; do [ "$1" = --report ] && mkdir -p "$2"; shift; done
echo "cve: stub · exit $STUB_GATE_EXIT"
exit "$STUB_GATE_EXIT"
`

type stubGate struct{ path, argv string }

func installStubGate(t *testing.T, exit string) stubGate {
	t.Helper()
	dir := t.TempDir()
	g := stubGate{path: filepath.Join(dir, "cve-gate.sh"), argv: filepath.Join(dir, "argv")}
	mustWriteFile(t, g.path, stubGateScript)
	if err := os.Chmod(g.path, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CVE_GATE", g.path)
	t.Setenv("STUB_GATE_ARGV", g.argv)
	t.Setenv("STUB_GATE_EXIT", exit)
	return g
}

func (g stubGate) calledWith(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(g.argv)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

func TestBuildStableRunsTheSharedGateWithTheSpecArgv(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	src := filepath.Join(t.TempDir(), "src")
	writeFixtureSource(t, src)
	g := installStubGate(t, "0")
	var err error
	stdout := captureStdout(t, func() {
		err = buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: src,
			DryRun: true, SignKey: testMinisignKey(t)})
	})
	if err != nil {
		t.Fatalf("a clean gate must let the build proceed: %v", err)
	}
	stamp, serr := relconfig.Stamp(context.Background(), filepath.Join(repo, "versions", "umbree"), src)
	if serr != nil {
		t.Fatal(serr)
	}
	want := []string{"--report", filepath.Join(repo, "dist", stamp, "cve"), "--gowork", "off",
		"--go", src, "--waivers", filepath.Join(repo, "cve-waivers.tsv")}
	if got := g.calledWith(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("gate argv = %q, want %q", got, want)
	}
	if !strings.Contains(stdout, "cve: stub · exit 0") {
		t.Fatalf("stdout = %q, want the gate's verdict line streamed", stdout)
	}
	if _, err := os.Stat(filepath.Join(repo, "dist", stamp, "umbree-linux-amd64.zip")); err != nil {
		t.Fatalf("a clean gate did not reach orchestrate: %v", err)
	}
}

func TestBuildStableGateRefusalStopsBeforeOrchestrate(t *testing.T) {
	cases := []struct {
		name, exit, want string
		gate             func(t *testing.T, g stubGate)
	}{
		{"findings", "1", "cve gate: exit 1", nil},
		{"cannot run", "2", "cve gate: exit 2", nil},
		{"missing", "0", "cve gate: exit 127", func(t *testing.T, g stubGate) {
			if err := os.Remove(g.path); err != nil {
				t.Fatal(err)
			}
		}},
		{"not executable", "0", "cve gate: exit 126", func(t *testing.T, g stubGate) {
			if err := os.Chmod(g.path, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertGateRefusal(t, c.exit, c.want, c.gate)
		})
	}
}

func assertGateRefusal(t *testing.T, exit, want string, prepare func(*testing.T, stubGate)) {
	t.Helper()
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	g := installStubGate(t, exit)
	if prepare != nil {
		prepare(t, g)
	}
	key := filepath.Join(t.TempDir(), "key")
	mustWriteFile(t, key, "stat-only")
	err := buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: repo, Bump: "patch", SignKey: key})
	if err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Fatalf("err = %v, want it to start %q", err, want)
	}
	assertOnlyReportDirs(t, filepath.Join(repo, "dist"), prepare == nil)
	got, _ := os.ReadFile(filepath.Join(repo, "versions", "umbree"))
	if string(got) != "0.1.0\n" {
		t.Fatalf("versions/umbree = %q after a refused gate, want the committed 0.1.0", got)
	}
}

func assertOnlyReportDirs(t *testing.T, dist string, gateRan bool) {
	t.Helper()
	stamps, err := os.ReadDir(dist)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if gateRan != (len(stamps) == 1) {
		t.Fatalf("dist/ holds %d stamp dirs after a refused gate, want %d", len(stamps), map[bool]int{true: 1, false: 0}[gateRan])
	}
	for _, s := range stamps {
		entries, err := os.ReadDir(filepath.Join(dist, s.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "cve" || !entries[0].IsDir() {
			t.Fatalf("dist/%s holds %v after a refused gate: only the report dir cve/ may", s.Name(), entries)
		}
	}
}

func TestBuildBetaNeverCallsTheGate(t *testing.T) {
	repo := t.TempDir()
	writeFixtureModule(t, repo)
	mustWriteFile(t, filepath.Join(repo, "versions", "umbree.beta"), "0.2.0\n")
	mustWriteFile(t, filepath.Join(repo, "install", "install.sh"), "#!/bin/sh\necho tree\n")
	commitAll(t, repo, "seed beta")
	g := installStubGate(t, "1")
	var err error
	stderr := captureStderr(t, func() {
		err = buildRun(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: repo, Channel: "beta",
			DryRun: true, SignKey: testMinisignKey(t)})
	})
	if err != nil {
		t.Fatalf("a beta build must not be stopped by the gate: %v", err)
	}
	if got := g.calledWith(t); got != nil {
		t.Fatalf("a beta build called the gate with %q", got)
	}
	if strings.Count(stderr, "cve gate: not run on the beta channel") != 1 {
		t.Fatalf("stderr = %q, want one line saying the gate is not run on beta", stderr)
	}
}

func TestBuildRefusesNoVulncheckAsAnUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	var code int
	captureStderr(t, func() {
		code = run(context.Background(), []string{"build", "--component", "umbree", "--no-vulncheck"}, &stdout, &stderr, nil)
	})
	if code != 2 {
		t.Fatalf("rkit build --no-vulncheck exited %d, want the usage status 2", code)
	}
	if !strings.Contains(stderr.String(), "-no-vulncheck") {
		t.Fatalf("stderr = %q, want the refused flag named", stderr.String())
	}
}

func TestCVEGateDefaultsToTheAgentsScriptUnderHome(t *testing.T) {
	t.Setenv("CVE_GATE", "")
	t.Setenv("HOME", "/home/someone")
	if got, want := cveGatePath(), "/home/someone/.agents/scripts/cve-gate.sh"; got != want {
		t.Fatalf("cveGatePath() = %q, want %q", got, want)
	}
	t.Setenv("CVE_GATE", "/elsewhere/gate")
	if got := cveGatePath(); got != "/elsewhere/gate" {
		t.Fatalf("cveGatePath() = %q, want CVE_GATE's value", got)
	}
}

func TestBuildHelpOffersNoWayAroundTheGate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	help := captureStderr(t, func() {
		run(context.Background(), []string{"build", "-h"}, &stdout, &stderr, nil)
	})
	if !strings.Contains(help, "-public") {
		t.Fatalf("build help lists no -public flag: %q", help)
	}
	if strings.Contains(strings.ToLower(help), "cve") || strings.Contains(help, "vulncheck") {
		t.Fatalf("build help still names the CVE gate as a flag's effect: %q", help)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	os.Stdout = orig
	w.Close()
	out := <-done
	r.Close()
	return out
}

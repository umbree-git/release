package main

import (
	"bytes"
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runScan(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(context.Background(), append([]string{"cve-scan"}, args...), &out, &errb, nil)
	return code, out.String(), errb.String()
}

func TestCVEScanPassesTheCutsGateArgv(t *testing.T) {
	repo, src, report := t.TempDir(), t.TempDir(), t.TempDir()
	g := installStubGate(t, "0")
	code, out, _ := runScan(t, "--component", "umbreed", "--repo", repo, "--src", src, "--report", report)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := cveGateArgs(buildOpts{RepoDir: repo, SrcDir: src}, filepath.Join(report, "umbreed"))
	if got := g.calledWith(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("gate argv = %q, want %q", got, want)
	}
	if out != "umbree/umbreed: cve: stub · exit 0\n" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestCVEScanExitCodes(t *testing.T) {
	for gateExit, want := range map[string]int{"0": 0, "1": 1, "2": 2, "64": 2} {
		installStubGate(t, gateExit)
		code, _, _ := runScan(t, "--component", "umbree", "--src", t.TempDir(), "--report", t.TempDir())
		if code != want {
			t.Fatalf("gate exit %s: scan exit = %d, want %d", gateExit, code, want)
		}
	}
}

func TestCVEScanMissingGateCannotRun(t *testing.T) {
	t.Setenv("CVE_GATE", filepath.Join(t.TempDir(), "absent"))
	code, out, _ := runScan(t, "--component", "umbree", "--src", t.TempDir(), "--report", t.TempDir())
	if code != 2 || !strings.HasPrefix(out, "umbree/umbree: cve: cannot-run · cve gate: exit 127") {
		t.Fatalf("exit %d out %q", code, out)
	}
}

func TestCVEScanMissingSourceCannotRun(t *testing.T) {
	g := installStubGate(t, "0")
	missing := filepath.Join(t.TempDir(), "nope")
	code, out, _ := runScan(t, "--component", "umbree", "--src", missing, "--report", t.TempDir())
	if code != 2 || out != "umbree/umbree: cve: cannot-run · missing source "+missing+"\n" {
		t.Fatalf("exit %d out %q", code, out)
	}
	if g.calledWith(t) != nil {
		t.Fatal("gate ran without a source")
	}
}

func TestCVEScanBetaSkipsTheGate(t *testing.T) {
	g := installStubGate(t, "1")
	code, out, _ := runScan(t, "--component", "umbree", "--channel", "beta", "--src", t.TempDir(), "--report", t.TempDir())
	if code != 0 || !strings.Contains(out, "skipped · beta channel") || g.calledWith(t) != nil {
		t.Fatalf("exit %d out %q", code, out)
	}
}

func TestCVEScanUsage(t *testing.T) {
	code, out, _ := runScan(t, "--help")
	if code != 0 || out != cveScanUsage+"\n" {
		t.Fatalf("help: exit %d out %q", code, out)
	}
	for _, args := range [][]string{{"--component", "x", "--report", "r"}, {"--component", "umbree"}, {"--bogus"}} {
		code, _, errs := runScan(t, args...)
		if code != 2 || !strings.Contains(errs, cveScanUsage) {
			t.Fatalf("%q: exit %d stderr %q", args, code, errs)
		}
	}
}

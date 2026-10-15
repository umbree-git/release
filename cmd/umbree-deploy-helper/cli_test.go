//go:build linux

package main

import (
	"strings"
	"testing"
)

func TestHelpGoesToStdoutExitZero(t *testing.T) {
	f := newFixture(t)
	if code := f.runVerb("--help"); code != 0 || !strings.Contains(f.out.String(), "usage:") {
		t.Fatalf("--help: exit %d stdout %q", code, f.out.String())
	}
}

func TestUnknownVerbIsUsageExit2(t *testing.T) {
	f := newFixture(t)
	if code := f.runVerb("install"); code != exitUsage || !strings.Contains(f.errOut.String(), "usage:") {
		t.Fatalf("unknown verb: exit %d stderr %q", code, f.errOut.String())
	}
}

func TestExtraArgumentIsUsageExit2(t *testing.T) {
	f := newFixture(t)
	for _, args := range [][]string{{"render", "/tmp/x"}, {}, {"install-binary", "--stage", "/tmp"}} {
		if code := f.runVerb(args...); code != exitUsage {
			t.Fatalf("%v: exit %d, want %d", args, code, exitUsage)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("a usage refusal ran commands: %v", f.calls)
	}
}

func TestNonRootRefuses(t *testing.T) {
	f := newFixture(t)
	f.h.euid = func() int { return 1000 }
	for _, v := range []string{"preflight", "install-binary", "render", "activate", "restore", "status"} {
		f.refuses("must run as root", v)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a non-root run ran commands: %v", f.calls)
	}
}

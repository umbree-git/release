package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestHelpReferenceByteDiff(t *testing.T) {
	var want bytes.Buffer
	writeReference(&want)
	got, err := os.ReadFile("../../" + referenceFile)
	if err != nil {
		t.Fatalf("read %s: %v; regenerate with: go run ./cmd/%s docs > %s", referenceFile, err, toolName, referenceFile)
	}
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("%s is stale; regenerate with: go run ./cmd/%s docs > %s", referenceFile, toolName, referenceFile)
	}
	if r := invoke(t, nil, "docs"); r.code != 0 || r.stdout != want.String() {
		t.Fatalf("the docs verb: exit %d", r.code)
	}
	for _, args := range [][]string{{"--help"}, {"serve", "--help"}, {"admin", "--help"}, {"admin", "add", "--help"}, {"admin", "reset-totp", "--help"}} {
		page := invoke(t, nil, args...).stdout
		if page == "" || !strings.Contains(want.String(), page) {
			t.Fatalf("the reference does not carry `%s` verbatim", strings.Join(args, " "))
		}
	}
}

func TestUnknownVerbPrintsHelpExit2(t *testing.T) {
	for _, tc := range []struct {
		args []string
		page string
	}{
		{[]string{"frobnicate"}, "Commands:"},
		{[]string{"help"}, "Commands:"},
		{[]string{"signup"}, "Commands:"},
		{[]string{"admin", "frobnicate"}, "Subcommands:"},
		{[]string{"admin", "signup"}, "Subcommands:"},
	} {
		r := invoke(t, nil, tc.args...)
		if r.code != exitUsage || r.stdout != "" || !strings.Contains(r.stderr, tc.page) {
			t.Fatalf("%v: exit %d stdout %q stderr %q", tc.args, r.code, r.stdout, r.stderr)
		}
	}
	if r := invoke(t, nil, "admin", "--help"); r.code != 0 || !strings.Contains(r.stdout, "reset-totp") {
		t.Fatalf("control, admin --help: exit %d %q", r.code, r.stdout)
	}
}

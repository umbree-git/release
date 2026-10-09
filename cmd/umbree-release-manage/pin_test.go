package main

import (
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/store"
)

func TestPinUnpinCLI(t *testing.T) {
	dir, id := seedPublicRow(t)
	stamp := "v0.1.8.2026.09.20.7162a3f3"
	vars := map[string]string{"USER": "op-alice"}
	if r := invoke(t, vars, "admin", "pin", "umbree", "--data-dir", dir); r.code != exitUsage {
		t.Fatalf("one argument: exit %d", r.code)
	}
	if r := invoke(t, vars, "admin", "pin", "umbree", "v0.0.0.2026.01.01.00000000", "--data-dir", dir); r.code != 1 {
		t.Fatalf("an unknown stamp: exit %d", r.code)
	}
	r := invoke(t, vars, "admin", "pin", "umbree", stamp, "--data-dir", dir)
	if r.code != 0 || !strings.Contains(r.stdout, "pinned") {
		t.Fatalf("pin: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	pinned := func() bool {
		s, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		rv, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		return rv.Permanent
	}
	if !pinned() {
		t.Fatal("pin did not set the row permanent")
	}
	if r := invoke(t, vars, "admin", "unpin", "umbree", stamp, "--data-dir", dir); r.code != 0 || pinned() {
		t.Fatalf("unpin: exit %d, still pinned %v", r.code, pinned())
	}
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	log, err := s.AuditLog()
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range log {
		if e.Action == "pin" || e.Action == "unpin" {
			actions = append(actions, e.Action+" "+e.Actor)
		}
	}
	if strings.Join(actions, ",") != "pin op-alice,unpin op-alice" {
		t.Fatalf("audit %v, want pin then unpin by op-alice", actions)
	}
}

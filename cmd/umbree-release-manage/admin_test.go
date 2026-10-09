package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

func seedPublicRow(t *testing.T) (string, int64) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	stamp := "v0.1.8.2026.09.20.7162a3f3"
	arts, _ := json.Marshal([]map[string]any{{"key": "umbree/" + stamp + "/umbree-linux-amd64.zip", "size": 1, "sha256": strings.Repeat("a", 64)}})
	id, err := s.InsertStaged(store.ReleaseVersion{Component: "umbree", Channel: "production", Version: "0.1.8", Stamp: stamp,
		ArtifactsJSON: string(arts), SumsKey: "s", MinisigKey: "m", CreatedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Promote(id, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, id
}

func TestAdminMarkYankedCatalogOnlyAudited(t *testing.T) {
	dir, id := seedPublicRow(t)
	vars := map[string]string{"USER": "op-alice"}
	if r := invoke(t, vars, "admin", "mark-yanked", strconv.FormatInt(id, 10), "--data-dir", dir); r.code != exitUsage || !strings.Contains(r.stderr, "--reason") {
		t.Fatalf("no reason: exit %d stderr %q", r.code, r.stderr)
	}
	if r := invoke(t, vars, "admin", "mark-yanked", "first", "--data-dir", dir, "--reason", "x"); r.code != exitUsage {
		t.Fatalf("a non-numeric id: exit %d", r.code)
	}
	r := invoke(t, vars, "admin", "mark-yanked", strconv.FormatInt(id, 10), "--data-dir", dir, "--reason", "latest.json pulled by hand")
	if r.code != 0 {
		t.Fatalf("mark-yanked: exit %d stderr %q", r.code, r.stderr)
	}
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rv, _ := s.Get(id)
	log, err := s.AuditLog()
	if err != nil || rv.State != "yanked" || rv.IsCurrent || len(log) != 1 || log[0].Actor != "op-alice" || log[0].Detail != "latest.json pulled by hand" {
		t.Fatalf("row %+v audit %+v %v", rv, log, err)
	}
	assertMarkYankedPages(t)
}

func assertMarkYankedPages(t *testing.T) {
	t.Helper()
	page := invoke(t, nil, "admin", "mark-yanked", "--help").stdout
	for _, flag := range []string{"--r2-", "bucket", "--public-base-url", "--listen"} {
		if strings.Contains(page, flag) {
			t.Fatalf("mark-yanked takes %s; it writes the catalog only:\n%s", flag, page)
		}
	}
	if !strings.Contains(page, "--reason") || !strings.Contains(invoke(t, nil, "--help").stdout, "admin mark-yanked") {
		t.Fatal("mark-yanked is not on the help pages")
	}
	if r := invoke(t, nil, "admin"); r.code != 0 || !strings.Contains(r.stdout, "mark-yanked") {
		t.Fatalf("bare admin: exit %d stdout %q", r.code, r.stdout)
	}
	if r := invoke(t, nil, "admin", "bogus"); r.code != exitUsage || !strings.Contains(r.stderr, "mark-yanked") {
		t.Fatalf("admin bogus: exit %d stderr %q", r.code, r.stderr)
	}
}

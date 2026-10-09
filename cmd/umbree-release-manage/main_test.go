package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/store"
)

type result struct {
	code           int
	stdout, stderr string
}

func invoke(t *testing.T, vars map[string]string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	e := &env{ctx: ctx, stdout: &out, stderr: &errOut, getenv: func(k string) string { return vars[k] }}
	code := run(e, args)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func dirSnapshot(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		lines = append(lines, e.Name()+" "+hex.EncodeToString(sum[:]))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestServeRefusesSameBucket(t *testing.T) {
	dir := t.TempDir()
	r := invoke(t, nil, "serve", "--data-dir", dir, "--gated-bucket", "downloads", "--public-bucket", "downloads", "--listen", "127.0.0.1:0")
	if r.code != exitUsage {
		t.Fatalf("same bucket: exit %d, want %d; stderr %s", r.code, exitUsage, r.stderr)
	}
	if !strings.Contains(r.stderr, "--gated-bucket and --public-bucket") || !strings.Contains(r.stderr, "Usage:") {
		t.Fatalf("stderr %q does not name the refusal with the serve page", r.stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, store.DBFile)); !os.IsNotExist(err) {
		t.Fatalf("a refused serve opened the catalog: %v", err)
	}
	vars := map[string]string{"UMBREE_R2_GATED_BUCKET": "downloads", "UMBREE_R2_BUCKET": "downloads"}
	if r := invoke(t, vars, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0"); r.code != exitUsage {
		t.Fatalf("same bucket through the environment: exit %d, want %d", r.code, exitUsage)
	}
	r = invoke(t, vars, "serve", "--data-dir", dir, "--gated-bucket", "gated-private", "--listen", "127.0.0.1:0")
	if r.code != 0 {
		t.Fatalf("keep-control: exit %d; stderr %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "listening on 127.0.0.1:") {
		t.Fatalf("keep-control stdout %q", r.stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, store.DBFile)); err != nil {
		t.Fatalf("keep-control did not migrate a catalog: %v", err)
	}
}

func TestServeRequiresDataDir(t *testing.T) {
	r := invoke(t, nil, "serve", "--listen", "127.0.0.1:0")
	if r.code != exitUsage || !strings.Contains(r.stderr, "--data-dir is required") {
		t.Fatalf("no data dir: exit %d stderr %q", r.code, r.stderr)
	}
	if r := invoke(t, nil, "serve", "--data-dir", " ", "--listen", "127.0.0.1:0"); r.code != exitUsage {
		t.Fatalf("blank --data-dir: exit %d", r.code)
	}
	r = invoke(t, map[string]string{"UMBREE_MANAGE_DATA_DIR": "  "}, "serve", "--listen", "127.0.0.1:0")
	if r.code != exitUsage {
		t.Fatalf("blank data dir from the environment: exit %d", r.code)
	}
	dir := t.TempDir()
	r = invoke(t, map[string]string{"UMBREE_MANAGE_DATA_DIR": dir}, "serve", "--listen", "127.0.0.1:0")
	if r.code != 0 {
		t.Fatalf("data dir from the environment: exit %d stderr %s", r.code, r.stderr)
	}
}

func TestHelpOnUsageError(t *testing.T) {
	cases := [][]string{
		{"frobnicate"},
		{"serve", "--no-such-flag"},
		{"serve", "--data-dir", t.TempDir(), "extra"},
		{"migrate", "--data-dir", t.TempDir()},
		{"help"},
	}
	for _, args := range cases {
		r := invoke(t, nil, args...)
		if r.code != exitUsage {
			t.Errorf("%v: exit %d, want %d", args, r.code, exitUsage)
		}
		if r.stdout != "" {
			t.Errorf("%v: a refusal wrote to stdout: %q", args, r.stdout)
		}
		if !strings.Contains(r.stderr, "Usage:") || !strings.Contains(r.stderr, toolName) {
			t.Errorf("%v: stderr carries no help page: %q", args, r.stderr)
		}
	}
	r := invoke(t, nil, "serve", "--no-such-flag")
	if !strings.Contains(r.stderr, "--gated-bucket") {
		t.Errorf("a serve refusal does not carry the serve page: %q", r.stderr)
	}
}

func TestHelpFlagStdoutExit0(t *testing.T) {
	for _, args := range [][]string{{}, {"-h"}, {"--help"}, {"serve", "-h"}, {"serve", "--help"}, {"migrate", "--help"}, {"serve", "--data-dir", "x", "-h"}} {
		r := invoke(t, nil, args...)
		if r.code != 0 || r.stderr != "" || !strings.Contains(r.stdout, "Usage:") {
			t.Errorf("%v: exit %d stdout %q stderr %q", args, r.code, r.stdout, r.stderr)
		}
	}
	top := invoke(t, nil, "--help").stdout
	for _, want := range []string{"serve", "migrate"} {
		if !strings.Contains(top, want) {
			t.Errorf("top page lacks %q", want)
		}
	}
	serve := invoke(t, nil, "serve", "--help").stdout
	for _, want := range []string{"--data-dir", "--listen", "--r2-account", "--r2-creds", "--gated-bucket", "--public-bucket", "--public-base-url", "UMBREE_R2_GATED_BUCKET"} {
		if !strings.Contains(serve, want) {
			t.Errorf("serve page lacks %q", want)
		}
	}
}

func TestMigrateCheckWritesNothing(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := dirSnapshot(t, dir)
	r := invoke(t, nil, "migrate", "--check", "--data-dir", dir)
	if r.code != 0 || !strings.Contains(r.stdout, "0 pending") {
		t.Fatalf("current catalog: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	if after := dirSnapshot(t, dir); after != before {
		t.Fatalf("migrate --check changed the data dir:\n%s\n---\n%s", before, after)
	}

	empty := t.TempDir()
	if r := invoke(t, nil, "migrate", "--check", "--data-dir", empty); r.code != 1 {
		t.Fatalf("no catalog: exit %d, want 1", r.code)
	}
	if got := dirSnapshot(t, empty); got != "" {
		t.Fatalf("migrate --check created files: %s", got)
	}

	bare := t.TempDir()
	path := filepath.Join(bare, store.DBFile)
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before = dirSnapshot(t, bare)
	r = invoke(t, nil, "migrate", "--check", "--data-dir", bare)
	if r.code != 1 || !strings.Contains(r.stderr, "pending migrations: 1 ") {
		t.Fatalf("unmigrated catalog: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	if after := dirSnapshot(t, bare); after != before {
		t.Fatalf("migrate --check migrated:\n%s\n---\n%s", before, after)
	}
}

func TestMigrateCheckReadsLiveCatalog(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	wal, err := os.Stat(filepath.Join(dir, store.DBFile+"-wal"))
	if err != nil || wal.Size() == 0 {
		t.Fatalf("the open store left no WAL (%v); the test proves nothing", err)
	}
	db, err := os.ReadFile(filepath.Join(dir, store.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	r := invoke(t, nil, "migrate", "--check", "--data-dir", dir)
	if r.code != 0 || !strings.Contains(r.stdout, "1 applied, 0 pending") {
		t.Fatalf("check while serve holds the catalog: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	after, err := os.ReadFile(filepath.Join(dir, store.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(db, after) {
		t.Fatal("migrate --check wrote to catalog.db")
	}
}

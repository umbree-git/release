package store_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/umbree-git/release/internal/manage/store"
)

var epoch = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func openStore(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open(%s): %v", dir, err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func stampFor(version string, n int) string {
	return fmt.Sprintf("v%s.2026.10.08.%08x", version, n)
}

func stagedRow(component, version string, n int, at time.Time) store.ReleaseVersion {
	stamp := stampFor(version, n)
	base := component + "/production/" + stamp + "/"
	return store.ReleaseVersion{
		Component:     component,
		Channel:       "production",
		Version:       version,
		Stamp:         stamp,
		ArtifactsJSON: `[{"key":"` + base + `umbree-linux-arm64.zip","size":1,"sha256":"00"}]`,
		SumsKey:       base + "SHA256SUMS.txt",
		MinisigKey:    base + "SHA256SUMS.txt.minisig",
		CreatedAt:     at,
	}
}

func insert(t *testing.T, s *store.Store, rv store.ReleaseVersion) int64 {
	t.Helper()
	id, err := s.InsertStaged(rv)
	if err != nil {
		t.Fatalf("InsertStaged(%s %s): %v", rv.Component, rv.Stamp, err)
	}
	return id
}

func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	path := filepath.Join(dir, store.DBFile)
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw %s: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func tableChecksum(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	rows, err := db.Query(`SELECT * FROM ` + table + ` ORDER BY 1`)
	if err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%v\n", vals)
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d:%s", n, hex.EncodeToString(h.Sum(nil)))
}

func TestMigrateFromEmpty(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	applied, err := s.AppliedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	want := store.Migrations()
	if len(want) == 0 {
		t.Fatal("the binary carries no migrations")
	}
	if len(applied) != len(want) {
		t.Fatalf("applied %v, want %v", applied, want)
	}
	for i := range want {
		if applied[i] != want[i] {
			t.Fatalf("applied[%d] = %v, want %v", i, applied[i], want[i])
		}
	}
	db := rawDB(t, dir)
	for _, col := range []string{"permanent", "expired_at", "gated_pruned_at", "public_pruned_at", "is_current"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('release_versions') WHERE name = ?`, col).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("release_versions has no %s column", col)
		}
	}
}

func TestMigrateIdempotentOnPopulated(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	insert(t, s, stagedRow("umbree", "0.1.8", 1, epoch))
	insert(t, s, stagedRow("umbreed", "0.1.8", 2, epoch))
	if err := s.IssueNonce("n1", epoch, epoch.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db := rawDB(t, dir)
	before := map[string]string{}
	for _, table := range []string{"release_versions", "nonces", "migrations"} {
		before[table] = tableChecksum(t, db, table)
	}
	s2 := openStore(t, dir)
	if _, err := s2.AppliedMigrations(); err != nil {
		t.Fatal(err)
	}
	for table, sum := range before {
		if got := tableChecksum(t, db, table); got != sum {
			t.Errorf("%s changed on a second Open: %s -> %s", table, sum, got)
		}
	}
	if !strings.HasPrefix(before["release_versions"], "2:") {
		t.Fatalf("populated fixture has %s rows, want 2", before["release_versions"])
	}
}

func TestMigrateRefusesUnknownLedgerVersion(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ahead := len(store.Migrations()) + 1
	db := rawDB(t, dir)
	if _, err := db.Exec(`INSERT INTO migrations (version, name, applied_at) VALUES (?, 'from a newer binary', 0)`, ahead); err != nil {
		t.Fatal(err)
	}
	s2, err := store.Open(dir)
	if err == nil {
		_ = s2.Close()
		t.Fatal("Open accepted a ledger ahead of the binary")
	}
	if !errors.Is(err, store.ErrLedgerMismatch) {
		t.Fatalf("error %v is not ErrLedgerMismatch", err)
	}
	if !strings.Contains(err.Error(), fmt.Sprint(ahead)) {
		t.Errorf("error %q does not name ledger version %d", err, ahead)
	}
}

func TestInsertStagedRoundTripsArtifactsVerbatim(t *testing.T) {
	s := openStore(t, t.TempDir())
	rv := stagedRow("umbree", "0.1.8", 1, epoch)
	rv.ArtifactsJSON = "[ {\"key\":\"umbree/production/" + rv.Stamp + "/a.zip\",  \"size\": 7, \"sha256\":\"ab\"} ]"
	id := insert(t, s, rv)
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArtifactsJSON != rv.ArtifactsJSON {
		t.Fatalf("artifacts_json = %q, want %q", got.ArtifactsJSON, rv.ArtifactsJSON)
	}
	if got.State != "staged" || got.IsCurrent || got.Permanent {
		t.Fatalf("new row = state %q current %v permanent %v, want staged/false/false", got.State, got.IsCurrent, got.Permanent)
	}
	if got.SumsKey != rv.SumsKey || got.MinisigKey != rv.MinisigKey || got.Version != rv.Version {
		t.Fatalf("row = %+v, want keys and version of %+v", got, rv)
	}
	if !got.CreatedAt.Equal(epoch) {
		t.Fatalf("created_at = %v, want %v", got.CreatedAt, epoch)
	}
	byStamp, err := s.ByStamp("umbree", "production", rv.Stamp)
	if err != nil || byStamp.ID != id {
		t.Fatalf("ByStamp = %+v, %v; want row %d", byStamp, err, id)
	}
}

func TestUniqueStampPerChannel(t *testing.T) {
	s := openStore(t, t.TempDir())
	rv := stagedRow("umbree", "0.1.8", 1, epoch)
	insert(t, s, rv)
	rv.CreatedAt = epoch.Add(time.Hour)
	_, err := s.InsertStaged(rv)
	if !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("second insert: %v, want ErrDuplicate", err)
	}
	rows, err := s.List("umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	insert(t, s, stagedRow("umbreed", "0.1.8", 1, epoch))
}

func TestOneCurrentPerChannel(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	a := insert(t, s, stagedRow("umbree", "0.1.8", 1, epoch))
	b := insert(t, s, stagedRow("umbree", "0.1.9", 2, epoch))
	c := insert(t, s, stagedRow("umbreed", "0.1.9", 3, epoch))
	db := rawDB(t, dir)
	if _, err := db.Exec(`UPDATE release_versions SET is_current = 1 WHERE id = ?`, a); err != nil {
		t.Fatalf("first current: %v", err)
	}
	if _, err := db.Exec(`UPDATE release_versions SET is_current = 1 WHERE id = ?`, b); err == nil {
		t.Fatal("a second current row on umbree/production was accepted")
	}
	if _, err := db.Exec(`UPDATE release_versions SET is_current = 1 WHERE id = ?`, c); err != nil {
		t.Fatalf("a current row on another component was refused: %v", err)
	}
}

func TestCheckLedgerReadsThroughWAL(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	insert(t, s, stagedRow("umbree", "0.1.8", 1, epoch))
	wal, err := os.Stat(filepath.Join(dir, store.DBFile+"-wal"))
	if err != nil || wal.Size() == 0 {
		t.Fatalf("the open store left no WAL to read through (%v); the test proves nothing", err)
	}
	report, err := store.CheckLedger(dir)
	if err != nil {
		t.Fatalf("CheckLedger on a served catalog: %v", err)
	}
	if len(report.Applied) != len(store.Migrations()) || len(report.Pending) != 0 {
		t.Fatalf("CheckLedger on a served catalog: applied %v pending %v, want every migration applied", report.Applied, report.Pending)
	}
}

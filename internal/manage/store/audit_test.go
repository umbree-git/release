package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

func TestMarkYankedWritesAudit(t *testing.T) {
	s := openStore(t, t.TempDir())
	id := insert(t, s, stagedRow("umbree", "0.1.0", 1, epoch))
	promoteRow(t, s, id)
	if err := s.MarkYanked(id, "operator", "manifest pulled by hand", epoch.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rv, _ := s.Get(id)
	if rv.State != "yanked" || rv.IsCurrent {
		t.Fatalf("row %+v", rv)
	}
	log, err := s.AuditLog()
	if err != nil || len(log) != 1 {
		t.Fatalf("audit %+v, %v", log, err)
	}
	e := log[0]
	if e.RowID != id || e.Actor != "operator" || e.Action != "mark-yanked" || e.Detail != "manifest pulled by hand" || !e.At.Equal(epoch.Add(2*time.Hour)) {
		t.Fatalf("audit entry %+v", e)
	}
	if err := s.MarkYanked(id, "operator", "again", epoch); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("marking a yanked row: %v", err)
	}
	if log, _ := s.AuditLog(); len(log) != 1 {
		t.Fatal("a refused mark wrote an audit entry")
	}
}

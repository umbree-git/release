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

func TestAdoptCurrentGuardedAndAudited(t *testing.T) {
	s := openStore(t, t.TempDir())
	a := insert(t, s, stagedRow("umbree", "0.1.0", 1, epoch))
	promoteRow(t, s, a)
	b := insert(t, s, stagedRow("umbree", "0.2.0", 2, epoch))
	promoteRow(t, s, b)
	if ok, err := s.AdoptCurrent(a, "op", "why", epoch); err != nil || ok {
		t.Fatalf("with a current row: adopted %v, %v", ok, err)
	}
	if err := s.MarkYanked(b, "op", "pulled", epoch); err != nil {
		t.Fatal(err)
	}
	staged := insert(t, s, stagedRow("umbree", "0.3.0", 3, epoch))
	if _, err := s.AdoptCurrent(staged, "op", "why", epoch); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("adopting a staged row: %v", err)
	}
	if _, err := s.AdoptCurrent(b, "op", "why", epoch); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("adopting a yanked row: %v", err)
	}
	ok, err := s.AdoptCurrent(a, "op", "manifest names it", epoch.Add(time.Hour))
	if err != nil || !ok {
		t.Fatalf("adopt with no current: %v, %v", ok, err)
	}
	rv, _ := s.Get(a)
	log, _ := s.AuditLog()
	last := log[len(log)-1]
	if !rv.IsCurrent || last.Action != "backfill-current" || last.RowID != a || last.Detail != "manifest names it" {
		t.Fatalf("row %+v audit %+v", rv, last)
	}
}

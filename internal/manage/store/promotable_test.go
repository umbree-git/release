package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

func promoteRow(t *testing.T, s *store.Store, id int64) {
	t.Helper()
	if err := s.Promote(id, "test-operator", epoch.Add(time.Hour)); err != nil {
		t.Fatalf("Promote(%d): %v", id, err)
	}
}

func versionsOf(rows []store.ReleaseVersion) []string {
	var out []string
	for _, rv := range rows {
		out = append(out, rv.Version)
	}
	return out
}

func assertPromotable(t *testing.T, s *store.Store, want ...string) {
	t.Helper()
	rows, err := s.Promotable("umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	got := versionsOf(rows)
	if len(got) != len(want) {
		t.Fatalf("Promotable = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Promotable = %v, want %v", got, want)
		}
	}
}

func TestPromotableExcludesOlderThanCurrent(t *testing.T) {
	s := openStore(t, t.TempDir())
	cur := insert(t, s, stagedRow("umbree", "0.9.9", 1, epoch))
	promoteRow(t, s, cur)
	insert(t, s, stagedRow("umbree", "0.3.26", 2, epoch.Add(2*time.Hour)))
	insert(t, s, stagedRow("umbree", "0.10.0", 3, epoch.Add(-time.Hour)))
	assertPromotable(t, s, "0.10.0")
}

func datedRow(version, date string, n int) store.ReleaseVersion {
	rv := stagedRow("umbree", version, n, epoch)
	rv.Stamp = fmt.Sprintf("v%s.%s.%08x", version, date, n)
	base := "umbree/production/" + rv.Stamp + "/"
	rv.ArtifactsJSON = `[{"key":"` + base + `umbree-linux-arm64.zip","size":1,"sha256":"00"}]`
	rv.SumsKey, rv.MinisigKey = base+"SHA256SUMS.txt", base+"SHA256SUMS.txt.minisig"
	return rv
}

func TestPromotableExcludesEqualToCurrent(t *testing.T) {
	s := openStore(t, t.TempDir())
	cur := insert(t, s, datedRow("0.5.0", "2026.10.08", 5))
	promoteRow(t, s, cur)
	insert(t, s, datedRow("0.5.0", "2026.10.07", 9))
	assertPromotable(t, s)
	current, err := s.Get(cur)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.IsPromotable(*current); err != nil || ok {
		t.Fatalf("the current row is promotable: %v, %v", ok, err)
	}
	asStaged := *current
	asStaged.State = "staged"
	if ok, _ := s.IsPromotable(asStaged); ok {
		t.Fatal("a staged row with the current stamp is promotable")
	}
	insert(t, s, datedRow("0.5.0", "2026.10.09", 1))
	assertPromotable(t, s, "0.5.0")
}

func TestPromotableAllStagedWhenNoCurrent(t *testing.T) {
	s := openStore(t, t.TempDir())
	insert(t, s, stagedRow("umbree", "0.1.0", 1, epoch))
	insert(t, s, stagedRow("umbree", "0.2.0", 2, epoch))
	assertPromotable(t, s, "0.2.0", "0.1.0")
}

func TestPromotableNewestFirst(t *testing.T) {
	s := openStore(t, t.TempDir())
	cur := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
	promoteRow(t, s, cur)
	insert(t, s, stagedRow("umbree", "0.3.0", 2, epoch.Add(3*time.Hour)))
	insert(t, s, stagedRow("umbree", "0.10.1", 3, epoch))
	insert(t, s, stagedRow("umbree", "0.9.0", 4, epoch.Add(time.Hour)))
	assertPromotable(t, s, "0.10.1", "0.9.0", "0.3.0")
}

func TestPromotableIgnoresYankedAndExpired(t *testing.T) {
	s := openStore(t, t.TempDir())
	old := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
	promoteRow(t, s, old)
	cur := insert(t, s, stagedRow("umbree", "0.5.0", 2, epoch))
	promoteRow(t, s, cur)
	if err := s.Transition(old, "public", "yanked", epoch.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	gone := insert(t, s, stagedRow("umbree", "0.7.0", 3, epoch))
	if err := s.Transition(gone, "staged", "expired", epoch); err != nil {
		t.Fatal(err)
	}
	insert(t, s, stagedRow("umbree", "0.3.0", 4, epoch))
	assertPromotable(t, s)
	insert(t, s, stagedRow("umbree", "0.6.0", 5, epoch))
	assertPromotable(t, s, "0.6.0")
}

func TestIsPromotableIsMembership(t *testing.T) {
	s := openStore(t, t.TempDir())
	cur := insert(t, s, stagedRow("umbree", "0.5.0", 1, epoch))
	promoteRow(t, s, cur)
	ids := []int64{
		insert(t, s, stagedRow("umbree", "0.4.0", 2, epoch)),
		insert(t, s, stagedRow("umbree", "0.6.0", 3, epoch)),
		insert(t, s, stagedRow("umbree", "0.7.0", 4, epoch)),
		cur,
	}
	listed := map[int64]bool{}
	rows, err := s.Promotable("umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	for _, rv := range rows {
		listed[rv.ID] = true
	}
	for _, id := range ids {
		rv, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := s.IsPromotable(*rv)
		if err != nil {
			t.Fatal(err)
		}
		if ok != listed[id] {
			t.Fatalf("row %d (%s): IsPromotable %v, listed %v", id, rv.Version, ok, listed[id])
		}
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d rows, want 2", len(listed))
	}
}

func TestPromoteFlipsCurrent(t *testing.T) {
	s := openStore(t, t.TempDir())
	a := insert(t, s, stagedRow("umbree", "0.1.0", 1, epoch))
	b := insert(t, s, stagedRow("umbree", "0.2.0", 2, epoch))
	promoteRow(t, s, a)
	promoteRow(t, s, b)
	ra, _ := s.Get(a)
	rb, _ := s.Get(b)
	if ra.IsCurrent || !rb.IsCurrent || rb.State != "public" || rb.PromotedAt.IsZero() {
		t.Fatalf("after two promotes: a %+v, b %+v", ra, rb)
	}
	cur, err := s.Current("umbree", "production")
	if err != nil || cur.ID != b {
		t.Fatalf("Current = %+v, %v; want row %d", cur, err, b)
	}
	if err := s.Promote(a, "test-operator", epoch); err == nil {
		t.Fatal("a public row was promoted again")
	}
}

func TestPromotableFloorIsHighWaterMark(t *testing.T) {
	t.Run("current row below a yanked mark", func(t *testing.T) {
		s := openStore(t, t.TempDir())
		low := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
		promoteRow(t, s, low)
		top := insert(t, s, stagedRow("umbree", "0.3.0", 2, epoch))
		promoteRow(t, s, top)
		if err := s.Yank(top, low, "test-operator", epoch.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		insert(t, s, stagedRow("umbree", "0.2.5", 3, epoch))
		assertPromotable(t, s)
		insert(t, s, stagedRow("umbree", "0.4.0", 4, epoch))
		assertPromotable(t, s, "0.4.0")
	})
	t.Run("no current row after mark-yanked", func(t *testing.T) {
		s := openStore(t, t.TempDir())
		low := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
		promoteRow(t, s, low)
		top := insert(t, s, stagedRow("umbree", "0.3.0", 2, epoch))
		promoteRow(t, s, top)
		if err := s.MarkYanked(top, "op", "manifest deleted by hand", epoch); err != nil {
			t.Fatal(err)
		}
		insert(t, s, stagedRow("umbree", "0.1.0", 3, epoch))
		insert(t, s, stagedRow("umbree", "0.2.5", 4, epoch))
		assertPromotable(t, s)
		insert(t, s, stagedRow("umbree", "0.3.1", 5, epoch))
		assertPromotable(t, s, "0.3.1")
	})
	t.Run("current row is the mark", func(t *testing.T) {
		s := openStore(t, t.TempDir())
		cur := insert(t, s, stagedRow("umbree", "0.5.0", 1, epoch))
		promoteRow(t, s, cur)
		insert(t, s, stagedRow("umbree", "0.4.0", 2, epoch))
		insert(t, s, stagedRow("umbree", "0.6.0", 3, epoch))
		assertPromotable(t, s, "0.6.0")
	})
}

func TestPromotableFloorSurvivesExpiry(t *testing.T) {
	t.Run("mark expired from public", func(t *testing.T) {
		s := openStore(t, t.TempDir())
		low := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
		promoteRow(t, s, low)
		top := insert(t, s, stagedRow("umbree", "0.3.0", 2, epoch))
		promoteRow(t, s, top)
		if err := s.Transition(top, "public", "expired", epoch.Add(3*time.Hour)); err != nil {
			t.Fatal(err)
		}
		insert(t, s, stagedRow("umbree", "0.2.5", 3, epoch))
		assertPromotable(t, s)
		insert(t, s, stagedRow("umbree", "0.3.1", 4, epoch))
		assertPromotable(t, s, "0.3.1")
	})
	t.Run("mark expired from yanked", func(t *testing.T) {
		s := openStore(t, t.TempDir())
		low := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
		promoteRow(t, s, low)
		top := insert(t, s, stagedRow("umbree", "0.3.0", 2, epoch))
		promoteRow(t, s, top)
		if err := s.Yank(top, low, "test-operator", epoch.Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if err := s.Transition(top, "yanked", "expired", epoch.Add(3*time.Hour)); err != nil {
			t.Fatal(err)
		}
		insert(t, s, stagedRow("umbree", "0.2.5", 3, epoch))
		assertPromotable(t, s)
	})
	t.Run("a never-public expired row does not raise the floor", func(t *testing.T) {
		s := openStore(t, t.TempDir())
		cur := insert(t, s, stagedRow("umbree", "0.2.0", 1, epoch))
		promoteRow(t, s, cur)
		never := insert(t, s, stagedRow("umbree", "0.9.0", 2, epoch))
		if err := s.Transition(never, "staged", "expired", epoch); err != nil {
			t.Fatal(err)
		}
		insert(t, s, stagedRow("umbree", "0.4.0", 3, epoch))
		assertPromotable(t, s, "0.4.0")
	})
}

func TestEveryPublicRowHasPromotedAt(t *testing.T) {
	s := openStore(t, t.TempDir())
	flipped := insert(t, s, stagedRow("umbree", "0.1.0", 1, epoch))
	promoteRow(t, s, flipped)
	backfilled, err := s.InsertBackfilled(stagedRow("umbree", "0.0.9", 2, epoch), false, epoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	staged := insert(t, s, stagedRow("umbree", "0.2.0", 3, epoch))
	for _, id := range []int64{flipped, backfilled} {
		rv, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if rv.State != "public" || rv.PromotedAt.IsZero() {
			t.Fatalf("row %d reached public with promoted_at %v", id, rv.PromotedAt)
		}
	}
	if rv, _ := s.Get(staged); !rv.PromotedAt.IsZero() {
		t.Fatal("a staged row carries promoted_at")
	}
	rows, err := s.List("umbree", "production", "public")
	if err != nil {
		t.Fatal(err)
	}
	for _, rv := range rows {
		if rv.PromotedAt.IsZero() {
			t.Fatalf("public row %d has no promoted_at", rv.ID)
		}
	}
}

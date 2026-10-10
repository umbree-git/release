package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

var allowedEdges = map[[2]string]bool{
	{"staged", "public"}:  true,
	{"staged", "expired"}: true,
	{"public", "yanked"}:  true,
	{"public", "expired"}: true,
	{"yanked", "expired"}: true,
}

var pathTo = map[string][]string{
	"staged":  nil,
	"public":  {"public"},
	"yanked":  {"public", "yanked"},
	"expired": {"expired"},
}

var stateNames = []string{"staged", "public", "yanked", "expired"}

func rowIn(t *testing.T, s *store.Store, n int, state string) int64 {
	t.Helper()
	id := insert(t, s, stagedRow("umbree", "0.1.8", n, epoch))
	from := "staged"
	for _, to := range pathTo[state] {
		if err := s.Transition(id, from, to, epoch.Add(time.Minute)); err != nil {
			t.Fatalf("drive row %d %s -> %s: %v", id, from, to, err)
		}
		from = to
	}
	return id
}

func TestTransitionAllowedEdges(t *testing.T) {
	s := openStore(t, t.TempDir())
	n := 0
	for edge := range allowedEdges {
		n++
		id := rowIn(t, s, n, edge[0])
		at := epoch.Add(time.Duration(n) * time.Hour)
		if err := s.Transition(id, edge[0], edge[1], at); err != nil {
			t.Errorf("%s -> %s refused: %v", edge[0], edge[1], err)
			continue
		}
		got, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != edge[1] {
			t.Errorf("%s -> %s left state %q", edge[0], edge[1], got.State)
		}
		stampOf := map[string]time.Time{"public": got.PromotedAt, "yanked": got.YankedAt, "expired": got.ExpiredAt}
		if !stampOf[edge[1]].Equal(at) {
			t.Errorf("%s -> %s did not stamp its time: %+v", edge[0], edge[1], got)
		}
	}
}

func TestTransitionRefusedEdges(t *testing.T) {
	s := openStore(t, t.TempDir())
	n := 0
	refused := 0
	for _, from := range stateNames {
		for _, to := range append(stateNames, "", "deleted") {
			if allowedEdges[[2]string{from, to}] {
				continue
			}
			n++
			refused++
			id := rowIn(t, s, n, from)
			err := s.Transition(id, from, to, epoch.Add(time.Hour))
			if !errors.Is(err, store.ErrBadState) {
				t.Errorf("%s -> %q: %v, want ErrBadState", from, to, err)
			}
			got, gerr := s.Get(id)
			if gerr != nil {
				t.Fatal(gerr)
			}
			if got.State != from {
				t.Errorf("%s -> %q refused but the row moved to %q", from, to, got.State)
			}
		}
	}
	if refused != 4*6-len(allowedEdges) {
		t.Fatalf("checked %d refused edges", refused)
	}
	if !allowedEdges[[2]string{"public", "yanked"}] || allowedEdges[[2]string{"yanked", "public"}] {
		t.Fatal("the edge table lost its yanked->public refusal")
	}
	id := rowIn(t, s, 999, "staged")
	if err := s.Transition(id, "public", "yanked", epoch); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("a transition naming the wrong from-state: %v, want ErrBadState", err)
	}
	if err := s.Transition(12345, "staged", "public", epoch); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a transition on a missing row: %v, want ErrNotFound", err)
	}
}

func TestNewestOrdersByVersionNotInsertion(t *testing.T) {
	s := openStore(t, t.TempDir())
	newer := insert(t, s, stagedRow("umbree", "0.9.9", 1, epoch))
	insert(t, s, stagedRow("umbree", "0.3.26", 2, epoch.Add(time.Hour)))
	insert(t, s, stagedRow("umbree", "0.10.0", 3, epoch.Add(-time.Hour)))
	insert(t, s, stagedRow("umbreed", "1.0.0", 4, epoch))
	got, err := s.Newest("umbree", "production", "staged")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.10.0" {
		t.Fatalf("Newest = %s, want 0.10.0", got.Stamp)
	}
	list, err := s.List("umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, rv := range list {
		order = append(order, rv.Version)
	}
	want := []string{"0.10.0", "0.9.9", "0.3.26"}
	if len(order) != len(want) {
		t.Fatalf("List = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("List = %v, want %v", order, want)
		}
	}
	if err := s.Transition(list[0].ID, "staged", "expired", epoch); err != nil {
		t.Fatal(err)
	}
	got, err = s.Newest("umbree", "production", "staged")
	if err != nil || got.ID != newer {
		t.Fatalf("Newest staged after expiring 0.10.0 = %+v, %v; want row %d (0.9.9)", got, err, newer)
	}
	if _, err := s.Newest("umbree", "production", "public"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Newest public on a catalog with none: %v, want ErrNotFound", err)
	}
}

func TestNewestTieBreaksCreatedAtThenID(t *testing.T) {
	stamp := stampFor("0.1.8", 7)
	a := store.ReleaseVersion{ID: 1, Stamp: stamp, CreatedAt: epoch}
	b := store.ReleaseVersion{ID: 2, Stamp: stamp, CreatedAt: epoch.Add(time.Second)}
	c := store.ReleaseVersion{ID: 3, Stamp: stamp, CreatedAt: epoch.Add(time.Second)}
	higher := store.ReleaseVersion{ID: 0, Stamp: stampFor("0.1.9", 7), CreatedAt: epoch.Add(-time.Hour)}
	cases := []struct {
		name     string
		x, y     store.ReleaseVersion
		xIsNewer bool
	}{
		{"later created wins a tied stamp", b, a, true},
		{"earlier created loses a tied stamp", a, b, false},
		{"higher id wins a tied stamp and time", c, b, true},
		{"lower id loses a tied stamp and time", b, c, false},
		{"version beats created and id", higher, c, true},
		{"a row is not newer than itself", c, c, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := store.Newer(tc.x, tc.y); got != tc.xIsNewer {
				t.Fatalf("Newer(%d, %d) = %v, want %v", tc.x.ID, tc.y.ID, got, tc.xIsNewer)
			}
		})
	}
	rows := []store.ReleaseVersion{a, c, higher, b}
	store.SortNewestFirst(rows)
	var ids []int64
	for _, rv := range rows {
		ids = append(ids, rv.ID)
	}
	want := []int64{0, 3, 2, 1}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("SortNewestFirst ids = %v, want %v", ids, want)
		}
	}
}

package retention_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"

	"github.com/umbree-git/release/internal/manage/retention"
)

type mix struct {
	name   string
	setup  func(w *world)
	oldest string
}

var gatedMixes = []mix{
	{"four staged", func(w *world) {
		for _, v := range []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
			w.stage(v)
		}
	}, "staged"},
	{"oldest public and current, three staged", func(w *world) {
		w.live("0.1.1")
		for _, v := range []string{"0.1.2", "0.1.3", "0.1.4"} {
			w.stage(v)
		}
	}, "public"},
	{"oldest yanked, three newer", func(w *world) {
		w.live("0.1.1", "0.1.2")
		if err := w.st.MarkYanked(w.ids["0.1.1"], "test-operator", "defective", epoch); err != nil {
			w.t.Fatal(err)
		}
		w.stage("0.1.3")
		w.stage("0.1.4")
	}, "yanked"},
	{"oldest staged below three public", func(w *world) {
		w.stage("0.1.1")
		w.live("0.1.2", "0.1.3", "0.1.4")
	}, "staged"},
}

func TestGatedKeepsThreeNewestAnyState(t *testing.T) {
	for _, m := range gatedMixes {
		t.Run(m.name, func(t *testing.T) {
			w := newWorld(t)
			m.setup(w)
			w.retain(retention.Gated)
			w.wantGated(t, []string{"0.1.2", "0.1.3", "0.1.4"}, []string{"0.1.1"})
			old := w.row("0.1.1")
			if old.GatedPrunedAt.IsZero() {
				t.Fatalf("the oldest row's gated copy is not recorded pruned: %+v", old)
			}
			wantState := m.oldest
			if m.oldest == "staged" {
				wantState = "expired"
			}
			if old.State != wantState {
				t.Fatalf("the oldest row is %s, want %s", old.State, wantState)
			}
			for _, v := range []string{"0.1.2", "0.1.3", "0.1.4"} {
				if rv := w.row(v); !rv.GatedPrunedAt.IsZero() || rv.State == "expired" {
					t.Fatalf("%s inside the window was touched: %+v", v, rv)
				}
			}
		})
	}
}

func TestGatedStagedOutsideWindowExpires(t *testing.T) {
	w := newWorld(t)
	gatedMixes[0].setup(w)
	reps := w.retain(retention.Gated)
	old := w.row("0.1.1")
	if old.State != "expired" || old.ExpiredAt.IsZero() || !old.PromotedAt.IsZero() {
		t.Fatalf("the staged row outside the window is %+v, want expired with no promoted_at", old)
	}
	if len(reps) != 1 || len(reps[0].Expired) != 1 || reps[0].Expired[0] != stampOf("0.1.1") {
		t.Fatalf("report %+v does not name the one expired stamp", reps)
	}
	if !w.holds(w.gated, w.gatedKeys("0.1.2")) {
		t.Fatal("keep-control: the next row's gated bytes are gone")
	}
}

func TestGatedPublicOutsideWindowKeepsState(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2")
	w.stage("0.1.3")
	w.stage("0.1.4")
	w.retain(retention.Gated)
	old := w.row("0.1.1")
	if old.State != "public" || old.GatedPrunedAt.IsZero() || !old.PublicPrunedAt.IsZero() {
		t.Fatalf("the public row outside the gated window is %+v, want public with only gated_pruned_at set", old)
	}
	w.wantGated(t, nil, []string{"0.1.1"})
	w.wantPublic(t, []string{"0.1.1", "0.1.2"}, nil)
}

func TestGatedPrunesCurrentRowsGatedCopyOnly(t *testing.T) {
	w := newWorld(t)
	gatedMixes[1].setup(w)
	manifest, _ := w.public.Body("umbree/latest.json")
	w.retain(retention.Gated)
	cur := w.row("0.1.1")
	if cur.State != "public" || !cur.IsCurrent || cur.GatedPrunedAt.IsZero() {
		t.Fatalf("the current row is %+v, want public, current, gated copy pruned", cur)
	}
	w.wantGated(t, nil, []string{"0.1.1"})
	w.wantPublic(t, []string{"0.1.1"}, nil)
	if after, _ := w.public.Body("umbree/latest.json"); string(after) != string(manifest) {
		t.Fatal("the gated pass changed the public manifest")
	}
	if w.public.Count("DELETE") != 0 {
		t.Fatalf("the gated pass deleted %d public objects", w.public.Count("DELETE"))
	}
}

func TestPublicKeepsFiveWithCurrent(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	w.retain(retention.Public)
	w.wantPublic(t, []string{"0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7"}, []string{"0.1.1", "0.1.2"})
	for _, v := range []string{"0.1.1", "0.1.2"} {
		if rv := w.row(v); rv.State != "public" || rv.PublicPrunedAt.IsZero() {
			t.Fatalf("%s is %+v, want public with public_pruned_at set", v, rv)
		}
	}
	if w.gated.Count("DELETE") != 0 {
		t.Fatal("the public pass deleted gated objects")
	}
	if !w.row("0.1.7").IsCurrent {
		t.Fatal("the current row moved")
	}
}

func TestPublicCurrentNeverExpiredEvenOldest(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6")
	for _, v := range []string{"0.1.6", "0.1.5", "0.1.4", "0.1.3", "0.1.2"} {
		if err := publishYank(w, w.ids[v]); err != nil {
			t.Fatalf("yank %s: %v", v, err)
		}
	}
	if !w.row("0.1.1").IsCurrent {
		t.Fatal("setup: 0.1.1 is not current after the yanks")
	}
	for range 2 {
		w.retain(retention.Gated, retention.Public)
	}
	cur := w.row("0.1.1")
	if cur.State != "public" || !cur.IsCurrent || !cur.PublicPrunedAt.IsZero() {
		t.Fatalf("the oldest, current row is %+v, want public, current, public bytes kept", cur)
	}
	w.wantPublic(t, []string{"0.1.1"}, []string{"0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6"})
}

func TestPublicPinKeptOutsideWindow(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	if _, err := w.st.SetPermanent("umbree", "production", stampOf("0.1.1"), true, "test-operator", epoch); err != nil {
		t.Fatal(err)
	}
	w.retain(retention.Gated, retention.Public)
	w.wantPublic(t, []string{"0.1.1"}, []string{"0.1.2"})
	if rv := w.row("0.1.1"); rv.State != "public" || !rv.PublicPrunedAt.IsZero() {
		t.Fatalf("the pinned row is %+v, want public with its public bytes", rv)
	}
	if rv := w.row("0.1.2"); rv.State != "expired" {
		t.Fatalf("keep-control: the unpinned row outside both windows is %s, want expired", rv.State)
	}
}

func TestExpiredOnlyWhenBothCopiesGone(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	w.retain(retention.Public)
	if rv := w.row("0.1.1"); rv.State != "public" {
		t.Fatalf("public bytes gone but gated kept: 0.1.1 is %s, want public", rv.State)
	}
	w.retain(retention.Gated)
	for _, v := range []string{"0.1.1", "0.1.2"} {
		if rv := w.row(v); rv.State != "expired" || rv.ExpiredAt.IsZero() {
			t.Fatalf("%s lost both copies and is %+v, want expired", v, rv)
		}
	}
	for _, v := range []string{"0.1.3", "0.1.4"} {
		if rv := w.row(v); rv.State != "public" || rv.GatedPrunedAt.IsZero() || !rv.PublicPrunedAt.IsZero() {
			t.Fatalf("%s lost only its gated copy and is %+v, want public", v, rv)
		}
	}
}

func TestExpiringNeverClearsPromotedAt(t *testing.T) {
	w := newWorld(t)
	w.stage("0.1.0")
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	if err := w.st.MarkYanked(w.ids["0.1.2"], "test-operator", "defective", epoch); err != nil {
		t.Fatal(err)
	}
	before := map[string]int64{"0.1.1": w.row("0.1.1").PromotedAt.Unix(), "0.1.2": w.row("0.1.2").PromotedAt.Unix()}
	w.retain(retention.Gated, retention.Public)
	for v, was := range before {
		rv := w.row(v)
		if rv.State != "expired" {
			t.Fatalf("%s is %s, want expired", v, rv.State)
		}
		if rv.PromotedAt.IsZero() || rv.PromotedAt.Unix() != was {
			t.Fatalf("%s: promoted_at %v after expiring, want %d", v, rv.PromotedAt, was)
		}
	}
	if rv := w.row("0.1.2"); rv.YankedAt.IsZero() {
		t.Fatal("yanked -> expired cleared yanked_at")
	}
	if rv := w.row("0.1.0"); rv.State != "expired" || !rv.PromotedAt.IsZero() {
		t.Fatalf("keep-control: the staged row expired as %+v, want expired with promoted_at still zero", rv)
	}
	mark, err := w.st.HighWaterMark("umbree", "production")
	if err != nil || mark.Stamp != stampOf("0.1.7") {
		t.Fatalf("high-water mark %v, %v; want 0.1.7", mark, err)
	}
}

func TestYankedMarkPrunedKeepsPromotedAt(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	if err := publishYank(w, w.ids["0.1.7"]); err != nil {
		t.Fatal(err)
	}
	promoted := w.row("0.1.7").PromotedAt
	w.retain(retention.Gated, retention.Public)
	mark := w.row("0.1.7")
	if mark.State != "yanked" || mark.PublicPrunedAt.IsZero() || !mark.PromotedAt.Equal(promoted) {
		t.Fatalf("the yanked high-water mark is %+v, want yanked, public bytes pruned, promoted_at unchanged", mark)
	}
	w.wantPublic(t, []string{"0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6"}, []string{"0.1.1", "0.1.7"})
	hw, err := w.st.HighWaterMark("umbree", "production")
	if err != nil || hw.Stamp != stampOf("0.1.7") {
		t.Fatalf("HighWaterMark = %v, %v after its bytes were pruned; want 0.1.7", hw, err)
	}
}

func TestYankedRowsTakeNoPublicSlot(t *testing.T) {
	w := newWorld(t)
	all := []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7", "0.1.8", "0.1.9", "0.1.10"}
	w.live(all...)
	yanked := []string{"0.1.10", "0.1.9", "0.1.8", "0.1.7", "0.1.6"}
	for _, v := range yanked {
		if err := publishYank(w, w.ids[v]); err != nil {
			t.Fatalf("yank %s: %v", v, err)
		}
	}
	p := w.plan(retention.Public)
	for _, v := range yanked {
		for _, k := range w.publicKeys(v) {
			if !slices.Contains(p.Keys(), k) {
				t.Fatalf("yanked %s: %s is not in the prune plan", v, k)
			}
		}
	}
	w.retain(retention.Public)
	w.wantPublic(t, []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5"}, yanked)
	if !w.row("0.1.5").IsCurrent {
		t.Fatal("setup: 0.1.5 is not current")
	}
	if err := publishYank(w, w.ids["0.1.5"]); err != nil {
		t.Fatalf("a further yank found no successor: %v", err)
	}
	if !w.row("0.1.4").IsCurrent {
		t.Fatal("the further yank did not re-point to 0.1.4")
	}
}

func TestYankedPinnedOrNamedKeepBytes(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5")
	for _, v := range []string{"0.1.5", "0.1.4", "0.1.3"} {
		if err := publishYank(w, w.ids[v]); err != nil {
			t.Fatalf("yank %s: %v", v, err)
		}
	}
	if _, err := w.st.SetPermanent("umbree", "production", stampOf("0.1.5"), true, "test-operator", epoch); err != nil {
		t.Fatal(err)
	}
	w.public.Seed("umbree/latest.json", []byte(`{"stamp":"`+stampOf("0.1.4")+`"}`))
	w.retain(retention.Public)
	w.wantPublic(t, []string{"0.1.5", "0.1.4", "0.1.2", "0.1.1"}, []string{"0.1.3"})
}

func TestPublicKeepsManifestStamp(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	w.public.Seed("umbree/latest.json", []byte(`{"stamp":"`+stampOf("0.1.1")+`"}`))
	p := w.plan(retention.Public)
	for _, k := range p.Keys() {
		for _, named := range w.publicKeys("0.1.1") {
			if k == named {
				t.Fatalf("the plan deletes %s, which latest.json names", k)
			}
		}
	}
	w.retain(retention.Public)
	w.wantPublic(t, []string{"0.1.1"}, []string{"0.1.2"})
	w.public.Seed("umbree/latest.json", []byte(`not json`))
	if _, err := w.r.Plan(t.Context(), "umbree", "production", retention.Public); err == nil {
		t.Fatal("an unreadable latest.json still produced a public plan")
	}
}

func TestFailedDeleteMidRowAuditedAndUnlinked(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6")
	keys := w.publicKeys("0.1.1")
	slices.Sort(keys)
	w.public.FailOn("DELETE", keys[1], errors.New("bucket unavailable"))
	if _, err := w.r.Retain(context.Background(), "umbree", "production", "test-operator", retention.Public); err == nil {
		t.Fatal("a failed DELETE was not reported")
	}
	rv := w.row("0.1.1")
	if rv.PublicPruningAt.IsZero() || !rv.PublicPrunedAt.IsZero() || rv.State != "public" {
		t.Fatalf("a part-pruned row is %+v, want marked pruning, not pruned, still public", rv)
	}
	if _, ok := w.public.Body(keys[0]); ok {
		t.Fatalf("setup: %s was not deleted before the failure", keys[0])
	}
	log, err := w.st.AuditLog()
	if err != nil {
		t.Fatal(err)
	}
	last := log[len(log)-1]
	if last.Action != "prune-public-partial" || last.Actor != "test-operator" || last.RowID != rv.ID ||
		!strings.Contains(last.Detail, keys[0]) || !strings.Contains(last.Detail, "bucket unavailable") {
		t.Fatalf("audit %+v does not name the deleted key, the actor and the failure", last)
	}
	w.public.FailOn("DELETE", keys[1], nil)
	w.retain(retention.Public)
	if rv := w.row("0.1.1"); rv.PublicPrunedAt.IsZero() {
		t.Fatalf("the retry did not finish the row: %+v", rv)
	}
	w.wantPublic(t, nil, []string{"0.1.1"})
}

func TestManifestSwitchedMidPassKeepsNamedRow(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	switched := false
	w.public.OnCall = func(c backendtest.Call) {
		if c.Op == "DELETE" && !switched {
			switched = true
			w.public.Seed("umbree/latest.json", []byte(`{"stamp":"`+stampOf("0.1.1")+`"}`))
		}
	}
	reps, err := w.r.Retain(context.Background(), "umbree", "production", "test-operator", retention.Public)
	if err != nil {
		t.Fatal(err)
	}
	if !switched {
		t.Fatal("setup: no DELETE ran")
	}
	w.wantPublic(t, []string{"0.1.1"}, []string{"0.1.2"})
	if rv := w.row("0.1.1"); !rv.PublicPrunedAt.IsZero() || !rv.PublicPruningAt.IsZero() {
		t.Fatalf("the row the manifest names was touched: %+v", rv)
	}
	if !slices.ContainsFunc(reps[0].Skipped, func(s retention.Skip) bool { return s.Stamp == stampOf("0.1.1") }) {
		t.Fatalf("the kept row is not reported: %+v", reps[0].Skipped)
	}
}

func TestPinRefusedWhilePublicPruning(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2")
	if err := w.st.MarkPublicPruning(w.ids["0.1.1"], epoch); err != nil {
		t.Fatal(err)
	}
	_, err := w.st.SetPermanent("umbree", "production", stampOf("0.1.1"), true, "test-operator", epoch)
	if err == nil || !strings.Contains(err.Error(), "public bytes are being pruned") {
		t.Fatalf("pinning a part-pruned row: %v, want a refusal naming the state", err)
	}
	if rv := w.row("0.1.1"); rv.Permanent {
		t.Fatal("the refused pin set the row permanent")
	}
	if _, err := w.st.SetPermanent("umbree", "production", stampOf("0.1.2"), true, "test-operator", epoch); err != nil {
		t.Fatalf("keep-control, an intact row: %v", err)
	}
}

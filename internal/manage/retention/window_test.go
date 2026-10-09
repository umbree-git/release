package retention_test

import (
	"testing"

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
	w.wantPublic(t, []string{"0.1.1", "0.1.3", "0.1.4", "0.1.5", "0.1.6"}, []string{"0.1.2"})
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

func TestPublicKeepsHighWaterMarkBytes(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	if err := publishYank(w, w.ids["0.1.7"]); err != nil {
		t.Fatal(err)
	}
	w.retain(retention.Gated, retention.Public)
	mark := w.row("0.1.7")
	if mark.State != "yanked" || !mark.PublicPrunedAt.IsZero() {
		t.Fatalf("the high-water mark is %+v, want yanked with its public bytes", mark)
	}
	w.wantPublic(t, []string{"0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7"}, []string{"0.1.1", "0.1.2"})
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

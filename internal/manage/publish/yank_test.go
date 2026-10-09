package publish_test

import (
	"bytes"
	"context"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/publish"
)

func (w *world) yank(id int64) (error, []publish.Event) {
	w.t.Helper()
	var buf bytes.Buffer
	err := publish.Yank(context.Background(), w.d, id, "test-operator", &buf)
	return err, events(w.t, buf.Bytes())
}

func TestYankRepointsToNewestWithBytes(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	mid := w.live("0.2.0", 2)
	top := w.live("0.3.0", 3)
	err, ev := w.yank(top)
	if err != nil || terminal(t, ev).Step != "done" {
		t.Fatalf("yank: %v %+v", err, ev)
	}
	if w.manifestStamp() != w.row(mid).Stamp {
		t.Fatalf("manifest names %s, want %s", w.manifestStamp(), w.row(mid).Stamp)
	}
	if r := w.row(top); r.State != "yanked" || r.IsCurrent || r.YankedAt.IsZero() {
		t.Fatalf("yanked row %+v", r)
	}
	if !w.row(mid).IsCurrent {
		t.Fatal("the successor is not current")
	}
}

func TestYankSkipsRowWithPrunedPublicBytes(t *testing.T) {
	w := newWorld(t)
	low := w.live("0.1.0", 1)
	mid := w.live("0.2.0", 2)
	top := w.live("0.3.0", 3)
	w.public.Remove("umbree/" + w.row(mid).Stamp + "/umbree-linux-amd64.zip")
	if err, ev := w.yank(top); err != nil {
		t.Fatalf("yank: %v %+v", err, ev)
	}
	if w.manifestStamp() != w.row(low).Stamp || !w.row(low).IsCurrent || w.row(mid).IsCurrent {
		t.Fatalf("manifest names %s; want the newest row whose bytes are all present (%s)", w.manifestStamp(), w.row(low).Stamp)
	}
}

func TestYankRefusesWithoutSuccessor(t *testing.T) {
	w := newWorld(t)
	only := w.live("0.1.0", 1)
	before, _ := w.public.Body("umbree/latest.json")
	err, ev := w.yank(only)
	wantRefused(t, err, ev, "no public row with bytes")
	after, ok := w.public.Body("umbree/latest.json")
	if !ok || string(after) != string(before) {
		t.Fatal("a refused yank changed or removed the manifest")
	}
	if r := w.row(only); r.State != "public" || !r.IsCurrent {
		t.Fatalf("a refused yank moved the row: %+v", r)
	}
	if w.public.Count("DELETE") != 0 {
		t.Fatal("a refused yank deleted something")
	}
}

func TestYankIssuesNoArtifactDelete(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	top := w.live("0.2.0", 2)
	if err, ev := w.yank(top); err != nil {
		t.Fatalf("yank: %v %+v", err, ev)
	}
	if n := w.public.Count("DELETE") + w.gated.Count("DELETE"); n != 0 {
		t.Fatalf("yank issued %d DELETEs", n)
	}
}

func TestYankRefusesNotCurrent(t *testing.T) {
	w := newWorld(t)
	old := w.live("0.1.0", 1)
	w.live("0.2.0", 2)
	err, ev := w.yank(old)
	wantRefused(t, err, ev, "not the current")
	staged, _ := w.stage("0.3.0", 3)
	err, ev = w.yank(staged)
	wantRefused(t, err, ev, "not the current")
}

func TestYankArtifactsStillListable(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	top := w.live("0.2.0", 2)
	stamp := w.row(top).Stamp
	before, _ := w.public.List(context.Background(), "umbree/"+stamp+"/")
	if err, ev := w.yank(top); err != nil {
		t.Fatalf("yank: %v %+v", err, ev)
	}
	after, _ := w.public.List(context.Background(), "umbree/"+stamp+"/")
	if len(before) != 4 || !reflect.DeepEqual(before, after) {
		t.Fatalf("listing before %v, after %v", before, after)
	}
	for _, k := range after {
		if !strings.HasPrefix(path.Base(k), "umbree-") && !strings.HasPrefix(path.Base(k), "SHA256SUMS") {
			t.Fatalf("unexpected key %s", k)
		}
	}
}

func TestNoCodePathDeletesManifest(t *testing.T) {
	if _, has := reflect.TypeOf((*backend.Public)(nil)).Elem().MethodByName("Delete"); has {
		t.Fatal("backend.Public grew a Delete method; no publish path may delete a public object")
	}
	if _, has := reflect.TypeOf((*backend.Gated)(nil)).Elem().MethodByName("Delete"); has {
		t.Fatal("backend.Gated grew a Delete method")
	}
	w := newWorld(t)
	w.live("0.1.0", 1)
	top := w.live("0.2.0", 2)
	_, _ = w.yank(top)
	_, _ = w.yank(w.live("0.3.0", 3))
	only := newWorld(t)
	_, _ = only.yank(only.live("0.1.0", 1))
	for _, s := range []interface{ Count(string) int }{w.public, w.gated, only.public, only.gated} {
		if s.Count("DELETE") != 0 {
			t.Fatal("a publish path issued a DELETE")
		}
	}
}

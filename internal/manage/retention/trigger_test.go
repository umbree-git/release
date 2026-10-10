package retention_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
)

func retentionEvents(ev []publish.Event) []publish.Event {
	var out []publish.Event
	for _, e := range ev {
		if e.Step == "retention" {
			out = append(out, e)
		}
	}
	return out
}

func TestPromoteRunsBothPasses(t *testing.T) {
	w := newWorld(t)
	w.d.AfterPromote = w.r.AfterPromote
	var last []publish.Event
	for _, v := range []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6"} {
		last = w.promote(w.stage(v))
	}
	w.wantGated(t, []string{"0.1.4", "0.1.5", "0.1.6"}, []string{"0.1.1", "0.1.2", "0.1.3"})
	w.wantPublic(t, []string{"0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6"}, []string{"0.1.1"})
	if rv := w.row("0.1.1"); rv.State != "expired" {
		t.Fatalf("0.1.1 is outside both windows after the sixth promote and is %s, want expired", rv.State)
	}
	got := retentionEvents(last)
	if len(got) != 1 || got[0].Status != "ok" || !strings.Contains(got[0].Message, "gated") || !strings.Contains(got[0].Message, "public") {
		t.Fatalf("the promote streamed %+v, want one ok retention event naming both passes", got)
	}
	if last[len(last)-1].Step != "done" {
		t.Fatalf("the terminal event is %+v, want done", last[len(last)-1])
	}
}

func TestPromoteRetentionFailureStillDone(t *testing.T) {
	w := newWorld(t)
	w.d.AfterPromote = w.r.AfterPromote
	for _, v := range []string{"0.1.1", "0.1.2", "0.1.3"} {
		w.promote(w.stage(v))
	}
	stuck := w.gatedKeys("0.1.1")[0]
	w.gated.FailOn("DELETE", stuck, errors.New("bucket unavailable"))
	id := w.stage("0.1.4")
	var buf bytes.Buffer
	err := publish.Promote(context.Background(), w.d, id, "test-operator", &buf)
	ev := events(t, buf.Bytes())
	if err != nil || ev[len(ev)-1].Step != "done" {
		t.Fatalf("a retention failure failed the promote: %v %+v", err, ev)
	}
	got := retentionEvents(ev)
	if len(got) != 1 || got[0].Status != "error" || !strings.Contains(got[0].Message, "bucket unavailable") {
		t.Fatalf("the promote streamed %+v, want one retention error naming the failure", got)
	}
	if rv := w.row("0.1.4"); rv.State != "public" || !rv.IsCurrent {
		t.Fatalf("the promoted row is %+v, want public and current", rv)
	}
	if rv := w.row("0.1.1"); !rv.GatedPrunedAt.IsZero() {
		t.Fatal("a row whose delete failed was recorded pruned")
	}
	if _, ok := w.gated.Body(stuck); !ok {
		t.Fatal("setup: the failing key is gone")
	}
}

func TestRetainAllSweepsEveryComponent(t *testing.T) {
	w := newWorld(t)
	for _, v := range []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
		w.stage(v)
	}
	reps, err := w.r.RetainAll(context.Background(), retention.ActorNightly)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range reps {
		seen[r.Component+" "+string(r.Window)] = true
	}
	for _, want := range []string{"umbree gated", "umbree public", "umbreed gated", "umbreed public"} {
		if !seen[want] {
			t.Fatalf("RetainAll reported %v, missing %s", seen, want)
		}
	}
	if rv := w.row("0.1.1"); rv.State != "expired" {
		t.Fatalf("the nightly net left 0.1.1 %s", rv.State)
	}
}

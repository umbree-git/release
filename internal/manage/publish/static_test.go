package publish_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/static"
)

const staticBase = "https://downloads.example.test"

func (w *world) withStatic() string {
	w.t.Helper()
	dir := w.t.TempDir()
	w.d.Static = &static.Publisher{Assets: release.Assets, Source: w.public, DownloadsBase: staticBase, Dest: static.Dest{Dir: dir}}
	return dir
}

func servedFloor(t *testing.T, dir, component string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, component, "install.sh"))
	if err != nil {
		t.Fatalf("no served bootstrap: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if v, ok := strings.CutPrefix(line, "MIN_VERSION="); ok {
			return strings.Trim(v, `"`)
		}
	}
	t.Fatal("the served bootstrap bakes no floor")
	return ""
}

func staticEvent(t *testing.T, ev []publish.Event) publish.Event {
	t.Helper()
	var found []publish.Event
	for _, e := range ev {
		if e.Step == "static" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d static events, want 1: %+v", len(found), ev)
	}
	return found[0]
}

type failingStatic struct{ calls int }

func (f *failingStatic) Publish(context.Context, string) (string, error) {
	f.calls++
	return "", errors.New("scp: Permission denied (publickey)")
}

func TestPromoteEndRepublishesStatic(t *testing.T) {
	w := newWorld(t)
	dir := w.withStatic()
	id, _ := w.stage("0.2.0", 2)
	err, ev := w.promote(id)
	if err != nil || terminal(t, ev).Step != "done" {
		t.Fatalf("promote: %v %+v", err, ev)
	}
	stamp := w.row(id).Stamp
	if e := staticEvent(t, ev); e.Status != "ok" || !strings.Contains(e.Message, stamp) {
		t.Fatalf("static event %+v", e)
	}
	if got := servedFloor(t, dir, "umbree"); got != stamp {
		t.Fatalf("the served floor is %q, want the promoted %q", got, stamp)
	}
	js, err := os.ReadFile(filepath.Join(dir, "umbree", "version.js"))
	if err != nil || !bytes.Contains(js, []byte(`"version":"0.2.0","stamp":"`+stamp+`"`)) {
		t.Fatalf("version.js %q, %v", js, err)
	}
	for _, f := range []string{"umbree-release.pub", "index.html"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not published: %v", f, err)
		}
	}
	for i, e := range ev {
		if e.Step == "static" && (i == 0 || ev[i-1].Step != "retention" && ev[i-1].Step != "flip") {
			t.Fatalf("the static step does not follow the flip: %+v", ev)
		}
	}
}

func TestStaticFloorFromManifest(t *testing.T) {
	w := newWorld(t)
	dir := w.withStatic()
	w.live("0.1.0", 1)
	w.stage("0.3.0", 3)
	id, _ := w.stage("0.2.0", 2)
	if err, ev := w.promote(id); err != nil {
		t.Fatalf("promote: %v %+v", err, ev)
	}
	if got, want := servedFloor(t, dir, "umbree"), w.row(id).Stamp; got != want {
		t.Fatalf("the served floor is %q, want the manifest's %q and never the newer staged cut", got, want)
	}
	if w.manifestStamp() != w.row(id).Stamp {
		t.Fatalf("fixture: the manifest names %q", w.manifestStamp())
	}
}

func TestStaticFailureDoesNotFailPromote(t *testing.T) {
	w := newWorld(t)
	f := &failingStatic{}
	w.d.Static = f
	id, _ := w.stage("0.2.0", 2)
	err, ev := w.promote(id)
	if err != nil || terminal(t, ev).Step != "done" {
		t.Fatalf("a failed static step failed the promote: %v %+v", err, ev)
	}
	e := staticEvent(t, ev)
	if e.Status != "error" || !strings.Contains(e.Message, "Permission denied") || !strings.Contains(e.Message, "publish-static umbree") {
		t.Fatalf("static event %+v does not stream the failure and the by-hand verb", e)
	}
	if r := w.row(id); r.State != "public" || !r.IsCurrent || w.manifestStamp() != r.Stamp {
		t.Fatalf("the promote did not complete: %+v manifest %q", r, w.manifestStamp())
	}
	if f.calls != 1 {
		t.Fatalf("static ran %d times", f.calls)
	}
}

func TestYankRepublishesStatic(t *testing.T) {
	w := newWorld(t)
	dir := w.withStatic()
	prev := w.live("0.1.0", 1)
	cur := w.live("0.2.0", 2)
	var buf bytes.Buffer
	if err := publish.Yank(context.Background(), w.d, cur, "test-operator", &buf); err != nil {
		t.Fatalf("yank: %v %s", err, buf.String())
	}
	ev := events(t, buf.Bytes())
	if e := staticEvent(t, ev); e.Status != "ok" {
		t.Fatalf("static event %+v", e)
	}
	if got, want := servedFloor(t, dir, "umbree"), w.row(prev).Stamp; got != want {
		t.Fatalf("after the yank the served floor is %q, want the successor's %q", got, want)
	}
}

func TestRepublishStaticTakesTheLock(t *testing.T) {
	w := newWorld(t)
	dir := w.withStatic()
	id := w.live("0.2.0", 2)
	w.d.Locks = publish.NewLocks(20 * time.Millisecond)
	release, err := w.d.Locks.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publish.RepublishStatic(context.Background(), w.d, "umbree", "production", "ops"); !errors.Is(err, publish.ErrBusy) {
		t.Fatalf("a republish while a promote holds the channel: %v, want ErrBusy", err)
	}
	release()
	if err := os.RemoveAll(filepath.Join(dir, "umbree")); err != nil {
		t.Fatal(err)
	}
	summary, err := publish.RepublishStatic(context.Background(), w.d, "umbree", "production", "ops")
	if err != nil || !strings.Contains(summary, w.row(id).Stamp) {
		t.Fatalf("republish: %q %v", summary, err)
	}
	if got := servedFloor(t, dir, "umbree"); got != w.row(id).Stamp {
		t.Fatalf("republished floor %q", got)
	}
	for _, bad := range [][3]string{{"umbree", "production", ""}, {"umbree", "beta", "ops"}, {"nosuch", "production", "ops"}} {
		if _, err := publish.RepublishStatic(context.Background(), w.d, bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("RepublishStatic%q accepted", bad)
		}
	}
	w.d.Static = nil
	if _, err := publish.RepublishStatic(context.Background(), w.d, "umbree", "production", "ops"); !errors.Is(err, publish.ErrNoStatic) {
		t.Fatalf("no publisher: %v, want ErrNoStatic", err)
	}
}

package publish_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/publish"
)

type outcome struct {
	err error
	buf bytes.Buffer
}

func promoteAsync(w *world, id int64) chan *outcome {
	done := make(chan *outcome, 1)
	go func() {
		o := &outcome{}
		o.err = publish.Promote(context.Background(), w.d, id, "test-operator", &o.buf)
		done <- o
	}()
	return done
}

func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestPromoteConcurrentSecondSeesFirst(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	inCopy, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	w.public.OnCall = func(c backendtest.Call) {
		if c.Op == "COPY" {
			once.Do(func() { close(inCopy); <-release })
		}
	}
	waiting := make(chan struct{})
	w.d.Locks.OnWait = func(component, channel string) { close(waiting) }
	first := promoteAsync(w, id)
	within(t, inCopy, "the first promote to start copying")
	second := promoteAsync(w, id)
	within(t, waiting, "the second promote to wait on the lock")
	close(release)
	a, b := <-first, <-second
	if a.err != nil {
		t.Fatalf("first: %v", a.err)
	}
	wantRefused(t, b.err, events(t, b.buf.Bytes()), "not staged")
	if n := w.public.Count("COPY"); n != len(arts) {
		t.Fatalf("%d copies, want exactly one promote's %d", n, len(arts))
	}
}

func TestPromoteBusyAfterBound(t *testing.T) {
	w := newWorld(t)
	id, _ := w.stage("0.1.0", 1)
	fire := make(chan time.Time, 1)
	w.d.Locks.After = func(time.Duration) <-chan time.Time { return fire }
	release, err := w.d.Locks.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fire <- time.Now()
	err, ev := w.promote(id)
	if !errors.Is(err, publish.ErrBusy) {
		t.Fatalf("a promote past the lock bound: %v", err)
	}
	terminal(t, ev)
}

func TestPromoteComponentsOverlap(t *testing.T) {
	w := newWorld(t)
	a, _ := w.stageAs("umbree", "0.1.0", 1, "")
	b, _ := w.stageAs("umbreed", "0.1.0", 2, "")
	inCopy, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	w.public.OnCall = func(c backendtest.Call) {
		if c.Op == "COPY" && c.Key[:7] == "umbree/" {
			once.Do(func() { close(inCopy); <-release })
		}
	}
	w.d.Locks.OnWait = func(component, channel string) { t.Errorf("waited on %s/%s", component, channel) }
	first := promoteAsync(w, a)
	within(t, inCopy, "the umbree promote to start copying")
	if err, ev := w.promote(b); err != nil {
		t.Fatalf("umbreed while umbree holds its lock: %v %+v", err, ev)
	}
	close(release)
	if o := <-first; o.err != nil {
		t.Fatalf("umbree: %v", o.err)
	}
}

func TestPromoteStreamTerminalEvent(t *testing.T) {
	scenarios := map[string]func(w *world) int64{
		"done":        func(w *world) int64 { id, _ := w.stage("0.1.0", 1); return id },
		"not staged":  func(w *world) int64 { return w.live("0.1.0", 1) },
		"unknown row": func(w *world) int64 { return 4242 },
		"not newer":   func(w *world) int64 { w.live("0.2.0", 1); id, _ := w.stage("0.1.0", 2); return id },
		"size":        func(w *world) int64 { id, a := w.stage("0.1.0", 1); w.gated.ReportSize(a[3].Key, 1); return id },
		"manifest PUT": func(w *world) int64 {
			id, _ := w.stage("0.1.0", 1)
			w.public.FailOn("PUT", "umbree/latest.json", errors.New("x"))
			return id
		},
	}
	for name, setup := range scenarios {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			id := setup(w)
			err, ev := w.promote(id)
			last := terminal(t, ev)
			if (err == nil) != (last.Step == "done") {
				t.Fatalf("error %v but terminal %+v", err, last)
			}
		})
	}
}

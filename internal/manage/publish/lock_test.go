package publish_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/publish"
)

func TestLockSerialisesSameChannel(t *testing.T) {
	l := publish.NewLocks(5 * time.Second)
	waiting := make(chan struct{}, 1)
	l.OnWait = func(component, channel string) { waiting <- struct{}{} }
	release, err := l.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		r, err := l.Acquire(context.Background(), "umbree", "production")
		if err == nil {
			r()
		}
		got <- err
	}()
	awaitWait(t, waiting)
	select {
	case err := <-got:
		t.Fatalf("the second holder got the lock while the first held it: %v", err)
	default:
	}
	release()
	if err := <-got; err != nil {
		t.Fatalf("second acquire after release: %v", err)
	}
}

func TestLockBoundedWait409(t *testing.T) {
	l := publish.NewLocks(5 * time.Second)
	fire := make(chan time.Time)
	var asked time.Duration
	l.After = func(d time.Duration) <-chan time.Time { asked = d; return fire }
	waiting := make(chan struct{}, 1)
	l.OnWait = func(component, channel string) { waiting <- struct{}{} }
	release, err := l.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	got := make(chan error, 1)
	go func() {
		_, err := l.Acquire(context.Background(), "umbree", "production")
		got <- err
	}()
	awaitWait(t, waiting)
	fire <- time.Now()
	err = <-got
	if !errors.Is(err, publish.ErrBusy) {
		t.Fatalf("a wait past the bound: %v, want ErrBusy", err)
	}
	if asked != 5*time.Second {
		t.Fatalf("waited on %v, want the 5s bound", asked)
	}
}

func TestLockIndependentAcrossComponents(t *testing.T) {
	l := publish.NewLocks(5 * time.Second)
	l.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }
	l.OnWait = func(component, channel string) { t.Errorf("waited on %s/%s", component, channel) }
	a, err := l.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := l.Acquire(context.Background(), "umbreed", "production")
	if err != nil {
		t.Fatal(err)
	}
	b()
}

func awaitWait(t *testing.T, waiting <-chan struct{}) {
	t.Helper()
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("the second acquire never waited on the held lock")
	}
}

package publish_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/publish"
)

func sharedLocks(t *testing.T, dir string) *publish.Locks {
	t.Helper()
	l, err := publish.NewSharedLocks(5*time.Second, dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSharedLocksSerialiseAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	serve, retain := sharedLocks(t, dir), sharedLocks(t, dir)
	fire := make(chan time.Time)
	retain.After = func(time.Duration) <-chan time.Time { return fire }
	waiting := make(chan struct{}, 1)
	retain.OnWait = func(component, channel string) { waiting <- struct{}{} }
	release, err := serve.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan error, 1)
	go func() {
		r, err := retain.Acquire(context.Background(), "umbree", "production")
		if err == nil {
			r()
		}
		got <- err
	}()
	awaitWait(t, waiting)
	select {
	case err := <-got:
		t.Fatalf("a second instance on the same data dir got the lock while the first held it: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	if err := <-got; err != nil {
		t.Fatalf("second instance after release: %v", err)
	}
	other, err := sharedLocks(t, dir).Acquire(context.Background(), "umbreed", "production")
	if err != nil {
		t.Fatalf("keep-control: another component was blocked: %v", err)
	}
	other()
}

func TestSharedLocksBoundedWaitAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	a, b := sharedLocks(t, dir), sharedLocks(t, dir)
	fire := make(chan time.Time, 1)
	b.After = func(time.Duration) <-chan time.Time { return fire }
	release, err := a.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fire <- time.Now()
	if _, err := b.Acquire(context.Background(), "umbree", "production"); !errors.Is(err, publish.ErrBusy) {
		t.Fatalf("a held file lock past the bound: %v, want ErrBusy", err)
	}
}

func TestSharedLockFileIsSafe(t *testing.T) {
	dir := t.TempDir()
	r, err := sharedLocks(t, dir).Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	r()
	info, err := os.Lstat(filepath.Join(dir, "lock.umbree.production"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file %v, %v; want a regular 0600 file", info, err)
	}
	evil := t.TempDir()
	target := filepath.Join(evil, "elsewhere")
	if err := os.Symlink(target, filepath.Join(evil, "lock.umbree.production")); err != nil {
		t.Fatal(err)
	}
	if _, err := sharedLocks(t, evil).Acquire(context.Background(), "umbree", "production"); err == nil {
		t.Fatal("a symlinked lock file was followed")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("the symlink's target was created")
	}
	for _, bad := range [][2]string{{"../x", "production"}, {"umbree", "a/b"}, {"", "production"}} {
		if _, err := sharedLocks(t, dir).Acquire(context.Background(), bad[0], bad[1]); err == nil {
			t.Fatalf("lock name %q/%q was accepted", bad[0], bad[1])
		}
	}
	if _, err := publish.NewSharedLocks(time.Second, ""); err == nil {
		t.Fatal("shared locks with no data dir were accepted")
	}
}

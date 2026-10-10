package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

func TestNonceSingleUse(t *testing.T) {
	s := openStore(t, t.TempDir())
	if err := s.IssueNonce("n1", epoch, epoch.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeNonce("n1", epoch.Add(time.Second)); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := s.ConsumeNonce("n1", epoch.Add(2*time.Second)); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("second use: %v, want ErrBadState", err)
	}
	if err := s.ConsumeNonce("never-issued", epoch); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown nonce: %v, want ErrNotFound", err)
	}
	if err := s.ConsumeNonce("", epoch); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty nonce: %v, want ErrNotFound", err)
	}
	if err := s.IssueNonce("n1", epoch, epoch.Add(time.Minute)); err == nil {
		t.Fatal("a nonce was issued twice")
	}
}

func TestNonceExpires(t *testing.T) {
	s := openStore(t, t.TempDir())
	expires := epoch.Add(5 * time.Minute)
	if err := s.IssueNonce("late", epoch, expires); err != nil {
		t.Fatal(err)
	}
	if err := s.IssueNonce("edge", epoch, expires); err != nil {
		t.Fatal(err)
	}
	if err := s.IssueNonce("fresh", epoch, expires); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeNonce("late", expires.Add(time.Second)); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("past expiry: %v, want ErrBadState", err)
	}
	if err := s.ConsumeNonce("edge", expires); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("at expiry: %v, want ErrBadState", err)
	}
	if err := s.ConsumeNonce("fresh", expires.Add(-time.Second)); err != nil {
		t.Fatalf("one second before expiry: %v", err)
	}
}

func TestNonceConsumedBeforeInsert(t *testing.T) {
	s := openStore(t, t.TempDir())
	rv := stagedRow("umbree", "0.1.8", 1, epoch)
	insert(t, s, rv)
	if err := s.IssueNonce("n-dup", epoch, epoch.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.ConsumeNonce("n-dup", epoch.Add(time.Second)); err != nil {
		t.Fatalf("consume: %v", err)
	}
	if _, err := s.InsertStaged(rv); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("duplicate insert: %v, want ErrDuplicate", err)
	}
	if err := s.ConsumeNonce("n-dup", epoch.Add(2*time.Second)); !errors.Is(err, store.ErrBadState) {
		t.Fatalf("the nonce of a failed insert was not burned: %v", err)
	}
}

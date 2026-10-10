package auth

import (
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

const boundPassword = "correct horse battery"

func boundService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "totp.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, err := LoadSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s := New(st, sealer, func() time.Time { return now }, nil)
	if _, err := s.AddAdmin("ops", boundPassword); err != nil {
		t.Fatal(err)
	}
	return s, &now
}

func (s *Service) failuresFor(t *testing.T, name string) int {
	t.Helper()
	n, err := s.Store.LoginFailures(store.FailureKey{Step: "pw", Source: "192.0.2.1", Name: name}, s.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStartLoginRefusesMalformedNameBeforeHashing(t *testing.T) {
	s, _ := boundService(t)
	for label, name := range map[string]string{
		"empty": "", "uppercase": "Ops", "punctuation": "ops!", "65 bytes": strings.Repeat("a", 65),
		"10 MB": strings.Repeat("a", 10<<20),
	} {
		_, err := s.StartLogin(httptest.NewRecorder(), httptest.NewRequest("POST", "/manage/login", nil), name, boundPassword)
		if !errors.Is(err, ErrRefused) {
			t.Fatalf("%s: %v, want the uniform refusal", label, err)
		}
		if s.decoy != "" {
			t.Fatalf("%s: the decoy hash ran for a malformed name", label)
		}
		if n := s.failuresFor(t, name); n != 0 {
			t.Fatalf("%s: %d failures stored under a malformed name", label, n)
		}
	}
	if _, err := s.StartLogin(httptest.NewRecorder(), httptest.NewRequest("POST", "/manage/login", nil), "nobody", boundPassword); !errors.Is(err, ErrRefused) {
		t.Fatalf("keep-control, an unknown well-formed name: %v", err)
	}
	if s.decoy == "" || s.failuresFor(t, "nobody") != 1 {
		t.Fatalf("keep-control: decoy ran %v, failures %d", s.decoy != "", s.failuresFor(t, "nobody"))
	}
}

func TestStartLoginBoundsConcurrentHashing(t *testing.T) {
	s, _ := boundService(t)
	s.hashSlots = make(chan struct{}, 1)
	s.hashWait = 20 * time.Millisecond
	s.hashSlots <- struct{}{}
	req := httptest.NewRequest("POST", "/manage/login", nil)
	start := time.Now()
	if _, err := s.StartLogin(httptest.NewRecorder(), req, "ops", boundPassword); !errors.Is(err, ErrBusy) {
		t.Fatalf("with every hash slot taken: %v, want ErrBusy", err)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("a busy sign-in waited %s", waited)
	}
	if n := s.failuresFor(t, "ops"); n != 0 {
		t.Fatalf("a busy refusal counted %d failures", n)
	}
	<-s.hashSlots
	if _, err := s.StartLogin(httptest.NewRecorder(), req, "ops", boundPassword); err != nil {
		t.Fatalf("control, a free slot: %v", err)
	}
	if len(s.hashSlots) != 0 {
		t.Fatal("the hash slot was not released")
	}
}

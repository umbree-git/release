package totp_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/totp"
)

var at = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestTOTPAcceptsCurrentStep(t *testing.T) {
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.Code(secret, at)
	if err != nil || len(code) != 6 {
		t.Fatalf("code %q: %v", code, err)
	}
	step, ok := totp.Verify(secret, code, at, 0)
	if !ok || step != at.Unix()/totp.Period {
		t.Fatalf("the current code: step %d ok %v", step, ok)
	}
	stale, _ := totp.Code(secret, at.Add(-3*totp.Period*time.Second))
	if _, ok := totp.Verify(secret, stale, at, 0); ok && stale != code {
		t.Fatal("a code three steps old was accepted")
	}
}

func TestTOTPRefusesReplayedStep(t *testing.T) {
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.Code(secret, at)
	step, ok := totp.Verify(secret, code, at, 0)
	if !ok {
		t.Fatal("control: the first use was refused")
	}
	if _, ok := totp.Verify(secret, code, at, step); ok {
		t.Fatal("the same step was accepted twice")
	}
	if _, ok := totp.Verify(secret, code, at.Add(totp.Period*time.Second), step); ok {
		t.Fatal("the used step was accepted one period later")
	}
}

func TestTOTPSecretSealedAtRest(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "totp.key")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sealer, err := auth.LoadSealer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	enrol, err := auth.New(st, sealer, nil, nil).AddAdmin("ops", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enrol.OTPAuthURL, "otpauth://totp/") || !strings.Contains(enrol.OTPAuthURL, enrol.Secret) {
		t.Fatalf("enrolment %+v", enrol)
	}
	a, err := st.Admin("ops")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrol.Secret)
	if len(a.TOTPSecretEnc) == 0 || bytes.Contains(a.TOTPSecretEnc, []byte(enrol.Secret)) || bytes.Contains(a.TOTPSecretEnc, raw) {
		t.Fatalf("the stored secret is not sealed: %x", a.TOTPSecretEnc)
	}
	opened, err := sealer.Open(a.TOTPSecretEnc)
	if err != nil || string(opened) != enrol.Secret {
		t.Fatalf("open with the right key: %q %v", opened, err)
	}
	other := filepath.Join(dir, "other.key")
	if err := os.WriteFile(other, bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	wrong, err := auth.LoadSealer(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.Open(a.TOTPSecretEnc); err == nil {
		t.Fatal("another key opened the sealed secret")
	}
}

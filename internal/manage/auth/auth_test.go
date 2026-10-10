package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
)

func TestPasswordArgon2idRoundTrip(t *testing.T) {
	a, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a, "$argon2id$v=19$") || a == b {
		t.Fatalf("hashes %q and %q: want two salted argon2id hashes", a, b)
	}
	ok, err := auth.VerifyPassword(a, testPassword)
	if err != nil || !ok {
		t.Fatalf("verify the right password: %v %v", ok, err)
	}
}

func TestPasswordWrongRefused(t *testing.T) {
	h, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{"", "correct horse battery ", "Correct horse battery"} {
		if ok, err := auth.VerifyPassword(h, wrong); err != nil || ok {
			t.Fatalf("password %q: ok=%v err=%v, want refused", wrong, ok, err)
		}
	}
	if _, err := auth.VerifyPassword("$2a$10$notargon", testPassword); err == nil {
		t.Fatal("a non-argon2id hash verified")
	}
}

func TestLoginRateLimited(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 5; i++ {
		if _, _, err := r.password(testAdmin, "wrong password!"); !isRefusal(err) {
			t.Fatalf("attempt %d: %v, want the refusal", i+1, err)
		}
	}
	if _, _, err := r.password(testAdmin, testPassword); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("the right password after five failures: %v, want rate limited", err)
	}
	r.now = r.now.Add(16 * time.Minute)
	if _, _, err := r.password(testAdmin, testPassword); err != nil {
		t.Fatalf("after the window: %v", err)
	}
}

func TestLoginRefusalUniform(t *testing.T) {
	r := newRig(t)
	_, _, unknown := r.password("nobody", testPassword)
	_, _, wrongPassword := r.password(testAdmin, "not the password")
	pending, _, err := r.password(testAdmin, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongCode := r.second(pending, wrongCodeFor(r.code()))
	for name, got := range map[string]error{"unknown admin": unknown, "wrong password": wrongPassword, "wrong code": wrongCode} {
		if got == nil || got.Error() != auth.ErrRefused.Error() || !errors.Is(got, auth.ErrRefused) {
			t.Fatalf("%s: %v, want exactly %q", name, got, auth.ErrRefused)
		}
	}
	if _, err := r.svc.PendingSession(request(pending)); err == nil {
		t.Fatal("a wrong code left the password-step session alive")
	}
	full := r.signIn()
	if _, err := r.svc.Session(request(full)); err != nil {
		t.Fatalf("control, all three right: %v", err)
	}
}

func TestSessionExpires(t *testing.T) {
	r := newRig(t)
	full := r.signIn()
	r.now = r.now.Add(auth.SessionTTL - time.Second)
	if _, err := r.svc.Session(request(full)); err != nil {
		t.Fatalf("inside the TTL: %v", err)
	}
	r.now = r.now.Add(time.Second)
	if _, err := r.svc.Session(request(full)); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("at the TTL: %v, want unauthorized", err)
	}
}

func TestSessionPasswordOnlyIsNotMFA(t *testing.T) {
	r := newRig(t)
	pending, csrf, err := r.password(testAdmin, testPassword)
	if err != nil || csrf == "" {
		t.Fatalf("password step: %q %v", csrf, err)
	}
	if cookie(pending, auth.SessionCookie) == nil || cookie(pending, auth.CSRFCookie) == nil {
		t.Fatalf("password step set %v", pending)
	}
	if _, err := r.svc.Session(request(pending)); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("a password-only session: %v, want unauthorized", err)
	}
	if _, err := r.svc.PendingSession(request(pending)); err != nil {
		t.Fatalf("the code step must still see it: %v", err)
	}
	full, err := r.second(pending, r.code())
	if err != nil {
		t.Fatal(err)
	}
	if cookie(full, auth.SessionCookie).Value == cookie(pending, auth.SessionCookie).Value {
		t.Fatal("the session id was not rotated at the second factor")
	}
	if _, err := r.svc.Session(request(pending)); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("the copied password-step cookie after sign-in: %v, want unauthorized", err)
	}
	if _, err := r.svc.Session(request(full)); err != nil {
		t.Fatalf("control: %v", err)
	}
}

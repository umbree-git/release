package auth_test

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/totp"
)

const (
	testAdmin    = "ops"
	testPassword = "correct horse battery"
)

type rig struct {
	t      *testing.T
	st     *store.Store
	svc    *auth.Service
	now    time.Time
	secret string
}

func writeKey(t *testing.T, dir string) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "totp.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sealer, err := auth.LoadSealer(writeKey(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, st: st, now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	r.svc = auth.New(st, sealer, func() time.Time { return r.now }, nil)
	enrol, err := r.svc.AddAdmin(testAdmin, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	r.secret = enrol.Secret
	return r
}

func (r *rig) code() string {
	r.t.Helper()
	c, err := totp.Code(r.secret, r.now)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func request(cookies []*http.Cookie) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/manage/login", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return req
}

func cookie(cs []*http.Cookie, name string) *http.Cookie {
	for _, c := range cs {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

func (r *rig) password(name, password string) ([]*http.Cookie, string, error) {
	rec := httptest.NewRecorder()
	csrf, err := r.svc.StartLogin(rec, request(nil), name, password)
	return rec.Result().Cookies(), csrf, err
}

func (r *rig) second(pending []*http.Cookie, code string) ([]*http.Cookie, error) {
	rec := httptest.NewRecorder()
	err := r.svc.CompleteTOTP(rec, request(pending), code)
	return rec.Result().Cookies(), err
}

func (r *rig) signIn() []*http.Cookie {
	r.t.Helper()
	pending, _, err := r.password(testAdmin, testPassword)
	if err != nil {
		r.t.Fatalf("password step: %v", err)
	}
	full, err := r.second(pending, r.code())
	if err != nil {
		r.t.Fatalf("code step: %v", err)
	}
	return full
}

func isRefusal(err error) bool {
	return err != nil && strings.Contains(err.Error(), auth.ErrRefused.Error())
}

func wrongCodeFor(right string) string {
	b := []byte(right)
	b[0] = '0' + (b[0]-'0'+1)%10
	return string(b)
}

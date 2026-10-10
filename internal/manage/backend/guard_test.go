package backend_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/backend"
)

func checkURL(t *testing.T, raw string) error {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return (&backend.Guard{}).CheckURL(u)
}

func wantRefusal(t *testing.T, raw, says string) {
	t.Helper()
	err := checkURL(t, raw)
	if err == nil || !strings.Contains(err.Error(), says) {
		t.Errorf("%s: %v, want a refusal naming %q", raw, err, says)
	}
}

func TestGuardRefusesHTTP(t *testing.T) {
	wantRefusal(t, "http://acct.r2.cloudflarestorage.com/b/k", "not https")
	wantRefusal(t, "ftp://acct.r2.cloudflarestorage.com/b/k", "not https")
	if err := checkURL(t, "https://acct.r2.cloudflarestorage.com/b/k"); err != nil {
		t.Fatalf("keep-control: %v", err)
	}
}

func TestGuardRefusesUserinfo(t *testing.T) {
	wantRefusal(t, "https://acct.r2.cloudflarestorage.com@evil.example/b/k", "userinfo")
	wantRefusal(t, "https://user:pass@acct.r2.cloudflarestorage.com/b/k", "userinfo")
}

func TestGuardRefusesIPLiteral(t *testing.T) {
	for _, raw := range []string{"https://93.184.216.34/k", "https://127.0.0.1/k", "https://169.254.169.254/latest", "https://[::1]/k", "https://[2606:4700::1]/k"} {
		wantRefusal(t, raw, "IP literal")
	}
	wantRefusal(t, "https:///k", "no host")
}

func TestGuardRefusesRedirect(t *testing.T) {
	hits := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/elsewhere", http.StatusTemporaryRedirect)
			return
		}
		_, _ = w.Write([]byte("followed"))
	}))
	defer srv.Close()
	c := (&backend.Guard{}).Client()
	c.Transport = srv.Client().Transport
	resp, err := c.Get(srv.URL + "/start")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("a redirect was followed (status %d)", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "redirect") || hits != 1 {
		t.Fatalf("err %v after %d requests, want a redirect refusal after 1", err, hits)
	}
}

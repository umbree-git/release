package web_test

import (
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/totp"
)

const wrongPassword = "not the password at all"

func (c *console) attempt(name, password string, header map[string]string) int {
	c.t.Helper()
	return c.browser().do(http.MethodPost, "/manage/login", url.Values{"name": {name}, "password": {password}}, header).status
}

func xff(v string) map[string]string { return map[string]string{"X-Forwarded-For": v} }

func (c *console) fail(t *testing.T, name string, n int, header func(i int) map[string]string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if got := c.attempt(name, wrongPassword, header(i)); got != http.StatusUnauthorized {
			t.Fatalf("wrong password %d for %s: HTTP %d, want 401", i+1, name, got)
		}
	}
}

func (c *console) signInVia(t *testing.T, name string, header map[string]string) {
	t.Helper()
	b := c.browser()
	r := b.do(http.MethodPost, "/manage/login", url.Values{"name": {name}, "password": {adminPassword}}, header)
	if r.status != http.StatusOK {
		t.Fatalf("password step for %s via %v: HTTP %d", name, header, r.status)
	}
	code, _ := totp.Code(c.secrets[name], c.now)
	r = b.do(http.MethodPost, "/manage/login/totp", url.Values{"code": {code}, auth.CSRFField: {b.csrf()}}, header)
	if r.status != http.StatusSeeOther {
		t.Fatalf("code step for %s via %v: HTTP %d", name, header, r.status)
	}
}

func trusting(t *testing.T, proxy string) *console {
	t.Helper()
	c := newConsole(t)
	if proxy != "" {
		c.svc.TrustedProxy = netip.MustParseAddr(proxy)
	}
	return c
}

func TestTrustedProxySourceHonoured(t *testing.T) {
	c := trusting(t, "127.0.0.1")
	c.fail(t, "ops", 5, func(int) map[string]string { return xff("203.0.113.10") })
	if got := c.attempt("ops", adminPassword, xff("203.0.113.10")); got != http.StatusTooManyRequests {
		t.Fatalf("source A after 5 failures: HTTP %d, want 429", got)
	}
	c.signInVia(t, "ops", xff("203.0.113.20"))
}

func TestUntrustedPeerHeadersIgnored(t *testing.T) {
	for _, proxy := range []string{"", "192.0.2.99"} {
		c := trusting(t, proxy)
		rotate := func(i int) map[string]string {
			return map[string]string{"X-Forwarded-For": fmt.Sprintf("198.51.100.%d", i+1), "X-Real-IP": fmt.Sprintf("198.51.100.%d", i+50),
				"Forwarded": fmt.Sprintf("for=198.51.100.%d", i+100)}
		}
		c.fail(t, "ops", 5, rotate)
		if got := c.attempt("ops", adminPassword, rotate(9)); got != http.StatusTooManyRequests {
			t.Fatalf("proxy %q: rotated headers from an untrusted peer escaped the budget: HTTP %d", proxy, got)
		}
	}
}

func TestAppendedSpoofIgnored(t *testing.T) {
	c := trusting(t, "127.0.0.1")
	c.fail(t, "ops", 5, func(i int) map[string]string { return xff(fmt.Sprintf("198.51.100.%d, 203.0.113.10", i+1)) })
	if got := c.attempt("ops", adminPassword, xff("198.51.100.77, 203.0.113.10")); got != http.StatusTooManyRequests {
		t.Fatalf("a rotated leftmost entry escaped: HTTP %d", got)
	}
	c.signInVia(t, "ops", xff("198.51.100.1, 203.0.113.30"))
}

func TestTrustedProxyGarbledHeaderFallsBack(t *testing.T) {
	c := trusting(t, "127.0.0.1")
	garbled := []map[string]string{nil, xff(""), xff("not-an-ip"), xff("203.0.113.10, "), xff("203.0.113.10:4431")}
	c.fail(t, "ops", 5, func(i int) map[string]string { return garbled[i] })
	if got := c.attempt("ops", adminPassword, xff("garbage")); got != http.StatusTooManyRequests {
		t.Fatalf("garbled headers did not all count against the proxy: HTTP %d", got)
	}
	c.signInVia(t, "ops", xff("203.0.113.40"))
}

func TestPerNameCeiling(t *testing.T) {
	c := trusting(t, "127.0.0.1")
	c.addAdmin("ops2")
	for s := 0; s < 10; s++ {
		c.fail(t, "ops", 5, func(int) map[string]string { return xff(fmt.Sprintf("203.0.113.%d", 100+s)) })
	}
	if got := c.attempt("ops", adminPassword, xff("203.0.113.250")); got != http.StatusTooManyRequests {
		t.Fatalf("a fresh source after 50 failures for the name: HTTP %d, want 429", got)
	}
	c.signInVia(t, "ops2", xff("203.0.113.251"))
}

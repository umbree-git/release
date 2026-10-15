package web_test

import (
	"net/http"
	"strings"
	"testing"
)

func assertVersionJSHeaders(t *testing.T, path string, r reply) {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("%s: HTTP %d %s", path, r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Fatalf("%s: Content-Type %q", path, ct)
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%s: no nosniff in %v", path, r.header)
	}
	if cc := r.header.Get("Cache-Control"); cc != "public, max-age=300" {
		t.Fatalf("%s: Cache-Control %q", path, cc)
	}
}

func TestVersionJSLive(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	r := c.browser().get("/umbree/version.js")
	assertVersionJSHeaders(t, "/umbree/version.js", r)
	want := `__umbreeVersion({"component":"umbree","channel":"production","version":"3.3.3","stamp":"` + stampOf("3.3.3", 2) + `"});` + "\n"
	if r.body != want {
		t.Fatalf("body %q, want %q", r.body, want)
	}
	for _, label := range []string{"new", "below", "equal"} {
		if rv := c.row(label); strings.Contains(r.body, rv.Stamp) {
			t.Fatalf("a staged row %s is named: %s", rv.Stamp, r.body)
		}
	}
	for _, p := range []string{"/nope/version.js", "/beta/version.js", "/umbree/version.json"} {
		if r := c.browser().get(p); r.status != http.StatusNotFound {
			t.Fatalf("%s: HTTP %d", p, r.status)
		}
	}
	if r := c.browser().post("/umbree/version.js", nil); r.status == http.StatusOK {
		t.Fatal("POST /umbree/version.js answered 200")
	}
}

func TestVersionJSNoCurrentIsNull(t *testing.T) {
	c := newConsole(t)
	c.stage("new", "4.4.4", 1)
	for _, comp := range []string{"umbree", "umbreed"} {
		path := "/" + comp + "/version.js"
		r := c.browser().get(path)
		assertVersionJSHeaders(t, path, r)
		want := `__umbreeVersion({"component":"` + comp + `","channel":"production","version":null,"stamp":null});` + "\n"
		if r.body != want {
			t.Fatalf("%s: body %q, want %q", path, r.body, want)
		}
	}
}

func TestVersionJSStoreErrorIs503(t *testing.T) {
	c := newConsole(t)
	c.withClosedStore()
	r := c.browser().get("/umbree/version.js")
	if r.status != http.StatusServiceUnavailable || r.body != "" {
		t.Fatalf("HTTP %d body %q, want 503 and no body", r.status, r.body)
	}
	if strings.Contains(r.header.Get("Content-Type"), "javascript") {
		t.Fatalf("a failed read is typed as script: %v", r.header)
	}
}

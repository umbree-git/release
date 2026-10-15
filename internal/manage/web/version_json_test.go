package web_test

import (
	"net/http"
	"strings"
	"testing"
)

func assertVersionJSONHeaders(t *testing.T, path string, r reply) {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("%s: HTTP %d %s", path, r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s: Content-Type %q", path, ct)
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("%s: no nosniff in %v", path, r.header)
	}
	if cc := r.header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("%s: Cache-Control %q", path, cc)
	}
	if acao := r.header.Get("Access-Control-Allow-Origin"); acao != "" {
		t.Fatalf("%s: a CORS header %q", path, acao)
	}
}

func TestVersionJSONLive(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	r := c.browser().get("/umbree/version.json")
	assertVersionJSONHeaders(t, "/umbree/version.json", r)
	want := `{"component":"umbree","channel":"production","version":"3.3.3","stamp":"` + stampOf("3.3.3", 2) + `"}` + "\n"
	if r.body != want {
		t.Fatalf("body %q, want %q", r.body, want)
	}
	for _, label := range []string{"new", "below", "equal"} {
		if rv := c.row(label); strings.Contains(r.body, rv.Stamp) {
			t.Fatalf("a staged row %s is named: %s", rv.Stamp, r.body)
		}
	}
}

func TestVersionJSONNoCurrentIsNull(t *testing.T) {
	c := newConsole(t)
	c.stage("new", "4.4.4", 1)
	for _, comp := range []string{"umbree", "umbreed"} {
		path := "/" + comp + "/version.json"
		r := c.browser().get(path)
		assertVersionJSONHeaders(t, path, r)
		want := `{"component":"` + comp + `","channel":"production","version":null,"stamp":null}` + "\n"
		if r.body != want {
			t.Fatalf("%s: body %q, want %q", path, r.body, want)
		}
	}
}

func TestVersionJSONStoreErrorIs503(t *testing.T) {
	c := newConsole(t)
	c.withClosedStore()
	r := c.browser().get("/umbree/version.json")
	if r.status != http.StatusServiceUnavailable || r.body != "" {
		t.Fatalf("HTTP %d body %q, want 503 and no body", r.status, r.body)
	}
	if strings.Contains(r.header.Get("Content-Type"), "json") {
		t.Fatalf("a failed read is typed as JSON: %v", r.header)
	}
}

func TestVersionJSONUnknownPaths(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	for _, p := range []string{"/nope/version.json", "/beta/version.json", "/umbree/version.jsonp"} {
		if r := c.browser().get(p); r.status != http.StatusNotFound {
			t.Fatalf("%s: HTTP %d", p, r.status)
		}
	}
	if r := c.browser().post("/umbree/version.json", nil); r.status == http.StatusOK {
		t.Fatal("POST /umbree/version.json answered 200")
	}
}

func TestVersionJSAndJSONAgree(t *testing.T) {
	for _, seeded := range []bool{true, false} {
		c := newConsole(t)
		if seeded {
			c.seedHistory()
		}
		for _, comp := range []string{"umbree", "umbreed"} {
			js := c.browser().get("/" + comp + "/version.js").body
			obj := strings.TrimSuffix(c.browser().get("/"+comp+"/version.json").body, "\n")
			if want := "__umbreeVersion(" + obj + ");\n"; js != want {
				t.Fatalf("%s seeded=%v: version.js %q, want %q", comp, seeded, js, want)
			}
		}
	}
}

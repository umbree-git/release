package web_test

import (
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/auth"
)

const wantCSP = "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"

var (
	headerRe = regexp.MustCompile(`(?s)<header class="top">(.*?)</header>`)
	stripRe  = regexp.MustCompile(`(?s)<div id="confirm-strip"[^>]*>(.*?)</div>`)
	liRe     = regexp.MustCompile(`<li [^>]*>`)
)

func TestCSPHeaderExact(t *testing.T) {
	c := newConsole(t)
	b := c.signedIn("ops")
	for _, r := range []reply{c.browser().get("/manage/login"), b.get("/manage/production/umbree"), b.get("/manage/static/site.css"), b.get("/manage/static/console.js"), b.get("/healthz")} {
		if got := r.header.Get("Content-Security-Policy"); got != wantCSP {
			t.Fatalf("CSP %q, want %q", got, wantCSP)
		}
	}
}

func TestConsoleJSServedWithTypeAndNosniff(t *testing.T) {
	c := newConsole(t)
	r := c.browser().get("/manage/static/console.js")
	if r.status != http.StatusOK {
		t.Fatalf("HTTP %d", r.status)
	}
	if ct := r.header.Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Fatalf("Content-Type %q", ct)
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.body, "confirm-strip") {
		t.Fatalf("headers %v body %q", r.header, r.body)
	}
}

func TestConfirmStripCarriesHiddenFields(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	r := b.post(actionPath(c.rows["new"], "promote"), url.Values{auth.CSRFField: {b.csrf()}})
	m := stripRe.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || m == nil {
		t.Fatalf("HTTP %d, no confirm strip in %s", r.status, r.body)
	}
	if !strings.Contains(m[1], `<input type="hidden" name="csrf_token" value="`+b.csrf()+`">`) || !confirmRe.MatchString(m[1]) {
		t.Fatalf("strip lacks hidden fields: %s", m[1])
	}
}

func TestInlineProgressRendersPanelOnly(t *testing.T) {
	for _, action := range []string{"promote", "yank"} {
		c := newConsole(t)
		c.seedHistory()
		b := c.signedIn("ops")
		label := "new"
		if action == "yank" {
			label = "current"
		}
		path := actionPath(c.rows[label], action)
		token := b.confirmToken(path)
		r := b.do(http.MethodPost, path, url.Values{auth.CSRFField: {b.csrf()}, "confirm": {token}}, map[string]string{"X-Inline-Progress": "1"})
		if r.status != http.StatusOK || strings.Contains(r.body, "<header") || strings.Contains(r.body, "<html") {
			t.Fatalf("%s: HTTP %d, not panel-only: %s", action, r.status, r.body)
		}
		if !strings.Contains(r.body, `<ol id="log" class="log">`) || !strings.Contains(r.body, `data-result="done"`) {
			t.Fatalf("%s: panel %s", action, r.body)
		}
		lis := liRe.FindAllString(r.body, -1)
		if len(lis) == 0 {
			t.Fatalf("%s: no log lines", action)
		}
		for _, li := range lis {
			if !strings.Contains(li, "data-step=") || !strings.Contains(li, "data-status=") {
				t.Fatalf("%s: line %s lacks data-step or data-status", action, li)
			}
		}
	}
}

func TestHeaderShowsLocalPartOnly(t *testing.T) {
	c := newConsole(t)
	c.addAdmin("a@b.example")
	b := c.signedIn("a@b.example")
	r := b.get("/manage/production/umbree")
	m := headerRe.FindStringSubmatch(r.body)
	if m == nil {
		t.Fatalf("no header in %s", r.body)
	}
	if strings.Contains(m[1], "@") || !strings.Contains(m[1], `<span class="dim">a</span>`) {
		t.Fatalf("header %s", m[1])
	}
}

func TestSiteCSSMonoStackAndWrap(t *testing.T) {
	r := newConsole(t).browser().get("/manage/static/site.css")
	mono := `ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace`
	if !regexp.MustCompile(`header\.top \{[^}]*flex-wrap: wrap`).MatchString(r.body) {
		t.Fatal("header.top does not wrap")
	}
	for _, sel := range []string{`.stamp, code {`, `.log {`} {
		i := strings.Index(r.body, sel)
		if i < 0 || !strings.Contains(r.body[i:i+strings.Index(r.body[i:], "}")], mono) {
			t.Fatalf("%s lacks the mono stack", sel)
		}
	}
}

func TestConsoleServesFavicon(t *testing.T) {
	c := newConsole(t)
	r := c.browser().get("/manage/static/favicon.svg")
	if r.status != http.StatusOK {
		t.Fatalf("HTTP %d", r.status)
	}
	if ct := r.header.Get("Content-Type"); ct != "image/svg+xml" {
		t.Fatalf("Content-Type %q", ct)
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", r.header)
	}
	want, err := fs.ReadFile(release.Assets, "site/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	if r.body != string(want) {
		t.Fatal("the served favicon is not site/favicon.svg")
	}
}

func TestLayoutLinksFavicon(t *testing.T) {
	c := newConsole(t)
	b := c.signedIn("ops")
	const link = `<link rel="icon" type="image/svg+xml" href="/manage/static/favicon.svg">`
	for name, r := range map[string]reply{"login": c.browser().get("/manage/login"), "index": b.get("/manage/production/umbree")} {
		if !strings.Contains(r.body, link) {
			t.Errorf("%s page does not link the favicon", name)
		}
	}
}

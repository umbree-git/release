package web_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/publish"
)

type writeRoute struct {
	name   string
	path   func(c *console) string
	target string
}

var writeRoutes = []writeRoute{
	{"page promote", func(c *console) string { return actionPath(c.rows["new"], "promote") }, "new"},
	{"page yank", func(c *console) string { return actionPath(c.rows["current"], "yank") }, "current"},
	{"api promote", func(c *console) string { return apiPath(c.rows["new"], "promote") }, "new"},
	{"api yank", func(c *console) string { return apiPath(c.rows["current"], "yank") }, "current"},
}

func (c *console) assertUntouched(t *testing.T, route string, writesBefore int) {
	t.Helper()
	if st, _ := c.state("new"); st != "staged" {
		t.Fatalf("%s: the staged row is now %s", route, st)
	}
	if st, cur := c.state("current"); st != "public" || !cur {
		t.Fatalf("%s: the current row is now %s current=%v", route, st, cur)
	}
	if c.writes() != writesBefore {
		t.Fatalf("%s: the public store saw %d writes", route, c.writes()-writesBefore)
	}
}

func TestWriteRefusedWithoutSession(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	before := c.writes()
	pending := c.browser()
	pending.passwordStep("ops")
	for _, rt := range writeRoutes {
		for who, b := range map[string]*browser{"anonymous": c.browser(), "password-only": pending} {
			r := b.post(rt.path(c), url.Values{auth.CSRFField: {b.csrf()}, "confirm": {"forged"}})
			if r.status != http.StatusUnauthorized {
				t.Fatalf("%s as %s: HTTP %d, want 401", rt.name, who, r.status)
			}
			c.assertUntouched(t, rt.name+" as "+who, before)
		}
	}
	control := c.signedIn("ops")
	if tok := control.confirmToken(actionPath(c.rows["new"], "promote")); tok == "" {
		t.Fatal("control: a signed-in admin got no confirm token")
	}
}

func TestWriteRefusedWithoutCSRF(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	before := c.writes()
	for _, rt := range writeRoutes {
		token := b.confirmToken(strings.Replace(rt.path(c), "/api/", "/", 1))
		for name, form := range map[string]url.Values{
			"no token":    {"confirm": {token}},
			"wrong token": {"confirm": {token}, auth.CSRFField: {"not-the-token"}},
		} {
			r := b.post(rt.path(c), form)
			if r.status != http.StatusForbidden {
				t.Fatalf("%s, %s: HTTP %d, want 403", rt.name, name, r.status)
			}
			c.assertUntouched(t, rt.name+", "+name, before)
		}
	}
}

func TestWriteRefusedWithForeignCSRF(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	c.addAdmin("ops2")
	a, other := c.signedIn("ops"), c.signedIn("ops2")
	before := c.writes()
	for _, rt := range writeRoutes {
		token := a.confirmToken(strings.Replace(rt.path(c), "/api/", "/", 1))
		r := a.post(rt.path(c), url.Values{auth.CSRFField: {other.csrf()}, "confirm": {token}})
		if r.status != http.StatusForbidden {
			t.Fatalf("%s with the other session's token: HTTP %d, want 403", rt.name, r.status)
		}
		own := a.cookies[auth.CSRFCookie]
		a.cookies[auth.CSRFCookie] = other.csrf()
		r = a.post(rt.path(c), url.Values{auth.CSRFField: {other.csrf()}, "confirm": {token}})
		a.cookies[auth.CSRFCookie] = own
		if r.status != http.StatusForbidden {
			t.Fatalf("%s with the other session's cookie and token: HTTP %d, want 403", rt.name, r.status)
		}
		c.assertUntouched(t, rt.name, before)
	}
}

func TestConfirmReplayRefused(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	path := actionPath(c.rows["new"], "promote")
	token := b.confirmToken(path)
	if r := b.confirmed(path, token); r.status != http.StatusOK || !strings.Contains(r.body, `data-result="done"`) {
		t.Fatalf("first use: HTTP %d %s", r.status, r.body)
	}
	writes := c.writes()
	r := b.confirmed(path, token)
	if r.status != http.StatusForbidden || strings.Contains(r.body, "data-result") {
		t.Fatalf("replay: HTTP %d %s, want 403 before any action", r.status, r.body)
	}
	if c.writes() != writes {
		t.Fatal("the replay reached the public store")
	}
}

func TestConfirmBoundToRow(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	other := c.stage("other", "5.5.5", 1)
	b := c.signedIn("ops")
	token := b.confirmToken(actionPath(c.rows["new"], "promote"))
	if r := b.confirmed(actionPath(other, "promote"), token); r.status != http.StatusForbidden {
		t.Fatalf("a token for row %d on row %d: HTTP %d, want 403", c.rows["new"], other, r.status)
	}
	if st, _ := c.state("other"); st != "staged" {
		t.Fatalf("row %d is %s", other, st)
	}
	if r := b.confirmed(actionPath(c.rows["new"], "promote"), token); r.status != http.StatusOK {
		t.Fatalf("control, the token on its own row: HTTP %d", r.status)
	}
}

func TestConfirmBoundToAction(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	id := c.rows["current"]
	token := b.confirmToken(actionPath(id, "promote"))
	if r := b.confirmed(actionPath(id, "yank"), token); r.status != http.StatusForbidden {
		t.Fatalf("a promote token on yank: HTTP %d, want 403", r.status)
	}
	if st, cur := c.state("current"); st != "public" || !cur {
		t.Fatalf("the current row is %s current=%v", st, cur)
	}
}

func TestConfirmExpires(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	path := actionPath(c.rows["new"], "promote")
	token := b.confirmToken(path)
	c.now = c.now.Add(10*time.Minute + time.Second)
	if r := b.confirmed(path, token); r.status != http.StatusForbidden {
		t.Fatalf("an expired token: HTTP %d, want 403", r.status)
	}
	if st, _ := c.state("new"); st != "staged" {
		t.Fatalf("the row is %s", st)
	}
	if r := b.confirmed(path, b.confirmToken(path)); r.status != http.StatusOK {
		t.Fatalf("control, a fresh token: HTTP %d", r.status)
	}
}

func TestConfirmControlSucceeds(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	path := actionPath(c.rows["new"], "promote")
	r := b.confirmed(path, b.confirmToken(path))
	if r.status != http.StatusOK || !strings.Contains(r.body, `data-result="done"`) {
		t.Fatalf("promote: HTTP %d %s", r.status, r.body)
	}
	if st, cur := c.state("new"); st != "public" || !cur {
		t.Fatalf("after promote the row is %s current=%v", st, cur)
	}
	api := apiPath(c.rows["new"], "yank")
	first := b.do(http.MethodPost, api, url.Values{}, map[string]string{auth.CSRFHeader: b.csrf()})
	var ask struct{ Confirm string }
	if first.status != http.StatusPreconditionRequired || json.Unmarshal([]byte(first.body), &ask) != nil || ask.Confirm == "" {
		t.Fatalf("api first POST: HTTP %d %s", first.status, first.body)
	}
	second := b.do(http.MethodPost, api, url.Values{"confirm": {ask.Confirm}}, map[string]string{auth.CSRFHeader: b.csrf()})
	if second.status != http.StatusOK || second.header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("api second POST: HTTP %d %s", second.status, second.body)
	}
	assertTerminal(t, second.body, "done")
	if st, _ := c.state("new"); st != "yanked" {
		t.Fatalf("after the api yank the row is %s", st)
	}
}

func assertTerminal(t *testing.T, ndjson, want string) {
	t.Helper()
	var last publish.Event
	sc := bufio.NewScanner(strings.NewReader(ndjson))
	for sc.Scan() {
		if err := json.Unmarshal(sc.Bytes(), &last); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
	}
	if last.Step != want {
		t.Fatalf("terminal event %+v, want %s", last, want)
	}
}

func TestSignInRefusalByteIdentical(t *testing.T) {
	c := newConsole(t)
	unknown := c.browser().post("/manage/login", url.Values{"name": {"nobody"}, "password": {adminPassword}})
	wrongPassword := c.browser().post("/manage/login", url.Values{"name": {"ops"}, "password": {"not the password"}})
	b := c.browser()
	b.passwordStep("ops")
	wrongCode := b.post("/manage/login/totp", url.Values{"code": {"12345"}, auth.CSRFField: {b.csrf()}})
	for name, r := range map[string]reply{"wrong password": wrongPassword, "wrong code": wrongCode} {
		if r.status != unknown.status || r.body != unknown.body {
			t.Fatalf("%s: HTTP %d differs from the unknown-admin refusal (HTTP %d)\n%s\n---\n%s", name, r.status, unknown.status, r.body, unknown.body)
		}
	}
	if unknown.status != http.StatusUnauthorized {
		t.Fatalf("refusal status %d, want 401", unknown.status)
	}
	if r := c.signedIn("ops").get("/manage/production/umbree"); r.status != http.StatusOK {
		t.Fatalf("control: HTTP %d", r.status)
	}
}

func TestPasswordOnlySessionRefusedOnEveryRoute(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.browser()
	b.passwordStep("ops")
	copied := &browser{c: c, cookies: map[string]string{auth.SessionCookie: b.cookies[auth.SessionCookie]}}
	for _, path := range []string{"/manage", "/manage/production/umbree", "/manage/production/umbree/history"} {
		for who, br := range map[string]*browser{"password-only": b, "copied cookie": copied} {
			if r := br.get(path); r.status == http.StatusOK || strings.Contains(r.body, c.row("current").Stamp) {
				t.Fatalf("%s GET %s: HTTP %d", who, path, r.status)
			}
		}
	}
	if r := b.get("/manage/login/totp"); r.status != http.StatusOK || !strings.Contains(r.body, `name="code"`) {
		t.Fatalf("the code step itself: HTTP %d", r.status)
	}
}

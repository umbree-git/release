package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/web"
)

const betaEmpty = "No beta cycle — Umbree publishes production only"

var (
	tabRowRe   = regexp.MustCompile(`(?s)<div class="tabrow">(.*?)</div>`)
	channelsRe = regexp.MustCompile(`(?s)<nav class="channels"[^>]*>(.*?)</nav>`)
)

func channelLinks(t *testing.T, path, body string) [][]string {
	t.Helper()
	row := tabRowRe.FindStringSubmatch(body)
	if row == nil || !tabsRe.MatchString(row[1]) {
		t.Fatalf("%s: no tab row holding the tabs", path)
	}
	sw := channelsRe.FindStringSubmatch(row[1])
	if sw == nil {
		t.Fatalf("%s: no channel switch in the tab row", path)
	}
	return navLinkRe.FindAllStringSubmatch(sw[1], -1)
}

func assertSwitch(t *testing.T, path, body, suffix, active string) {
	t.Helper()
	links := channelLinks(t, path, body)
	want := [][2]string{{"Production", "/manage/production/umbree" + suffix}, {"Beta", "/manage/beta/umbree" + suffix}}
	if len(links) != len(want) {
		t.Fatalf("%s: switch %v", path, links)
	}
	for i, w := range want {
		if links[i][3] != w[0] || links[i][1] != w[1] {
			t.Fatalf("%s: switch link %d is %v, want %v", path, i, links[i], w)
		}
		if (links[i][2] != "") != (w[0] == active) {
			t.Fatalf("%s: %s aria-current %q, active is %s", path, w[0], links[i][2], active)
		}
	}
}

func TestChannelSwitchOnOverviewAndHistory(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	for path, suffix := range map[string]string{"/manage/production/umbree": "", "/manage/production/umbree/history": "/history"} {
		assertSwitch(t, path, b.get(path).body, suffix, "Production")
	}
	for path, suffix := range map[string]string{"/manage/beta/umbree": "", "/manage/beta/umbree/history": "/history"} {
		assertSwitch(t, path, b.get(path).body, suffix, "Beta")
	}
	css := c.browser().get("/manage/static/site.css").body
	if !regexp.MustCompile(`\.tabrow \{[^}]*display: flex`).MatchString(css) ||
		!regexp.MustCompile(`\.tabrow nav\.channels \{[^}]*margin-left: auto`).MatchString(css) {
		t.Fatal("the channel switch is not right-aligned in the tab row")
	}
}

func (c *console) withClosedStore() {
	c.t.Helper()
	closed, err := store.Open(c.t.TempDir())
	if err != nil {
		c.t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		c.t.Fatal(err)
	}
	s, err := web.New(web.Config{Store: closed, Auth: c.svc, PublicBaseURL: publicBase, Now: func() time.Time { return c.now }})
	if err != nil {
		c.t.Fatal(err)
	}
	c.srv.Close()
	c.srv = httptest.NewServer(s.Handler())
	c.t.Cleanup(c.srv.Close)
}

func TestBetaEmptyStateNeverReadsStore(t *testing.T) {
	c := newConsole(t)
	c.withClosedStore()
	b := c.signedIn("ops")
	if r := b.get("/manage/production/umbree"); r.status != http.StatusInternalServerError {
		t.Fatalf("control: a production page on the failing store answered HTTP %d", r.status)
	}
	for _, path := range []string{"/manage/beta/umbree", "/manage/beta/umbree/history", "/manage/beta/umbreed", "/manage/beta/umbreed/history"} {
		r := b.get(path)
		if r.status != http.StatusOK {
			t.Fatalf("%s: HTTP %d %s", path, r.status, r.body)
		}
		if !strings.Contains(r.body, betaEmpty) || strings.Contains(r.body, "<table") {
			t.Fatalf("%s: not the empty state: %s", path, r.body)
		}
		for _, f := range formBlock.FindAllString(r.body, -1) {
			if !strings.HasPrefix(f, `<form class="signout"`) {
				t.Fatalf("%s: a form other than Sign out: %s", path, f)
			}
		}
	}
	for _, path := range []string{"/manage/beta/nope", "/manage/beta/nope/history"} {
		if r := b.get(path); r.status != http.StatusNotFound {
			t.Fatalf("%s: HTTP %d", path, r.status)
		}
	}
	anon := c.browser()
	for _, path := range []string{"/manage/beta/umbree", "/manage/beta/umbree/history", "/manage/production/umbree"} {
		r := anon.get(path)
		if r.status != http.StatusSeeOther || r.header.Get("Location") != "/manage/login" || strings.Contains(r.body, betaEmpty) {
			t.Fatalf("%s signed out: HTTP %d to %q", path, r.status, r.header.Get("Location"))
		}
	}
}

func TestBetaWritesStillRefused(t *testing.T) {
	c := newConsole(t, func(d *publish.Deps) { d.Static = staticStub{} })
	c.seedHistory()
	b := c.signedIn("ops")
	paths := []string{"/manage/republish/beta/umbree"}
	for _, w := range retention.Windows {
		paths = append(paths, "/manage/retention/beta/umbree/"+string(w))
	}
	for _, p := range paths {
		if r := b.post(p, url.Values{auth.CSRFField: {b.csrf()}}); r.status != http.StatusNotFound {
			t.Fatalf("%s: HTTP %d", p, r.status)
		}
	}
	for _, w := range retention.Windows {
		p := "/manage/retention/production/umbree/" + string(w)
		if r := b.post(p, url.Values{auth.CSRFField: {b.csrf()}}); r.status != http.StatusOK {
			t.Fatalf("control %s: HTTP %d", p, r.status)
		}
	}
}

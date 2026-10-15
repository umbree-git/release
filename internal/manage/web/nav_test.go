package web_test

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/publish"
)

type staticStub struct{}

func (staticStub) Publish(_ context.Context, component string) (string, error) {
	return "republished " + component, nil
}

var (
	navRe      = regexp.MustCompile(`(?s)<nav class="components">(.*?)</nav>`)
	navLinkRe  = regexp.MustCompile(`<a href="([^"]+)"( aria-current="page")?>([^<]+)</a>`)
	tabsRe     = regexp.MustCompile(`(?s)<nav class="tabs">(.*?)</nav>`)
	signoutRe  = regexp.MustCompile(`(?s)<form class="signout"[^>]*>(.*?)</form>`)
	formBlock  = regexp.MustCompile(`(?s)<form[^>]*>.*?</form>`)
	hiddenRe   = regexp.MustCompile(`<input type="hidden" name="([a-z_]+)"`)
	theadRe    = regexp.MustCompile(`(?s)<thead>(.*?)</thead>`)
	thRe       = regexp.MustCompile(`<th[^>]*>([^<]+)</th>`)
	badgeRe    = regexp.MustCompile(`<span class="state state-([a-z]+)">`)
	headLabels = []string{"state", "version", "stamp", "cut", "promoted", "yanked", "expired", "downloads"}
)

func (c *console) signedPages(t *testing.T) map[string]reply {
	c.seedHistory()
	b := c.signedIn("ops")
	path := actionPath(c.rows["new"], "promote")
	token := b.confirmToken(path)
	gated := "/manage/retention/production/umbree/gated"
	return map[string]reply{
		"overview":  b.get("/manage/production/umbree"),
		"history":   b.get("/manage/production/umbree/history"),
		"confirm":   b.post(path, url.Values{auth.CSRFField: {b.csrf()}}),
		"progress":  b.confirmed(path, token),
		"retention": b.post(gated, url.Values{auth.CSRFField: {b.csrf()}}),
		"republish": b.post("/manage/republish/production/umbree", url.Values{auth.CSRFField: {b.csrf()}}),
	}
}

func TestEverySignedInPageRendersNav(t *testing.T) {
	c := newConsole(t, func(d *publish.Deps) { d.Static = staticStub{} })
	pages := c.signedPages(t)
	for name, r := range pages {
		if r.status != 200 {
			t.Fatalf("%s: HTTP %d %s", name, r.status, r.body)
		}
		if !strings.Contains(r.body, `<a class="brand" href="/manage">`) {
			t.Fatalf("%s: the brand does not link to /manage", name)
		}
		nav := navRe.FindStringSubmatch(r.body)
		if nav == nil {
			t.Fatalf("%s: no component nav", name)
		}
		var current []string
		links := navLinkRe.FindAllStringSubmatch(nav[1], -1)
		if len(links) != 2 {
			t.Fatalf("%s: nav links %v", name, links)
		}
		for _, l := range links {
			if l[2] != "" {
				current = append(current, l[3])
			}
		}
		if !slices.Equal(current, []string{"umbree"}) {
			t.Fatalf("%s: aria-current on %v, want only umbree", name, current)
		}
		so := signoutRe.FindStringSubmatch(r.body)
		if so == nil || !strings.Contains(so[1], `name="csrf_token" value="`) || strings.Contains(so[1], `value=""`) {
			t.Fatalf("%s: the sign-out form has no CSRF token: %v", name, so)
		}
	}
}

func TestOverviewHistoryTabsMarkCurrent(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	for path, active := range map[string]string{"/manage/production/umbree": "Overview", "/manage/production/umbree/history": "History"} {
		body := b.get(path).body
		tabs := tabsRe.FindStringSubmatch(body)
		if tabs == nil {
			t.Fatalf("%s: no tabs", path)
		}
		links := navLinkRe.FindAllStringSubmatch(tabs[1], -1)
		if len(links) != 2 || links[0][3] != "Overview" || links[1][3] != "History" {
			t.Fatalf("%s: tabs %v", path, links)
		}
		for _, l := range links {
			if (l[2] != "") != (l[3] == active) {
				t.Fatalf("%s: tab %s aria-current %q, active is %s", path, l[3], l[2], active)
			}
		}
		if strings.Contains(body, "history →") || strings.Contains(body, "← overview") {
			t.Fatalf("%s: the old text links remain", path)
		}
	}
}

func TestTablesHaveLabelledHead(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	path := actionPath(c.rows["new"], "promote")
	fileLabels := []string{"FILE", "PLATFORM", "SIZE", "SHA-256"}
	cases := map[string]struct {
		body   string
		heads  int
		labels []string
	}{
		"overview": {b.get("/manage/production/umbree").body, 2, fileLabels},
		"history":  {b.get("/manage/production/umbree/history").body, 1, historyLabels},
		"confirm":  {b.post(path, url.Values{auth.CSRFField: {b.csrf()}}).body, 1, headLabels},
	}
	for name, tc := range cases {
		heads := theadRe.FindAllStringSubmatch(tc.body, -1)
		if len(heads) != tc.heads {
			t.Fatalf("%s: %d thead, want %d", name, len(heads), tc.heads)
		}
		for _, h := range heads {
			var got []string
			for _, m := range thRe.FindAllStringSubmatch(h[1], -1) {
				got = append(got, m[1])
			}
			if !slices.Equal(got, tc.labels) {
				t.Fatalf("%s: head %v, want %v", name, got, tc.labels)
			}
		}
	}
}

func TestStateBadgeClasses(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	if err := c.st.Transition(c.rows["public"], "public", "yanked", c.now); err != nil {
		t.Fatal(err)
	}
	body := c.signedIn("ops").get("/manage/production/umbree/history").body
	seen := map[string]bool{}
	for _, m := range badgeRe.FindAllStringSubmatch(body, -1) {
		seen[m[1]] = true
	}
	for _, state := range []string{"staged", "public", "current", "yanked", "expired"} {
		if !seen[state] {
			t.Fatalf("no badge class state-%s in %v", state, seen)
		}
	}
}

func TestOverviewGroupsReleaseAndMaintenance(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get("/manage/production/umbree").body
	order := []string{"<h2>Release</h2>", "Ready to promote", "<h3>Current</h3>", "<h2>Maintenance</h2>", "<h3>Retention</h3>"}
	last := -1
	for _, want := range order {
		at := strings.Index(body, want)
		if at < 0 || at < last {
			t.Fatalf("%q at %d after %d", want, at, last)
		}
		last = at
	}
}

func TestFormHiddenFieldsUnchanged(t *testing.T) {
	c := newConsole(t, func(d *publish.Deps) { d.Static = staticStub{} })
	pages := c.signedPages(t)
	want := map[string][][]string{
		"overview":  {{"csrf_token"}, {"csrf_token"}, {"csrf_token"}, {"csrf_token"}, {"csrf_token"}, {"csrf_token"}},
		"confirm":   {{"csrf_token"}, {"csrf_token", "confirm"}},
		"retention": {{"csrf_token"}, {"csrf_token", "confirm", "fingerprint"}},
		"republish": {{"csrf_token"}, {"csrf_token", "confirm"}},
	}
	for name, forms := range want {
		var got [][]string
		for _, f := range formBlock.FindAllString(pages[name].body, -1) {
			var names []string
			for _, m := range hiddenRe.FindAllStringSubmatch(f, -1) {
				names = append(names, m[1])
			}
			got = append(got, names)
		}
		if len(got) != len(forms) {
			t.Fatalf("%s: forms %v, want %v", name, got, forms)
		}
		for i := range forms {
			if !slices.Equal(got[i], forms[i]) {
				t.Fatalf("%s form %d: hidden %v, want %v", name, i, got[i], forms[i])
			}
		}
	}
}

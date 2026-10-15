package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
)

var formRe = regexp.MustCompile(`action="/manage/releases/(\d+)/(promote|yank)"`)

func forms(body, action string) []int64 {
	var ids []int64
	for _, m := range formRe.FindAllStringSubmatch(body, -1) {
		if m[2] == action {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			ids = append(ids, id)
		}
	}
	return ids
}

func TestOverviewShowsCurrentAndPromotable(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	r := c.signedIn("ops").get("/manage/production/umbree")
	if r.status != http.StatusOK {
		t.Fatalf("HTTP %d", r.status)
	}
	for _, label := range []string{"current", "new"} {
		if !strings.Contains(r.body, c.row(label).Stamp) {
			t.Fatalf("the overview does not show the %s row %s", label, c.row(label).Stamp)
		}
	}
	if got := forms(r.body, "promote"); !slices.Equal(got, []int64{c.rows["new"]}) {
		t.Fatalf("promote forms %v, want only row %d", got, c.rows["new"])
	}
}

func TestOverviewNoButtonForNotPromotable(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	for _, label := range []string{"below", "equal"} {
		if slices.Contains(forms(b.get("/manage/production/umbree").body, "promote"), c.rows[label]) {
			t.Fatalf("the %s row has a promote button", label)
		}
	}
	promotable, err := c.st.Promotable("umbree", "production")
	if err != nil || len(promotable) != 1 || promotable[0].ID != c.rows["new"] {
		t.Fatalf("the predicate itself: %v %v", promotable, err)
	}
	b.confirmed(actionPath(c.rows["new"], "promote"), b.confirmToken(actionPath(c.rows["new"], "promote")))
	if got := forms(b.get("/manage/production/umbree").body, "promote"); len(got) != 0 {
		t.Fatalf("with nothing newer than the mark, promote forms %v", got)
	}
	r := b.confirmed(actionPath(c.rows["below"], "promote"), b.confirmToken(actionPath(c.rows["below"], "promote")))
	if !strings.Contains(r.body, `data-result="failed"`) {
		t.Fatalf("the action refuses what the page does not offer: %s", r.body)
	}
	if st, _ := c.state("below"); st != "staged" {
		t.Fatalf("below is %s", st)
	}
}

func TestOverviewYankOnCurrentOnly(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	if got := forms(b.get("/manage/production/umbree").body, "yank"); !slices.Equal(got, []int64{c.rows["current"]}) {
		t.Fatalf("yank forms %v, want only the current row %d", got, c.rows["current"])
	}
	history := b.get("/manage/production/umbree/history").body
	promotable := c.promotableIDs()
	got := forms(history, "promote")
	slices.Sort(got)
	if !slices.Equal(forms(history, "yank"), []int64{c.rows["current"]}) || !slices.Equal(got, promotable) || len(formRe.FindAllString(history, -1)) != 1+len(promotable) {
		t.Fatalf("history yank %v promote %v, want yank on %d and promote on %v", forms(history, "yank"), got, c.rows["current"], promotable)
	}
	if !strings.Contains(historyRow(t, history, c.rows["current"]), actionPath(c.rows["current"], "yank")) {
		t.Fatal("the history yank form is outside the current row")
	}
}

func TestHistoryNewestFirstByVersion(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get("/manage/production/umbree/history").body
	last := -1
	for _, label := range []string{"new", "current", "equal", "below", "public", "expired"} {
		at := strings.Index(body, c.row(label).Stamp)
		if at < 0 || at < last {
			t.Fatalf("%s (%s) at %d, after %d: not newest-first by version", label, c.row(label).Stamp, at, last)
		}
		last = at
	}
}

func TestHistoryExpiredGrayed(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get("/manage/production/umbree/history").body
	for label := range c.rows {
		rv := c.row(label)
		tr := regexp.MustCompile(fmt.Sprintf(`<tr class="([^"]*)" id="row-%d"`, rv.ID)).FindStringSubmatch(body)
		if tr == nil {
			t.Fatalf("no row for %s", label)
		}
		if grayed := strings.Contains(tr[1], "expired"); grayed != (rv.State == "expired") {
			t.Fatalf("%s (%s) has classes %q", label, rv.State, tr[1])
		}
	}
}

var hrefRe = regexp.MustCompile(`href="(https?://[^"]+)"`)

func TestLinksOnlyForPublicRows(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	if err := c.st.Transition(c.rows["public"], "public", "yanked", c.now); err != nil {
		t.Fatal(err)
	}
	c.promoteDirect(c.stage("live", "6.6.6", 1))
	c.stage("next", "7.7.7", 1)
	b := c.signedIn("ops")
	manifest := publicBase + "/umbree/latest.json"
	for _, page := range []string{"/manage/production/umbree", "/manage/production/umbree/history"} {
		body := b.get(page).body
		manifests := 0
		for _, m := range hrefRe.FindAllStringSubmatch(body, -1) {
			if m[1] == manifest {
				manifests++
				continue
			}
			if !strings.Contains(m[1], c.row("live").Stamp) && !strings.Contains(m[1], c.row("current").Stamp) {
				t.Fatalf("%s links %s, which is not a public row", page, m[1])
			}
		}
		if manifests == 0 {
			continue
		}
		if cur, err := c.st.Current("umbree", "production"); err != nil || manifests != 1 || cur.ID != c.rows["live"] {
			t.Fatalf("%s: %d manifest links, current %v %v", page, manifests, cur, err)
		}
		if inner := currentBlock(t, page, body, c.rows["live"]); !strings.Contains(inner, `href="`+manifest+`"`) {
			t.Fatalf("%s: the manifest link is outside the current row", page)
		}
	}
	if _, ready := card(t, b.get("/manage/production/umbree").body, c.rows["next"]); hrefRe.MatchString(ready) {
		t.Fatalf("the ready card carries a link: %s", ready)
	}
	if !strings.Contains(b.get("/manage/production/umbree/history").body, publicBase+"/umbree/"+c.row("live").Stamp+"/") {
		t.Fatal("control: the public row has no link")
	}
}

func currentBlock(t *testing.T, page, body string, id int64) string {
	t.Helper()
	if strings.HasSuffix(page, "/history") {
		return historyRow(t, body, id)
	}
	_, inner := card(t, body, id)
	return inner
}

func TestLinksArePublicURLsNeverGatedKeys(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	for _, page := range []string{"/manage/production/umbree", "/manage/production/umbree/history"} {
		body := b.get(page).body
		if hit := contains(body, "umbree/production/", "X-Amz", "Signature=", "r2.cloudflarestorage"); hit != "" {
			t.Fatalf("%s carries %q", page, hit)
		}
		if n := strings.Count(body, publicBase+manifestPath); n > 1 || (n == 1 && !strings.Contains(currentBlock(t, page, body, c.rows["current"]), publicBase+manifestPath)) {
			t.Fatalf("%s: %d manifest links, or one outside the current row", page, n)
		}
		links := hrefRe.FindAllStringSubmatch(body, -1)
		if len(links) == 0 {
			t.Fatalf("%s has no download links", page)
		}
		for _, m := range links {
			u, err := url.Parse(m[1])
			if err != nil || !(strings.HasPrefix(m[1], publicBase+"/umbree/v") || m[1] == publicBase+"/umbree/latest.json") || u.RawQuery != "" {
				t.Fatalf("%s links %q", page, m[1])
			}
		}
		staged := []string{c.row("new").Stamp, c.row("below").Stamp, c.row("equal").Stamp}
		for _, m := range regexp.MustCompile(`href="([^"]*)"`).FindAllStringSubmatch(body, -1) {
			if hit := contains(m[1], append([]string{"X-Amz-", "?", "gated-console", "umbree/production/"}, staged...)...); hit != "" {
				t.Fatalf("%s href %q carries %q", page, m[1], hit)
			}
		}
	}
}

func TestUnauthenticatedCrawlLeaksNoStaged(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	var secrets []string
	for _, label := range []string{"new", "below", "equal"} {
		rv := c.row(label)
		secrets = append(secrets, rv.Stamp, rv.Version)
	}
	secrets = append(secrets, "umbree-darwin-arm64.zip", "umbree-linux-amd64.zip", "SHA256SUMS")
	paths := []string{"/", "/healthz", "/manage", "/manage/", "/manage/login", "/manage/login/totp",
		"/manage/production/umbree", "/manage/production/umbree/history", "/manage/production/umbreed",
		"/manage/static/site.css", "/manage/nope", "/downloads", "/umbree/latest.json", "/api/v1/releases/nonce",
		"/api/v1/releases/register", "/api/v1/releases/status", "/manage/logout"}
	for _, id := range c.rows {
		for _, action := range []string{"promote", "yank"} {
			paths = append(paths, actionPath(id, action), apiPath(id, action))
		}
	}
	b := c.browser()
	for _, p := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			r := b.do(method, p, url.Values{auth.CSRFField: {"x"}}, nil)
			dump := fmt.Sprint(r.header) + r.body
			if hit := contains(dump, secrets...); hit != "" {
				t.Fatalf("%s %s (HTTP %d) leaks %q", method, p, r.status, hit)
			}
		}
	}
	if r := b.get("/healthz"); r.status != http.StatusOK {
		t.Fatalf("control, /healthz: HTTP %d", r.status)
	}
}

func TestNoPublicPagesOrBadge(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.browser()
	for _, p := range []string{"/", "/downloads", "/verify", "/install", "/platforms", "/docs",
		"/badge/umbree.svg", "/umbree/badge-production.svg", "/umbree/latest.json", "/api/v1/manage/releases/production/versions"} {
		if r := b.get(p); r.status != http.StatusNotFound {
			t.Fatalf("GET %s: HTTP %d, want 404", p, r.status)
		}
	}
}

func TestCookieNamespace(t *testing.T) {
	c := newConsole(t)
	b := c.browser()
	r := b.passwordStep("ops")
	want := map[string]bool{auth.SessionCookie: true, auth.CSRFCookie: false}
	if len(r.set) != len(want) {
		t.Fatalf("cookies %v, want exactly %v", r.set, want)
	}
	for _, ck := range r.set {
		httpOnly, ok := want[ck.Name]
		if !ok || !strings.HasPrefix(ck.Name, "umbree_manage_") {
			t.Fatalf("cookie %q outside the namespace", ck.Name)
		}
		if ck.Path != "/manage" || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || ck.HttpOnly != httpOnly {
			t.Fatalf("cookie %s: path %q secure %v samesite %v httponly %v", ck.Name, ck.Path, ck.Secure, ck.SameSite, ck.HttpOnly)
		}
	}
}

func TestTemplatesParse(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	anon := c.browser()
	b := c.signedIn("ops")
	path := actionPath(c.rows["new"], "promote")
	token := b.confirmToken(path)
	pending := c.browser()
	for name, r := range map[string]reply{
		"login":    anon.get("/manage/login"),
		"totp":     pending.passwordStep("ops"),
		"overview": b.get("/manage/production/umbree"),
		"history":  b.get("/manage/production/umbree/history"),
		"confirm":  b.post(path, url.Values{auth.CSRFField: {b.csrf()}}),
		"progress": b.confirmed(path, token),
	} {
		if r.status != http.StatusOK || !strings.Contains(r.body, "</html>") {
			t.Fatalf("%s: HTTP %d, not a whole page:\n%s", name, r.status, r.body)
		}
	}
}

package web_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

const (
	seededDate   = "2026-10-09 12:00 UTC"
	manifestPath = "/umbree/latest.json"
)

var (
	fileRowRe  = regexp.MustCompile(`<tr><td class="file">(<a href="[^"]*">)?([^<]*)(?:</a>)?</td><td class="platform">([^<]*)</td><td class="size">([^<]*)</td><td class="sha">(.*?)</td></tr>`)
	dateCellRe = regexp.MustCompile(`<td class="date">([^<]*)</td>`)
	dateSpanRe = regexp.MustCompile(`<span class="dim date">(?:cut|promoted) ([^<]*)</span>`)
	dateValRe  = regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d UTC$`)
)

func card(t *testing.T, body string, id int64) (string, string) {
	t.Helper()
	m := regexp.MustCompile(fmt.Sprintf(`(?s)<section class="([^"]*)" id="row-%d">(.*?)</section>`, id)).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no card for row %d", id)
	}
	return m[1], m[2]
}

func hasCard(body string, id int64) bool {
	return strings.Contains(body, fmt.Sprintf(`" id="row-%d">`, id)) && strings.Contains(body, `<section class="card`)
}

func artifactsOf(t *testing.T, rv *store.ReleaseVersion) []register.Artifact {
	t.Helper()
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil {
		t.Fatal(err)
	}
	return arts
}

func TestCurrentCardRendersHeadline(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get("/manage/production/umbree").body
	rv := c.row("current")
	class, inner := card(t, body, rv.ID)
	if class != "card state-public" {
		t.Fatalf("current card class %q", class)
	}
	if at, h := strings.Index(body, `id="row-`+fmt.Sprint(rv.ID)+`"`), strings.Index(body, "<h3>Current</h3>"); h < 0 || h > at {
		t.Fatalf("the current card is not under <h3>Current</h3>: %d %d", h, at)
	}
	for _, want := range []string{`<b class="version">3.3.3</b>`, `<span class="stamp">` + rv.Stamp + `</span>`,
		`<span class="state state-public">public</span> <span class="state state-current">current</span>`,
		`<span class="dim date">promoted ` + seededDate + `</span>`} {
		if !strings.Contains(inner, want) {
			t.Fatalf("current card lacks %q:\n%s", want, inner)
		}
	}
}

func TestReadyCardRendersPromote(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get("/manage/production/umbree").body
	rv := c.row("new")
	class, inner := card(t, body, rv.ID)
	if class != "card state-staged" {
		t.Fatalf("ready card class %q", class)
	}
	for _, want := range []string{`<b class="version">4.4.4</b>`, `<span class="stamp">` + rv.Stamp + `</span>`,
		`<span class="state state-staged">staged</span>`, `<span class="dim date">cut ` + seededDate + `</span>`,
		fmt.Sprintf(`action="/manage/releases/%d/promote"`, rv.ID), "Promote 4.4.4"} {
		if !strings.Contains(inner, want) {
			t.Fatalf("ready card lacks %q:\n%s", want, inner)
		}
	}
	if strings.Contains(inner, "/yank") {
		t.Fatal("the ready card carries a yank form")
	}
}

func TestFileTablePlatformsAndColumns(t *testing.T) {
	c := newConsole(t)
	stamp := stampOf("0.9.0", 3)
	base := "umbreed/production/" + stamp + "/"
	sha := strings.Repeat("ab", 32)
	arts := []register.Artifact{{Key: base + register.SumsName, Size: 300, SHA256: sha}, {Key: base + register.MinisigName, Size: 200, SHA256: sha},
		{Key: base + "notes.zip", Size: 512, SHA256: sha}, {Key: base + "umbreed-linux-amd64.zip", Size: 7549747, SHA256: sha}}
	raw, _ := json.Marshal(arts)
	id, err := c.st.InsertStaged(store.ReleaseVersion{Component: "umbreed", Channel: "production", Version: "0.9.0", Stamp: stamp,
		ArtifactsJSON: string(raw), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName, CreatedAt: c.now})
	if err != nil {
		t.Fatal(err)
	}
	_, inner := card(t, c.signedIn("ops").get("/manage/production/umbreed").body, id)
	if got := theadRe.FindAllStringSubmatch(inner, -1); len(got) != 1 || got[0][1] != `<tr><th>FILE</th><th>PLATFORM</th><th>SIZE</th><th>SHA-256</th></tr>` {
		t.Fatalf("file table head %v", got)
	}
	want := [][4]string{{"notes.zip", "—", "512 B", "sha"}, {"umbreed-linux-amd64.zip", "linux/amd64", "7.2 MiB", "sha"},
		{register.SumsName, "checksums", "", ""}, {register.MinisigName, "signature", "", ""}}
	rows := fileRowRe.FindAllStringSubmatch(inner, -1)
	if len(rows) != len(want) {
		t.Fatalf("file rows %d, want %d:\n%s", len(rows), len(want), inner)
	}
	for i, w := range want {
		r := rows[i]
		if r[1] != "" || r[2] != w[0] || r[3] != w[1] || r[4] != w[2] || (r[5] != "") != (w[3] != "") {
			t.Fatalf("row %d %q, want %q", i, r[1:], w)
		}
	}
	c2 := newConsole(t)
	c2.seedHistory()
	_, cur := card(t, c2.signedIn("ops").get("/manage/production/umbree").body, c2.rows["current"])
	if !strings.Contains(cur, `<td class="platform">darwin/arm64</td>`) || !strings.Contains(cur, `<td class="platform">linux/amd64</td>`) {
		t.Fatalf("umbree archives lack their platforms:\n%s", cur)
	}
}

func TestShaFullValueAndCopyButton(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	rv := c.row("current")
	_, inner := card(t, c.signedIn("ops").get("/manage/production/umbree").body, rv.ID)
	zips := 0
	for _, a := range artifactsOf(t, rv) {
		if !strings.HasSuffix(a.Key, ".zip") {
			continue
		}
		zips++
		if len(a.SHA256) != 64 {
			t.Fatalf("fixture sha %q", a.SHA256)
		}
		want := `<td class="sha"><code title="` + a.SHA256 + `">` + a.SHA256 + `</code><button class="btn copy" type="button" data-copy hidden>copy</button></td>`
		if !strings.Contains(inner, want) {
			t.Fatalf("no full sha cell for %s:\n%s", a.Key, inner)
		}
	}
	if zips != 2 || strings.Count(inner, "data-copy") != 2 {
		t.Fatalf("zips %d, copy buttons %d", zips, strings.Count(inner, "data-copy"))
	}
}

func TestManifestLinkOnlyOnCurrent(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	body := b.get("/manage/production/umbree").body
	rv := c.row("current")
	_, cur := card(t, body, rv.ID)
	_, ready := card(t, body, c.rows["new"])
	manifest := `<a href="` + publicBase + manifestPath + `">manifest</a>`
	if strings.Count(body, publicBase+manifestPath) != 1 || !strings.Contains(cur, manifest) {
		t.Fatalf("manifest link not exactly once on the current card:\n%s", cur)
	}
	for _, name := range []string{register.SumsName, register.MinisigName} {
		u, _ := url.JoinPath(publicBase, "umbree", rv.Stamp, name)
		if !strings.Contains(cur, `href="`+u+`"`) {
			t.Fatalf("current card lacks the %s link", name)
		}
	}
	if strings.Contains(ready, "href=") {
		t.Fatalf("the ready card carries a link:\n%s", ready)
	}
	if strings.Contains(body, "/check") {
		t.Fatal("the overview links a check route")
	}
	for _, page := range []string{"/manage/production/umbree/history", "/manage/production/umbreed"} {
		if strings.Contains(b.get(page).body, "latest.json") {
			t.Fatalf("%s carries a manifest link", page)
		}
	}
}

func TestEmptyStagedBar(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	bar := `<p class="empty-bar">No staged cut waiting</p>`
	if body := b.get("/manage/production/umbree").body; strings.Contains(body, bar) || !hasCard(body, c.rows["new"]) {
		t.Fatal("control: a promotable row shows the empty bar or no card")
	}
	c.promoteDirect(c.rows["new"])
	body := b.get("/manage/production/umbree").body
	if !strings.Contains(body, bar) || strings.Contains(body, "/promote") {
		t.Fatalf("nothing staged: no empty bar, or a promote form:\n%s", body)
	}
	if strings.Count(body, `<section class="card`) != 1 {
		t.Fatalf("cards %d, want only the current one", strings.Count(body, `<section class="card`))
	}
}

func TestCardFormsUnchanged(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	body := b.get("/manage/production/umbree").body
	forms := map[int64]string{
		c.rows["new"]: fmt.Sprintf("<form class=\"act\" method=\"post\" action=\"/manage/releases/%d/promote\">\n      <input type=\"hidden\" name=\"csrf_token\" value=\"%s\">\n      <button class=\"btn primary\" type=\"submit\">Promote 4.4.4</button>\n    </form>", c.rows["new"], b.csrf()),
		c.rows["current"]: fmt.Sprintf("<form class=\"act\" method=\"post\" action=\"/manage/releases/%d/yank\">\n      <input type=\"hidden\" name=\"csrf_token\" value=\"%s\">\n      <button class=\"btn danger outline\" type=\"submit\">Yank 3.3.3</button>\n    </form>", c.rows["current"], b.csrf()),
	}
	for id, form := range forms {
		if _, inner := card(t, body, id); !strings.Contains(inner, form) {
			t.Fatalf("row %d card lacks the unchanged form %q:\n%s", id, form, inner)
		}
	}
}

func TestDateFormatNoWrap(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	b := c.signedIn("ops")
	overview := b.get("/manage/production/umbree").body
	history := b.get("/manage/production/umbree/history").body
	spans := dateSpanRe.FindAllStringSubmatch(overview, -1)
	cells := dateCellRe.FindAllStringSubmatch(history, -1)
	if len(spans) != 2 || len(cells) != 4*len(c.rows) {
		t.Fatalf("overview date spans %d, history date cells %d", len(spans), len(cells))
	}
	filled := 0
	for _, m := range append(spans, cells...) {
		if m[1] == "" {
			continue
		}
		filled++
		if !dateValRe.MatchString(m[1]) {
			t.Fatalf("date %q is not the one format", m[1])
		}
	}
	if filled < len(c.rows)+2 || strings.Contains(overview+history, "12:00Z") {
		t.Fatalf("filled dates %d, or the old format remains", filled)
	}
	css := b.get("/manage/static/site.css").body
	if !regexp.MustCompile(`\.date \{[^}]*white-space: nowrap`).MatchString(css) {
		t.Fatal("site.css does not keep dates on one line")
	}
}

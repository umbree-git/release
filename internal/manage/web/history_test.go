package web_test

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/register"
)

const (
	historyPath = "/manage/production/umbree/history"
	noValue     = "—"
	colLinks    = 7
	colAction   = 8
)

var (
	historyLabels = []string{"State", "Version", "Stamp", "Cut", "Promoted", "Yanked", "Artifacts", "Links", "Action"}
	cellRe        = regexp.MustCompile(`(?s)<td([^>]*)>(.*?)</td>`)
	cssRuleRe     = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	stateSpanRe   = regexp.MustCompile(`class="state state-`)
	sizedWidthRe  = regexp.MustCompile(`(?:^|[;\s])width:\s*([0-9.]+)(px|rem)`)
	minWidthRe    = regexp.MustCompile(`min-width:\s*([^;]+)`)
	percentRe     = regexp.MustCompile(`(?:^|[;\s])width:\s*([0-9.]+)%`)
)

type cell struct {
	attrs string
	inner string
}

func historyRow(t *testing.T, body string, id int64) string {
	t.Helper()
	m := regexp.MustCompile(fmt.Sprintf(`(?s)<tr class="[^"]*" id="row-%d">(.*?)</tr>`, id)).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no history row %d", id)
	}
	return m[1]
}

func rowCells(t *testing.T, body string, id int64) []cell {
	t.Helper()
	var out []cell
	for _, m := range cellRe.FindAllStringSubmatch(historyRow(t, body, id), -1) {
		out = append(out, cell{attrs: m[1], inner: strings.TrimSpace(m[2])})
	}
	if len(out) != len(historyLabels) {
		t.Fatalf("row %d has %d cells, want %d", id, len(out), len(historyLabels))
	}
	return out
}

type cssRule struct {
	selector string
	body     string
}

func cssRules(t *testing.T, c *console) []cssRule {
	t.Helper()
	css := c.browser().get("/manage/static/site.css").body
	var out []cssRule
	for _, m := range cssRuleRe.FindAllStringSubmatch(css, -1) {
		out = append(out, cssRule{selector: strings.TrimSpace(m[1]), body: m[2]})
	}
	if len(out) == 0 {
		t.Fatal("site.css has no rules")
	}
	return out
}

func ruleFor(t *testing.T, rules []cssRule, selector string) string {
	t.Helper()
	var bodies []string
	for _, r := range rules {
		for _, s := range strings.Split(r.selector, ",") {
			if strings.TrimSpace(s) == selector {
				bodies = append(bodies, r.body)
			}
		}
	}
	if len(bodies) == 0 {
		t.Fatalf("site.css has no %q rule", selector)
	}
	return strings.Join(bodies, ";")
}

func requireDecl(t *testing.T, rules []cssRule, selector string, decls ...string) {
	t.Helper()
	body := ruleFor(t, rules, selector)
	for _, d := range decls {
		if !strings.Contains(body, d) {
			t.Fatalf("%s lacks %q: %s", selector, d, body)
		}
	}
}

func actionFixture(c *console) {
	c.seedHistory()
	if err := c.st.Transition(c.rows["public"], "public", "yanked", c.now); err != nil {
		c.t.Fatal(err)
	}
	c.promoteDirect(c.stage("live", "6.6.6", 1))
	c.stage("next", "7.7.7", 1)
	c.stage("later", "8.8.8", 1)
}

func (c *console) promotableIDs() []int64 {
	c.t.Helper()
	var ids []int64
	for _, id := range c.rows {
		rv, err := c.st.Get(id)
		if err != nil {
			c.t.Fatal(err)
		}
		ok, err := c.st.IsPromotable(*rv)
		if err != nil {
			c.t.Fatal(err)
		}
		if ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

func promoteForm(id int64, version, csrf string) string {
	return fmt.Sprintf("<form class=\"act\" method=\"post\" action=\"/manage/releases/%d/promote\">\n      <input type=\"hidden\" name=\"csrf_token\" value=\"%s\">\n      <button class=\"btn primary\" type=\"submit\">Promote %s</button>\n    </form>", id, csrf, version)
}

func yankForm(id int64, version, csrf string) string {
	return fmt.Sprintf("<form class=\"act\" method=\"post\" action=\"/manage/releases/%d/yank\">\n      <input type=\"hidden\" name=\"csrf_token\" value=\"%s\">\n      <button class=\"btn danger outline\" type=\"submit\">Yank %s</button>\n    </form>", id, csrf, version)
}

func TestHistoryColumnLabels(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get(historyPath).body
	heads := theadRe.FindAllStringSubmatch(body, -1)
	if len(heads) != 1 {
		t.Fatalf("%d thead", len(heads))
	}
	var got []string
	for _, m := range thRe.FindAllStringSubmatch(heads[0][1], -1) {
		got = append(got, m[1])
	}
	if !slices.Equal(got, historyLabels) {
		t.Fatalf("labels %v, want %v", got, historyLabels)
	}
	for _, id := range c.rows {
		rowCells(t, body, id)
	}
}

func TestHistoryBadgesSideBySide(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get(historyPath).body
	for label, id := range c.rows {
		cells := rowCells(t, body, id)
		want := 1
		if label == "current" {
			want = 2
		}
		if !strings.HasPrefix(cells[0].inner, `<span class="pills">`) || len(stateSpanRe.FindAllString(cells[0].inner, -1)) != want {
			t.Fatalf("%s state cell %q", label, cells[0].inner)
		}
		if n := len(stateSpanRe.FindAllString(historyRow(t, body, id), -1)); n != want {
			t.Fatalf("%s: %d badges in the row, %d in the state cell", label, n, want)
		}
	}
	rules := cssRules(t, c)
	requireDecl(t, rules, ".pills", "display: inline-flex")
	requireDecl(t, rules, "table.history .pills", "flex-wrap: wrap")
}

func TestHistoryArtifactCount(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get(historyPath).body
	for label, id := range c.rows {
		want := strconv.Itoa(len(artifactsOf(t, c.row(label))))
		if got := rowCells(t, body, id)[6].inner; got != want || want == "0" {
			t.Fatalf("%s artifacts %q, want %s", label, got, want)
		}
	}
}

func TestHistoryLinksPublicNotPruned(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	if _, err := c.st.RecordPruned(c.rows["public"], "public", nil, "test-operator", c.now); err != nil {
		t.Fatal(err)
	}
	body := c.signedIn("ops").get(historyPath).body
	for label, id := range c.rows {
		cells := rowCells(t, body, id)
		for i, cl := range cells {
			if i != colLinks && hrefRe.MatchString(cl.inner) {
				t.Fatalf("%s cell %d carries a link: %s", label, i, cl.inner)
			}
		}
		links := cells[colLinks].inner
		if label != "current" {
			if links != noValue {
				t.Fatalf("%s links %q, want %s", label, links, noValue)
			}
			continue
		}
		rv := c.row(label)
		for _, want := range []string{rv.Stamp + "/" + register.SumsName, rv.Stamp + "/" + register.MinisigName, publicBase + manifestPath} {
			if !strings.Contains(links, want) {
				t.Fatalf("current links %q lack %s", links, want)
			}
		}
	}
	if n := strings.Count(body, publicBase+manifestPath); n != 1 {
		t.Fatalf("%d manifest links on history", n)
	}
}

func TestHistoryActionsMatchService(t *testing.T) {
	c := newConsole(t)
	actionFixture(c)
	b := c.signedIn("ops")
	body := b.get(historyPath).body
	promotable := c.promotableIDs()
	if !slices.Equal(promotable, []int64{c.rows["next"], c.rows["later"]}) {
		t.Fatalf("control: promotable %v", promotable)
	}
	got := forms(body, "promote")
	slices.Sort(got)
	if !slices.Equal(got, promotable) || !slices.Equal(forms(body, "yank"), []int64{c.rows["live"]}) {
		t.Fatalf("promote %v yank %v", got, forms(body, "yank"))
	}
	for label, id := range c.rows {
		action := rowCells(t, body, id)[colAction].inner
		rv := c.row(label)
		switch {
		case slices.Contains(promotable, id):
			if action != promoteForm(id, rv.Version, b.csrf()) {
				t.Fatalf("%s action %q", label, action)
			}
		case label == "live":
			if action != yankForm(id, rv.Version, b.csrf()) {
				t.Fatalf("%s action %q", label, action)
			}
		default:
			if action != "" {
				t.Fatalf("%s (%s) carries an action %q", label, rv.State, action)
			}
		}
	}
}

func TestConsoleLinksNoUnderline(t *testing.T) {
	c := newConsole(t)
	rules := cssRules(t, c)
	requireDecl(t, rules, "a", "text-decoration: none")
	requireDecl(t, rules, "a:hover", "text-decoration: underline")
	requireDecl(t, rules, "a:focus-visible", "outline:")
	for _, r := range rules {
		if !strings.Contains(r.body, "text-decoration: underline") {
			continue
		}
		for _, s := range strings.Split(r.selector, ",") {
			if !strings.Contains(s, ":hover") && !strings.Contains(s, ":focus-visible") {
				t.Fatalf("%q underlines at rest", s)
			}
		}
	}
}

func TestCopyButtonRightAligned(t *testing.T) {
	c := newConsole(t)
	rules := cssRules(t, c)
	requireDecl(t, rules, "table.files td.sha", "display: flex", "align-items: center")
	requireDecl(t, rules, "table.files td.sha code", "flex: 1 1 auto", "min-width: 0", "text-overflow: ellipsis")
	requireDecl(t, rules, "table.files td.sha .btn.copy", "flex: none", "margin-left: auto")
}

func TestHistoryNoHorizontalScrollRules(t *testing.T) {
	c := newConsole(t)
	c.seedHistory()
	body := c.signedIn("ops").get(historyPath).body
	if !strings.Contains(body, `<section class="panel history">`) || !strings.Contains(body, `<table class="history">`) {
		t.Fatal("history panel or table lacks the history class")
	}
	rules := cssRules(t, c)
	requireDecl(t, rules, ".panel.history", "overflow-x: visible")
	requireDecl(t, rules, "table.history", "table-layout: fixed", "width: 100%")
	total := 0.0
	for _, r := range rules {
		if strings.Contains(r.selector, "history") && regexp.MustCompile(`overflow(-x)?:\s*(auto|scroll)`).MatchString(r.body) {
			t.Fatalf("%s scrolls: %s", r.selector, r.body)
		}
		if m := minWidthRe.FindStringSubmatch(r.body); m != nil && strings.TrimSpace(m[1]) != "0" {
			t.Fatalf("%s sets min-width %s", r.selector, m[1])
		}
		for _, m := range sizedWidthRe.FindAllStringSubmatch(r.body, -1) {
			if n, _ := strconv.ParseFloat(m[1], 64); (m[2] == "rem" && n > 72) || (m[2] == "px" && n > 1152) {
				t.Fatalf("%s width %s%s exceeds the container", r.selector, m[1], m[2])
			}
		}
		if strings.HasPrefix(r.selector, "table.history th:nth-child(") {
			m := percentRe.FindStringSubmatch(r.body)
			if m == nil {
				t.Fatalf("%s has no percentage width", r.selector)
			}
			n, _ := strconv.ParseFloat(m[1], 64)
			total += n
		}
	}
	if total != 100 {
		t.Fatalf("history column widths sum to %v%%", total)
	}
}

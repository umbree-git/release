package web_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
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

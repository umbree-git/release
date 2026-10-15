package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestConsoleScriptStaticRules(t *testing.T) {
	js, err := fs.ReadFile(assets, "static/console.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "eval(", "new Function", "document.write", "//", "/*", ".style", "cssText", `setAttribute("style"`, "setAttribute('style'", "insertRule", "CSSStyleSheet", "var ", `"rail"`, `"step"`, `"step "`, `"rail "`} {
		if strings.Contains(string(js), bad) {
			t.Fatalf("console.js contains %q", bad)
		}
	}
	scriptShape(t, string(js))
	if regexp.MustCompile(`set(Timeout|Interval)\(\s*["'\x60]`).Match(js) {
		t.Fatal("console.js passes a string to a timer")
	}
	handler := regexp.MustCompile(`\son[a-z]+=`)
	names, err := fs.Glob(assets, "templates/*.html")
	if err != nil || len(names) == 0 {
		t.Fatalf("templates %v %v", names, err)
	}
	for _, name := range names {
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		if handler.Match(b) || strings.Contains(string(b), "style=") || strings.Contains(string(b), "<style") {
			t.Fatalf("%s carries an inline handler or style", name)
		}
		scripts := strings.Count(string(b), "<script")
		if name == "templates/layout.html" {
			if scripts != 1 || !strings.Contains(string(b), `<script src="/manage/static/console.js" defer></script>`) {
				t.Fatalf("layout.html scripts %d", scripts)
			}
		} else if scripts != 0 {
			t.Fatalf("%s carries a script", name)
		}
	}
}

var (
	edgeLine   = regexp.MustCompile(`(?m)^[^\s].*$`)
	globalSet  = regexp.MustCompile(`\b(window|globalThis)\.[A-Za-z_$][\w$]*\s*=[^=]`)
	clashClass = regexp.MustCompile(`\.(rail|step)\b`)
)

func scriptShape(t *testing.T, js string) {
	t.Helper()
	lines := edgeLine.FindAllString(js, -1)
	if len(lines) != 3 || lines[0] != `"use strict";` || lines[1] != "{" || lines[2] != "}" || !strings.HasPrefix(js, `"use strict";`) {
		t.Fatalf("console.js must be \"use strict\"; then one top-level block, got top-level lines %q", lines)
	}
	if globalSet.MatchString(js) {
		t.Fatal("console.js assigns to window or globalThis")
	}
}

func TestSiteCSSAvoidsSiblingClassNames(t *testing.T) {
	css, err := fs.ReadFile(assets, "static/site.css")
	if err != nil {
		t.Fatal(err)
	}
	if m := clashClass.Find(css); m != nil {
		t.Fatalf("site.css uses the sibling class %s", m)
	}
}

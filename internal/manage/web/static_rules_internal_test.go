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
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "eval(", "new Function", "document.write", "//", "/*", ".style", "cssText", `setAttribute("style"`, "setAttribute('style'", "insertRule", "CSSStyleSheet"} {
		if strings.Contains(string(js), bad) {
			t.Fatalf("console.js contains %q", bad)
		}
	}
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

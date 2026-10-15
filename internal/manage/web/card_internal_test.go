package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 512: "512 B", 1023: "1023 B", 1024: "1.0 KiB", 7549747: "7.2 MiB", 1 << 30: "1.0 GiB", 1048575: "1.0 MiB", 1073741823: "1.0 GiB"} {
		if got := humanSize(n); got != want {
			t.Fatalf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestConsoleScriptCopyRules(t *testing.T) {
	js, err := fs.ReadFile(assets, "static/console.js")
	if err != nil {
		t.Fatal(err)
	}
	scriptShape(t, string(js))
	for _, want := range []string{"navigator.clipboard.writeText", "[data-copy]", "hidden = false", "textContent"} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("console.js lacks %q", want)
		}
	}
	css, err := fs.ReadFile(assets, "static/site.css")
	if err != nil || !strings.Contains(string(css), ".card-head > .confirm-inline, .card-head > .inline-progress { flex-basis: 100%; }") {
		t.Fatalf("the confirm strip and step log are not full-width rows in the card head: %v", err)
	}
	for _, bad := range []string{"innerHTML", ".style", "var ", "execCommand"} {
		if strings.Contains(string(js), bad) {
			t.Fatalf("console.js contains %q", bad)
		}
	}
}

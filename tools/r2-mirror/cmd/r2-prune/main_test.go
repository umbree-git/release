package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtectHasNoImplicitDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tools", "retain-permanent"), []byte("umbree/v0.1.0.2026.01.01.deadbeef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, err := loadProtect("")
	if err != nil || len(got) != 0 {
		t.Fatalf("no --protect read %v, %v; want no pins and no file guessed", got, err)
	}
	explicit, err := loadProtect(filepath.Join(dir, "tools", "retain-permanent"))
	if err != nil || len(explicit) == 0 {
		t.Fatalf("keep-control, an explicit --protect: %v, %v", explicit, err)
	}
	for _, want := range []string{"catalog", "admin pin", "--execute"} {
		if !strings.Contains(protectUsage, want) {
			t.Errorf("--protect usage %q does not say %q", protectUsage, want)
		}
	}
}

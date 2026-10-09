package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
)

func TestSecretKeyRequiredAbsoluteCleanRegular(t *testing.T) {
	dir := t.TempDir()
	good := writeKey(t, dir)
	missing := filepath.Join(dir, "absent.key")
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "loose.key")
	if err := os.WriteFile(loose, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(dir, "short.key")
	if err := os.WriteFile(short, make([]byte, 16), 0o600); err != nil {
		t.Fatal(err)
	}
	refused := map[string]string{
		"empty":       "",
		"missing":     missing,
		"relative":    "totp.key",
		"unclean":     dir + "/./totp.key",
		"a directory": dir,
		"a symlink":   link,
		"group-read":  loose,
		"short":       short,
	}
	for name, path := range refused {
		if _, err := auth.LoadSealer(path); err == nil {
			t.Errorf("%s (%q) was accepted", name, path)
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("a missing key was created: %v", err)
	}
	if _, err := auth.LoadSealer(good); err != nil {
		t.Fatalf("control, a 0600 regular 32-byte file: %v", err)
	}
}

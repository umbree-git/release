package relconfig

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStamp proves Stamp() wraps version.Stamp with DateVersionScheme,
// producing "v<semver>.<dateUTC>.<sha8>" over a throwaway git repo.
//
// Unlike clawee's release repo, umbree's has no tools/version.sh oracle to
// shell out to (release-kit's `rkit build` is the only builder here — see
// the repo README); Task 4 adds tools/version.sh for the *distribute-only*
// path, not the stamp computation itself. So this asserts the stamp's shape
// directly instead of diffing against a shell script.
func TestStamp(t *testing.T) {
	src := t.TempDir()
	git := func(args ...string) {
		c := exec.Command("git", append([]string{"-C", src}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "c")

	semverFile := filepath.Join(t.TempDir(), "umbree")
	if err := os.WriteFile(semverFile, []byte("0.1.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Stamp(context.Background(), semverFile, src)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006.01.02")
	prefix := "v0.1.0." + today + "."
	if !strings.HasPrefix(got, prefix) {
		t.Fatalf("stamp %q missing prefix %q", got, prefix)
	}
	if sha := strings.TrimPrefix(got, prefix); len(sha) != 8 {
		t.Errorf("stamp %q sha8 suffix wrong length: %q", got, sha)
	}
}

func TestStampMissingSemverFile(t *testing.T) {
	src := t.TempDir()
	if _, err := Stamp(context.Background(), filepath.Join(src, "nope"), src); err == nil {
		t.Fatal("expected error for missing semver file")
	}
}

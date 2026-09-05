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

// TestStampFor pins the two channel stamps: beta carries the dotted ".beta."
// infix between semver and date, stable is exactly Stamp(), and an unknown
// channel is an error rather than a guessed shape.
func TestStampFor(t *testing.T) {
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

	versions := t.TempDir()
	if err := os.WriteFile(filepath.Join(versions, "umbree"), []byte("0.1.8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versions, "umbree.beta"), []byte("0.2.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC().Format("2006.01.02")

	beta, err := StampFor(context.Background(), "beta", versions, "umbree", src)
	if err != nil {
		t.Fatal(err)
	}
	if prefix := "v0.2.0.beta." + today + "."; !strings.HasPrefix(beta, prefix) || len(strings.TrimPrefix(beta, prefix)) != 8 {
		t.Fatalf("beta stamp %q, want prefix %q + sha8", beta, prefix)
	}
	stable, err := StampFor(context.Background(), "stable", versions, "umbree", src)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := Stamp(context.Background(), filepath.Join(versions, "umbree"), src); stable != want {
		t.Fatalf("stable StampFor = %q, want Stamp() %q", stable, want)
	}
	if strings.Contains(stable, ".beta.") {
		t.Fatalf("stable stamp %q carries .beta.", stable)
	}
	if _, err := StampFor(context.Background(), "bogus", versions, "umbree", src); err == nil {
		t.Fatal("unknown channel accepted")
	}

	// Byte parity with the shell: tools/version.sh --channel beta --stamp
	// over the same files must equal StampFor (relconfig's contract).
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "tools", "version.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "version.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(repo, "versions"), os.DirFS(versions)); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(repo, "tools", "version.sh"), "umbree", "--channel", "beta", "--stamp")
	cmd.Env = append(os.Environ(), "SRC_DIR="+src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version.sh: %v\n%s", err, out)
	}
	if shell := strings.TrimSpace(string(out)); shell != beta {
		t.Fatalf("shell beta stamp %q != StampFor %q", shell, beta)
	}
}

package static_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"umbree-release-r2-mirror/manifest"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/static"
)

const fixtureBase = "https://downloads.example.test"

var fixtureStamps = map[string]string{
	"umbree":  "v0.2.1.2026.10.09.0a1b2c3d",
	"umbreed": "v0.3.4.2026.10.09.4e5f6a7b",
}

var placeholderRe = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*@`)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func semverOf(stamp string) string {
	parts := strings.SplitN(strings.TrimPrefix(stamp, "v"), ".", 4)
	return strings.Join(parts[:3], ".")
}

func fixtureManifest(component, stamp string) manifest.Manifest {
	return manifest.Build(component, component+"/", semverOf(stamp), stamp,
		[]string{component + "-linux-amd64.zip", component + "-darwin-arm64.zip"}, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, body, 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellTree(t *testing.T) string {
	t.Helper()
	root, dir := repoRoot(t), t.TempDir()
	for _, p := range []string{"tools/gen-bootstraps.sh", "tools/gen-version-jsonp.sh", "tools/bootstrap.template.sh", "umbree-release.pub"} {
		copyFile(t, filepath.Join(root, p), filepath.Join(dir, p))
	}
	modules, err := filepath.Glob(filepath.Join(root, "tools/modules/*"))
	if err != nil || len(modules) == 0 {
		t.Fatalf("no modules under %s: %v", root, err)
	}
	for _, m := range modules {
		copyFile(t, m, filepath.Join(dir, "tools/modules", filepath.Base(m)))
	}
	for comp, stamp := range fixtureStamps {
		writeFile(t, filepath.Join(dir, "versions", comp), semverOf(stamp)+"\n")
		writeFile(t, filepath.Join(dir, "versions", comp+".stamp"), stamp+"\n")
	}
	writeFile(t, filepath.Join(dir, ".stub/go"), "#!/bin/sh\necho umbree\necho umbreed\n")
	if err := os.Chmod(filepath.Join(dir, ".stub/go"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runShell(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("sh", args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + filepath.Join(dir, ".stub") + ":/usr/local/bin:/usr/bin:/bin", "HOME=" + dir}, env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sh %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func manifestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for comp, stamp := range fixtureStamps {
		body, err := fixtureManifest(comp, stamp).Encode()
		if err != nil {
			t.Fatal(err)
		}
		mux.HandleFunc("GET /"+comp+"/latest.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func firstDifference(a, b []byte) string {
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	for i := 0; i < len(al) && i < len(bl); i++ {
		if al[i] != bl[i] {
			return fmt.Sprintf("line %d: go %q, shell %q", i+1, al[i], bl[i])
		}
	}
	return fmt.Sprintf("%d lines against %d", len(al), len(bl))
}

func TestStaticRenderMatchesShellGenerators(t *testing.T) {
	dir := shellTree(t)
	runShell(t, dir, []string{"UMBREE_R2_DOWNLOADS_BASE=" + fixtureBase}, "tools/gen-bootstraps.sh")
	srv := manifestServer(t)
	runShell(t, dir, []string{"UMBREE_R2_DOWNLOADS_BASE=" + srv.URL}, "tools/gen-version-jsonp.sh", "umbree", "umbreed")
	r := static.NewRenderer(release.Assets)
	for comp, stamp := range fixtureStamps {
		got, err := r.Bootstrap(comp, stamp, fixtureBase)
		if err != nil {
			t.Fatalf("%s: %v", comp, err)
		}
		want, err := os.ReadFile(filepath.Join(dir, comp, "install.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s/install.sh: the Go render differs from gen-bootstraps.sh (%s)", comp, firstDifference(got, want))
		}
		m := fixtureManifest(comp, stamp)
		js, err := static.VersionJS(comp, m.Version, m.Stamp)
		if err != nil {
			t.Fatalf("%s: %v", comp, err)
		}
		wantJS, err := os.ReadFile(filepath.Join(dir, comp, "version.js"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(js, wantJS) {
			t.Errorf("%s/version.js: Go %q, gen-version-jsonp.sh %q", comp, js, wantJS)
		}
	}
}

func TestStaticRenderMatchesCommittedBootstraps(t *testing.T) {
	root := repoRoot(t)
	r := static.NewRenderer(release.Assets)
	for _, comp := range []string{"umbree", "umbreed"} {
		floor, err := os.ReadFile(filepath.Join(root, "versions", comp+".stamp"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := r.Bootstrap(comp, strings.TrimSpace(string(floor)), "https://downloads.umbree.org")
		if err != nil {
			t.Fatalf("%s: %v", comp, err)
		}
		want, err := os.ReadFile(filepath.Join(root, comp, "install.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s/install.sh: the Go render differs from the committed bootstrap (%s)", comp, firstDifference(got, want))
		}
	}
}

func TestNoPlaceholderSurvives(t *testing.T) {
	dir := shellTree(t)
	runShell(t, dir, []string{"UMBREE_R2_DOWNLOADS_BASE=" + fixtureBase}, "tools/gen-bootstraps.sh")
	r := static.NewRenderer(release.Assets)
	template, err := fs.ReadFile(release.Assets, static.TemplatePath)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(placeholderRe.FindAll(template, -1)); n < 6 {
		t.Fatalf("the template carries %d placeholders; the check below would prove nothing", n)
	}
	for comp, stamp := range fixtureStamps {
		goRender, err := r.Bootstrap(comp, stamp, fixtureBase)
		if err != nil {
			t.Fatal(err)
		}
		shellRender, err := os.ReadFile(filepath.Join(dir, comp, "install.sh"))
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string][]byte{"go": goRender, "shell": shellRender} {
			if left := placeholderRe.FindAll(body, -1); len(left) > 0 {
				t.Errorf("%s render of %s leaves %q", name, comp, left)
			}
		}
	}
}

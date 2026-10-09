package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"umbree-release-r2-mirror/manifest"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/static"
)

const staticStamp = "v0.2.1.2026.10.09.0a1b2c3d"

var staticVars = map[string]string{"UMBREE_R2_ACCOUNT": "acct", "UMBREE_R2_CREDS": "/nonexistent",
	"UMBREE_R2_BUCKET": "public-static-cmd", "UMBREE_PUBLIC_BASE_URL": "https://downloads.example.test", "USER": "ops"}

func staticFixture(t *testing.T) *backendtest.Store {
	t.Helper()
	body, err := manifest.Build("umbree", "umbree/", "0.2.1", staticStamp, []string{"umbree-linux-amd64.zip"},
		time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)).Encode()
	if err != nil {
		t.Fatal(err)
	}
	public := backendtest.New("public-static-cmd")
	public.Seed("umbree/latest.json", body)
	newStaticSource = func(*options) (static.ManifestSource, error) { return public, nil }
	t.Cleanup(func() { newStaticSource = r2StaticSource })
	return public
}

func TestPublishStaticVerbByHand(t *testing.T) {
	staticFixture(t)
	data, dest := t.TempDir(), t.TempDir()
	r := invoke(t, staticVars, "publish-static", "umbree", "--data-dir", data, "--static-dest", dest)
	if r.code != 0 || !strings.Contains(r.stdout, staticStamp) || !strings.Contains(r.stdout, dest) {
		t.Fatalf("publish-static: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	body, err := os.ReadFile(filepath.Join(dest, "umbree", "install.sh"))
	if err != nil || !strings.Contains(string(body), "\nMIN_VERSION=\""+staticStamp+"\"\n") {
		t.Fatalf("the published bootstrap does not bake the manifest's stamp: %v", err)
	}
	for _, f := range []string{"umbree/version.js", "umbree-release.pub", "index.html"} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("%s not published: %v", f, err)
		}
	}
	for name, args := range map[string][]string{
		"no component":        {"publish-static", "--data-dir", data, "--static-dest", dest},
		"unknown component":   {"publish-static", "nosuch", "--data-dir", data, "--static-dest", dest},
		"two components":      {"publish-static", "umbree", "umbreed", "--data-dir", data, "--static-dest", dest},
		"no static dest":      {"publish-static", "umbree", "--data-dir", data},
		"remote dest, no key": {"publish-static", "umbree", "--data-dir", data, "--static-dest", "static-host:/srv/static"},
		"no data dir":         {"publish-static", "umbree", "--static-dest", dest},
	} {
		if r := invoke(t, staticVars, args...); r.code != exitUsage || !strings.Contains(r.stderr, "Usage:") {
			t.Errorf("%s: exit %d stderr %q, want usage", name, r.code, r.stderr)
		}
	}
	if r := invoke(t, staticVars, "publish-static", "umbree", "--data-dir", data, "--static-dest", "static-host:/srv/static"); !strings.Contains(r.stderr, "--static-ssh-key") {
		t.Fatalf("a remote dest without a key does not name --static-ssh-key: %q", r.stderr)
	}
	if r := invoke(t, nil, "--help"); !strings.Contains(r.stdout, "publish-static <component>") {
		t.Fatalf("the root help does not list publish-static: %q", r.stdout)
	}
}

func TestServeRefusesRemoteStaticDestWithoutKey(t *testing.T) {
	dir := t.TempDir()
	args := []string{"serve", "--data-dir", dir, "--secret-key", "/nonexistent/key", "--r2-account", "acct",
		"--r2-creds", "/nonexistent", "--gated-bucket", "gated", "--public-bucket", "public",
		"--public-base-url", "https://downloads.example.test", "--listen", "127.0.0.1:0"}
	r := invoke(t, nil, append(args, "--static-dest", "static-host:/srv/static")...)
	if r.code != exitUsage || !strings.Contains(r.stderr, "--static-ssh-key") {
		t.Fatalf("serve with a remote dest and no key: exit %d stderr %q", r.code, r.stderr)
	}
	r = invoke(t, nil, append(args, "--static-dest", "relative/dir")...)
	if r.code != exitUsage || !strings.Contains(r.stderr, "--static-dest") {
		t.Fatalf("serve with a relative dest: exit %d stderr %q", r.code, r.stderr)
	}
}

func TestServeWarnsWithoutStaticDest(t *testing.T) {
	vars := serveVars(t)
	delete(vars, "UMBREE_MANAGE_STATIC_DEST")
	r := invoke(t, vars, "serve", "--data-dir", t.TempDir(), "--listen", "127.0.0.1:0", "--trusted-proxy", "127.0.0.1")
	if r.code != 0 || strings.Count(r.stderr, "level=WARN") != 1 || !strings.Contains(r.stderr, "--static-dest") {
		t.Fatalf("serve with no static dest: exit %d stderr %q", r.code, r.stderr)
	}
	r = invoke(t, serveVars(t), "serve", "--data-dir", t.TempDir(), "--listen", "127.0.0.1:0", "--trusted-proxy", "127.0.0.1")
	if r.code != 0 || strings.Contains(r.stderr, "level=WARN") {
		t.Fatalf("control, a local static dest: exit %d stderr %q", r.code, r.stderr)
	}
}

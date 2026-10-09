package static_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/static"
)

type manifestSource struct {
	bodies map[string][]byte
	gets   int
}

func (m *manifestSource) Get(_ context.Context, key string, _ int64) ([]byte, error) {
	m.gets++
	body, ok := m.bodies[key]
	if !ok {
		return nil, backend.ErrNotFound
	}
	return body, nil
}

func sourceFor(t *testing.T, comps ...string) *manifestSource {
	t.Helper()
	src := &manifestSource{bodies: map[string][]byte{}}
	for _, comp := range comps {
		body, err := fixtureManifest(comp, fixtureStamps[comp]).Encode()
		if err != nil {
			t.Fatal(err)
		}
		src.bodies[comp+"/latest.json"] = body
	}
	return src
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func embedded(t *testing.T, path string) string {
	t.Helper()
	body, err := fs.ReadFile(release.Assets, path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRemoteDestRequiresSSHKey(t *testing.T) {
	if _, err := static.ParseDest("static-host:/srv/static", "", "/data/known_hosts"); !errors.Is(err, static.ErrNeedSSHKey) {
		t.Fatalf("a remote dest without a key: %v, want ErrNeedSSHKey", err)
	}
	for _, tc := range [][3]string{
		{"static-host:/srv/static", "relative/key", "/data/known_hosts"},
		{"static-host:/srv/static", "/keys/static", ""},
		{"static-host:relative", "/keys/static", "/data/known_hosts"},
		{"-oProxyCommand=x:/srv/static", "/keys/static", "/data/known_hosts"},
		{"static-host:/srv/a b", "/keys/static", "/data/known_hosts"},
		{"static-host:/srv/../etc", "/keys/static", "/data/known_hosts"},
		{"relative/dir", "", ""},
		{"/srv/../etc", "", ""},
	} {
		if d, err := static.ParseDest(tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("ParseDest%q = %+v, want a refusal", tc, d)
		}
	}
	if _, err := static.ParseDest("", "", ""); !errors.Is(err, static.ErrNoDest) {
		t.Fatalf("no dest: %v, want ErrNoDest", err)
	}
	remote, err := static.ParseDest("static-host:/srv/static", "/keys/static", "/data/known_hosts")
	if err != nil || !remote.Remote() {
		t.Fatalf("keep-control, remote with a key: %+v %v", remote, err)
	}
	local, err := static.ParseDest("/srv/static", "", "")
	if err != nil || local.Remote() {
		t.Fatalf("keep-control, a local dir needs no key: %+v %v", local, err)
	}
}

func TestStaticRefusesWithoutDest(t *testing.T) {
	src := sourceFor(t, "umbree")
	p := &static.Publisher{Assets: release.Assets, Source: src, DownloadsBase: fixtureBase}
	if _, err := p.Publish(context.Background(), "umbree"); !errors.Is(err, static.ErrNoDest) {
		t.Fatalf("no dest: %v, want ErrNoDest", err)
	}
	if src.gets != 0 {
		t.Fatalf("the manifest was read %d times before the refusal", src.gets)
	}
}

func TestStaticDestLocalDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "umbreed", "install.sh"), "the other component's bootstrap\n")
	p := &static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase,
		Dest: static.Dest{Dir: dir}}
	summary, err := p.Publish(context.Background(), "umbree")
	if err != nil {
		t.Fatal(err)
	}
	stamp := fixtureStamps["umbree"]
	if !strings.Contains(summary, stamp) || !strings.Contains(summary, dir) {
		t.Fatalf("summary %q names neither the stamp nor the dest", summary)
	}
	want, err := static.NewRenderer(release.Assets).Bootstrap("umbree", stamp, fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "umbree", "install.sh")); got != string(want) {
		t.Fatal("the published install.sh is not the render for the manifest's stamp")
	}
	if got := readFile(t, filepath.Join(dir, "umbree", "version.js")); !strings.Contains(got, `"stamp":"`+stamp+`"`) {
		t.Fatalf("version.js %q", got)
	}
	if readFile(t, filepath.Join(dir, "umbree-release.pub")) != embedded(t, static.PubkeyPath) {
		t.Fatal("the pubkey is not the embedded one")
	}
	if readFile(t, filepath.Join(dir, "index.html")) != embedded(t, static.SitePath) {
		t.Fatal("index.html is not the embedded site page")
	}
	if got := readFile(t, filepath.Join(dir, "umbreed", "install.sh")); got != "the other component's bootstrap\n" {
		t.Fatalf("the other component's bootstrap changed: %q", got)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "umbree", ".*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
	if _, err := (&static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase,
		Dest: static.Dest{Dir: filepath.Join(dir, "absent")}}).Publish(context.Background(), "umbree"); err == nil {
		t.Fatal("a dest dir that does not exist was created rather than refused")
	}
}

func TestStaticDestScpStubbed(t *testing.T) {
	var name string
	var args []string
	var staged map[string]string
	run := func(_ context.Context, n string, a ...string) ([]byte, error) {
		name, args, staged = n, a, map[string]string{}
		for _, src := range a[len(a)-4 : len(a)-1] {
			_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					rel, _ := filepath.Rel(filepath.Dir(src), p)
					staged[rel] = readFile(t, p)
				}
				return nil
			})
		}
		return nil, nil
	}
	dest, err := static.ParseDest("static-host:/srv/static", "/keys/static", "/data/known_hosts")
	if err != nil {
		t.Fatal(err)
	}
	p := &static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase, Dest: dest, Run: run}
	if _, err := p.Publish(context.Background(), "umbree"); err != nil {
		t.Fatal(err)
	}
	if name != "scp" {
		t.Fatalf("ran %q, want scp", name)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"-i /keys/static", "-o BatchMode=yes", "-o IdentitiesOnly=yes",
		"-o StrictHostKeyChecking=accept-new", "-o UserKnownHostsFile=/data/known_hosts", "-r", "-q"} {
		if !strings.Contains(joined, want) {
			t.Errorf("scp argv %q lacks %q", joined, want)
		}
	}
	if last := args[len(args)-1]; last != "static-host:/srv/static/" {
		t.Fatalf("scp target %q", last)
	}
	keys := make([]string, 0, len(staged))
	for k := range staged {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"index.html", "umbree-release.pub", "umbree/install.sh", "umbree/version.js"}; !slices.Equal(keys, want) {
		t.Fatalf("staged %v, want %v", keys, want)
	}
}

func TestStaticRefusesBadManifest(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]*manifestSource{
		"absent":         {bodies: map[string][]byte{}},
		"not json":       {bodies: map[string][]byte{"umbree/latest.json": []byte("{")}},
		"other comp":     {bodies: map[string][]byte{"umbree/latest.json": sourceFor(t, "umbreed").bodies["umbreed/latest.json"]}},
		"no stamp shape": {bodies: map[string][]byte{"umbree/latest.json": []byte(`{"component":"umbree","version":"0.2.1","stamp":"v0.2.1.beta.2026.10.09.0a1b2c3d"}`)}},
	} {
		p := &static.Publisher{Assets: release.Assets, Source: src, DownloadsBase: fixtureBase, Dest: static.Dest{Dir: dir}}
		if _, err := p.Publish(context.Background(), "umbree"); err == nil {
			t.Errorf("%s: published", name)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a refused manifest still wrote %d entries", len(entries))
	}
}

func TestStaticScpFailureNamesItsOutput(t *testing.T) {
	dest, err := static.ParseDest("static-host:/srv/static", "/keys/static", "/data/known_hosts")
	if err != nil {
		t.Fatal(err)
	}
	failing := &static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase, Dest: dest,
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Permission denied (publickey)"), errors.New("exit status 1")
		}}
	if _, err := failing.Publish(context.Background(), "umbree"); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("a failed scp: %v, want its output in the error", err)
	}
}

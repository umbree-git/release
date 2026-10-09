package static_test

import (
	"context"
	"errors"
	"io/fs"
	"maps"
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
		{"a@-oProxyCommand:/srv/static", "/keys/static", "/data/known_hosts"},
		{"-user@static-host:/srv/static", "/keys/static", "/data/known_hosts"},
		{"a@b@static-host:/srv/static", "/keys/static", "/data/known_hosts"},
		{"static-host:/", "/keys/static", "/data/known_hosts"},
		{"/", "", ""},
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
	if d, err := static.ParseDest("deploy@static-host:/srv/static", "/keys/static", "/data/known_hosts"); err != nil || d.Host != "deploy@static-host" {
		t.Fatalf("keep-control, a user@host dest: %+v %v", d, err)
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

type batchCommand struct{ verb, from, to string }

func parseBatch(t *testing.T, path string) []batchCommand {
	t.Helper()
	var out []batchCommand
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, path)), "\n") {
		fields := strings.Fields(line)
		for i := range fields {
			fields[i] = strings.Trim(fields[i], `"`)
		}
		c := batchCommand{verb: fields[0]}
		if len(fields) > 1 {
			c.from = fields[1]
		}
		if len(fields) > 2 {
			c.to = fields[2]
		}
		out = append(out, c)
	}
	return out
}

func TestStaticDestRemoteUploadsThenRenames(t *testing.T) {
	var name string
	var args []string
	var batch []batchCommand
	uploaded := map[string]string{}
	run := func(_ context.Context, n string, a ...string) ([]byte, error) {
		name, args = n, a
		batch = parseBatch(t, a[slices.Index(a, "-b")+1])
		for _, c := range batch {
			if c.verb == "put" {
				uploaded[c.to] = readFile(t, c.from)
			}
		}
		return nil, nil
	}
	dest, err := static.ParseDest("deploy@static-host:/srv/static", "/keys/static", "/data/known_hosts")
	if err != nil {
		t.Fatal(err)
	}
	p := &static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase, Dest: dest, Run: run}
	if _, err := p.Publish(context.Background(), "umbree"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if name != "sftp" || args[len(args)-1] != "deploy@static-host" {
		t.Fatalf("ran %s %q, want sftp to deploy@static-host", name, joined)
	}
	for _, want := range []string{"-i /keys/static", "-o BatchMode=yes", "-o IdentitiesOnly=yes",
		"-o StrictHostKeyChecking=accept-new", "-o UserKnownHostsFile=/data/known_hosts"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sftp argv %q lacks %q", joined, want)
		}
	}
	final := map[string]string{}
	lastPut, firstRename := -1, len(batch)
	for i, c := range batch {
		switch c.verb {
		case "put":
			lastPut = i
			if base := filepath.Base(c.to); !strings.HasPrefix(base, ".") {
				t.Errorf("put %s lands on a served name, not a dot-temp", c.to)
			}
		case "rename":
			firstRename = min(firstRename, i)
			final[c.to] = uploaded[c.from]
		}
	}
	if lastPut < 0 || lastPut > firstRename {
		t.Fatalf("a rename precedes an upload, so a failed upload could follow a replaced file: %+v", batch)
	}
	want := []string{"/srv/static/index.html", "/srv/static/umbree-release.pub", "/srv/static/umbree/install.sh", "/srv/static/umbree/version.js"}
	keys := slices.Sorted(maps.Keys(final))
	if !slices.Equal(keys, want) {
		t.Fatalf("renamed into place %v, want %v", keys, want)
	}
	install, _ := static.NewRenderer(release.Assets).Bootstrap("umbree", fixtureStamps["umbree"], fixtureBase)
	if final["/srv/static/umbree/install.sh"] != string(install) {
		t.Fatal("the bytes renamed to install.sh are not the render")
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

func TestStaticRemoteFailureNamesItsOutput(t *testing.T) {
	dest, err := static.ParseDest("static-host:/srv/static", "/keys/static", "/data/known_hosts")
	if err != nil {
		t.Fatal(err)
	}
	failing := &static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase, Dest: dest,
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Permission denied (publickey)"), errors.New("exit status 1")
		}}
	if _, err := failing.Publish(context.Background(), "umbree"); err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("a failed upload: %v, want its output in the error", err)
	}
}

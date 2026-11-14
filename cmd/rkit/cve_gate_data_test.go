package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/relconfig"
)

const gateHeader = "component\tkey\tvalue\n"

func gateFixture(rows ...string) string {
	return gateHeader + strings.Join(rows, "\n") + "\n"
}

func moduleSource(t *testing.T, modDirs ...string) string {
	t.Helper()
	src := t.TempDir()
	for _, d := range modDirs {
		mustWriteFile(t, filepath.Join(src, d, "go.mod"), "module example.com/m\n")
	}
	return src
}

func writeGateRepo(t *testing.T, content string) string {
	t.Helper()
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, cveGateFile), content)
	return repo
}

func copyRealGateFile(t *testing.T, repo string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(realRepoRoot(t), cveGateFile))
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(repo, cveGateFile), string(data))
}

func TestCVEGateFileArgsEqualTheCutsFormerArgv(t *testing.T) {
	repo := realRepoRoot(t)
	for _, comp := range relconfig.Components {
		src := moduleSource(t, ".")
		dist := filepath.Join(t.TempDir(), "dist")
		got, err := cveGateArgs(buildOpts{Component: comp, RepoDir: repo, SrcDir: src}, dist)
		if err != nil {
			t.Fatalf("%s: %v", comp, err)
		}
		want := []string{"--report", filepath.Join(dist, "cve"), "--gowork", "off",
			"--go", src, "--waivers", filepath.Join(repo, "cve-waivers.tsv")}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: argv = %q, want %q", comp, got, want)
		}
	}
}

func TestCVEGateFileSourcesNameTheBrandCheckouts(t *testing.T) {
	d, err := loadCVEGateData(realRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"umbree": "cli/code/main", "umbreed": "daemon/code/main"}
	for comp, src := range want {
		if got := d.entries[comp].source; got != src {
			t.Fatalf("%s: source = %q, want %q (tools/cve-scan.sh's checkout)", comp, got, src)
		}
	}
}

func TestCVEGateFileComponentsMatchRkitComponents(t *testing.T) {
	d, err := loadCVEGateData(realRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.checkComponents(relconfig.Components); err != nil {
		t.Fatalf("committed cve-gate.tsv: %v", err)
	}
	cases := map[string]string{
		"missing": gateFixture("umbree\tsource\tcli/code/main", "umbree\tgo\t."),
		"extra": gateFixture("umbree\tsource\tcli/code/main", "umbree\tgo\t.",
			"umbreed\tsource\tdaemon/code/main", "umbreed\tgo\t.",
			"umbrex\tsource\tx/code/main", "umbrex\tgo\t."),
	}
	for name, content := range cases {
		d, err := parseCVEGateData(strings.NewReader(content))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if err := d.checkComponents(relconfig.Components); err == nil {
			t.Fatalf("%s: a component set unlike rkit components was accepted", name)
		}
	}
}

func TestCVEGateDriftRefusesUnlistedAndMissingModules(t *testing.T) {
	d, err := parseCVEGateData(strings.NewReader(gateFixture(
		"umbree\tsource\tcli/code/main", "umbree\tgo\t.", "umbree\tgo\tsub")))
	if err != nil {
		t.Fatal(err)
	}
	e := d.entries["umbree"]
	if err := e.checkGoModules(moduleSource(t, ".", "sub", "testdata/x", "vendor/y", ".git/z", "_old")); err != nil {
		t.Fatalf("listed modules plus go-ignored dirs: %v", err)
	}
	err = e.checkGoModules(moduleSource(t, ".", "sub", "extra/mod"))
	if err == nil || !strings.Contains(err.Error(), "extra/mod") {
		t.Fatalf("an unlisted go.mod: err = %v, want it named", err)
	}
	err = e.checkGoModules(moduleSource(t, "."))
	if err == nil || !strings.Contains(err.Error(), "sub") {
		t.Fatalf("a listed module with no go.mod: err = %v, want it named", err)
	}
}

func TestCVEGateFileKeepsRowOrderAndResolvesBases(t *testing.T) {
	d, err := parseCVEGateData(strings.NewReader(gateHeader + "# a comment\n\n" +
		"web\tsource\t.\nweb\tgowork\tgo.work\nweb\tgo\tapp\nweb\tjs-manifest\tjs/m.tsv\n" +
		"web\tjs-root\tjs\nweb\tvendor-dir\tjs/v1\nweb\tvendor-dir\tjs/v2\nweb\twaivers\tw.tsv\n"))
	if err != nil {
		t.Fatal(err)
	}
	got := d.entries["web"].args("/repo", "/src")
	want := []string{"--gowork", "/src/go.work", "--go", "/src/app", "--js-manifest", "/src/js/m.tsv",
		"--js-root", "/src/js", "--vendor-dir", "/src/js/v1", "--vendor-dir", "/src/js/v2", "--waivers", "/repo/w.tsv"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestCVEGateFileRefusals(t *testing.T) {
	cases := []struct{ name, content, want string }{
		{"unknown key", gateFixture("umbree\tsource\tcli", "umbree\tchannel-skip\tbeta"), "unknown key"},
		{"dotdot go", gateFixture("umbree\tsource\tcli", "umbree\tgo\t../other"), ".."},
		{"inner dotdot", gateFixture("umbree\tsource\tcli", "umbree\tgo\ta/../../b"), ".."},
		{"dotdot source", gateFixture("umbree\tsource\t../elsewhere"), ".."},
		{"dotdot waivers", gateFixture("umbree\tsource\tcli", "umbree\twaivers\t../w.tsv"), ".."},
		{"absolute go", gateFixture("umbree\tsource\tcli", "umbree\tgo\t/etc"), "absolute"},
		{"absolute source", gateFixture("umbree\tsource\t/srv/cli"), "absolute"},
		{"absolute gowork", gateFixture("umbree\tsource\tcli", "umbree\tgowork\t/w/go.work"), "absolute"},
		{"no source", gateFixture("umbree\tgo\t."), "no source"},
		{"two sources", gateFixture("umbree\tsource\tcli", "umbree\tsource\tcli2"), "source"},
		{"two waivers", gateFixture("umbree\tsource\tcli", "umbree\twaivers\ta", "umbree\twaivers\tb"), "waivers"},
		{"vendor before manifest", gateFixture("umbree\tsource\tcli", "umbree\tvendor-dir\tv"), "js-manifest"},
		{"bad header", "comp\tkey\tvalue\numbree\tsource\tcli\n", "header"},
		{"no header", "", "header"},
		{"two fields", gateFixture("umbree\tsource"), "3 fields"},
		{"four fields", gateFixture("umbree\tsource\tcli\textra"), "3 fields"},
		{"empty value", gateFixture("umbree\tsource\t"), "empty"},
		{"trailing dotdot", gateFixture("umbree\tsource\tcli", "umbree\tgo\tfoo/.."), ".."},
		{"backslash dotdot", gateFixture("umbree\tsource\tcli", "umbree\tgo\tfoo\\..\\bar"), ".."},
		{"empty key", gateFixture("umbree\tsource\tcli", "umbree\t\t."), "unknown key"},
		{"empty component", gateFixture("umbree\tsource\tcli", "\tgo\t."), "empty"},
		{"doubled tab", gateFixture("umbree\tsource\tcli", "umbree\t\tgo\t."), "3 fields"},
		{"leading tab", gateFixture("\tumbree\tsource\tcli"), "3 fields"},
		{"trailing tab", gateFixture("umbree\tsource\tcli\t"), "3 fields"},
		{"crlf row", gateHeader + "umbree\tsource\tcli\r\n", "carriage return"},
		{"crlf header", "component\tkey\tvalue\r\numbree\tsource\tcli\r\n", "carriage return"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseCVEGateData(strings.NewReader(c.content))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a refusal naming %q", err, c.want)
			}
		})
	}
}

func TestCVEGateFileAcceptsDotsThatAreNotAParentSegment(t *testing.T) {
	for _, v := range []string{"a..b", "..foo/bar", "...", "v1..2/x", "foo/...", ".hidden/x"} {
		d, err := parseCVEGateData(strings.NewReader(gateFixture("umbree\tsource\t"+v, "umbree\tgo\t"+v)))
		if err != nil {
			t.Fatalf("%q: refused: %v", v, err)
		}
		if got := d.entries["umbree"].source; got != v {
			t.Fatalf("%q: source = %q", v, got)
		}
	}
}

func TestCVEGateArgsRefusesAMissingFileOrComponent(t *testing.T) {
	src := moduleSource(t, ".")
	_, err := cveGateArgs(buildOpts{Component: "umbree", RepoDir: t.TempDir(), SrcDir: src}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), cveGateFile) {
		t.Fatalf("a missing %s: err = %v", cveGateFile, err)
	}
	repo := writeGateRepo(t, gateFixture("umbree\tsource\tcli/code/main", "umbree\tgo\t."))
	_, err = cveGateArgs(buildOpts{Component: "umbree", RepoDir: repo, SrcDir: src}, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "umbreed") {
		t.Fatalf("a file without umbreed: err = %v, want the mismatch named", err)
	}
}

func TestCVEScanRefusesDriftWithoutRunningTheGate(t *testing.T) {
	g := installStubGate(t, "0")
	repo := t.TempDir()
	copyRealGateFile(t, repo)
	src := moduleSource(t, ".", "tools/extra")
	code, out, _ := runScan(t, "--component", "umbree", "--repo", repo, "--src", src, "--report", t.TempDir())
	if code != 2 || !strings.HasPrefix(out, "umbree/umbree: cve: cannot-run · cve gate: ") || !strings.Contains(out, "tools/extra") {
		t.Fatalf("exit %d out %q", code, out)
	}
	if g.calledWith(t) != nil {
		t.Fatal("the gate ran against a source whose go.mod set drifted from cve-gate.tsv")
	}
}

func TestCVEScanRefusesAnUnknownKeyWithoutRunningTheGate(t *testing.T) {
	g := installStubGate(t, "0")
	repo := writeGateRepo(t, gateFixture("umbree\tsource\tcli/code/main", "umbree\tgo\t.", "umbree\tcommand\trm",
		"umbreed\tsource\tdaemon/code/main", "umbreed\tgo\t."))
	code, out, _ := runScan(t, "--component", "umbree", "--repo", repo, "--src", moduleSource(t, "."), "--report", t.TempDir())
	if code != 2 || !strings.Contains(out, "unknown key") || g.calledWith(t) != nil {
		t.Fatalf("exit %d out %q", code, out)
	}
}

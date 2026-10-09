package static_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/static"
)

const fixturePub = "untrusted comment: fixture key\nRWQfixtureKeyLine0123456789+/=\n"

func fixtureAssets(template string, modules map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		static.TemplatePath: {Data: []byte(template)},
		static.PubkeyPath:   {Data: []byte(fixturePub)},
		static.SitePath:     {Data: []byte("<html></html>\n")},
	}
	for name, body := range modules {
		fsys[static.ModulesDir+"/"+name+".sh"] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func moduleNamed(modules map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		body, ok := modules[name]
		if !ok {
			return nil, static.ErrMissingModule
		}
		return []byte(body), nil
	}
}

func TestRenderExpandsIncludesDroppingModuleHeaders(t *testing.T) {
	modules := map[string]string{"greet": "# module: greet\n# needs: helpers\nhello\n# since: 1\n# a plain comment goes\nworld"}
	got, err := static.ExpandIncludes([]byte("pre\n@INCLUDE:greet@\n  @INCLUDE:greet@\npost"), moduleNamed(modules))
	if err != nil {
		t.Fatal(err)
	}
	want := "pre\n# BEGIN greet\nhello\nworld\n# END greet\n  @INCLUDE:greet@\npost\n"
	if string(got) != want {
		t.Fatalf("expanded\n%q\nwant\n%q", got, want)
	}
	empty, err := static.ExpandIncludes([]byte(""), moduleNamed(modules))
	if err != nil || len(empty) != 0 {
		t.Fatalf("an empty template expands to %q, %v", empty, err)
	}
}

func TestRenderRefusesUnexpandedInclude(t *testing.T) {
	if _, err := static.ExpandIncludes([]byte("@INCLUDE:absent@\n"), moduleNamed(nil)); !errors.Is(err, static.ErrMissingModule) {
		t.Fatalf("a missing module: %v, want ErrMissingModule", err)
	}
	r := static.NewRenderer(fixtureAssets("COMP=@COMP@\n  @INCLUDE:greet@\n", map[string]string{"greet": "hi\n"}))
	if _, err := r.Bootstrap("umbree", fixtureStamps["umbree"], fixtureBase); !errors.Is(err, static.ErrUnexpandedInclude) {
		t.Fatalf("an indented @INCLUDE survives expansion: %v, want ErrUnexpandedInclude", err)
	}
	keep := static.NewRenderer(fixtureAssets("COMP=@COMP@\n@INCLUDE:greet@\n", map[string]string{"greet": "hi\n"}))
	if _, err := keep.Bootstrap("umbree", fixtureStamps["umbree"], fixtureBase); err != nil {
		t.Fatalf("keep-control, an anchored @INCLUDE: %v", err)
	}
}

func TestRenderFillsTestSeamWithNothing(t *testing.T) {
	r := static.NewRenderer(fixtureAssets("a\n@TEST_SEAM@\nb @CHANNEL@ @MIN_VERSION@ @DOWNLOADS_BASE@ @BRAND@ @brand@ @PUBKEY@\n", nil))
	got, err := r.Bootstrap("umbreed", fixtureStamps["umbreed"], fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	want := "a\n\nb stable " + fixtureStamps["umbreed"] + " " + fixtureBase + " UMBREE umbree RWQfixtureKeyLine0123456789+/=\n"
	if string(got) != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
	real, err := static.NewRenderer(release.Assets).Bootstrap("umbree", fixtureStamps["umbree"], fixtureBase)
	if err != nil {
		t.Fatal(err)
	}
	for _, seam := range []string{"UMBREE_TEST_ALLOW_HTTP", "UMBREE_DL_BASE:-", "--proto =http "} {
		if strings.Contains(string(real), seam) {
			t.Errorf("a served bootstrap carries the test seam's %q", seam)
		}
	}
}

func TestRenderRefusesBadFloorOrBase(t *testing.T) {
	r := static.NewRenderer(release.Assets)
	for _, tc := range []struct{ comp, floor, base string }{
		{"umbree", "v0.2.1.beta.2026.10.09.0a1b2c3d", fixtureBase},
		{"umbree", "0.2.1", fixtureBase},
		{"umbree", "", fixtureBase},
		{"umbree", fixtureStamps["umbree"], "http://downloads.example.test"},
		{"umbree", fixtureStamps["umbree"], "https://downloads.example.test/a&b"},
		{"umbree", fixtureStamps["umbree"], "https://downloads.example.test/a|b"},
		{"umbree", fixtureStamps["umbree"], ""},
		{"nosuch", fixtureStamps["umbree"], fixtureBase},
		{"umbree|x", fixtureStamps["umbree"], fixtureBase},
	} {
		if out, err := r.Bootstrap(tc.comp, tc.floor, tc.base); err == nil {
			t.Errorf("Bootstrap(%q, %q, %q) rendered %d bytes, want a refusal", tc.comp, tc.floor, tc.base, len(out))
		}
	}
	if _, err := r.Bootstrap("umbree", fixtureStamps["umbree"], fixtureBase); err != nil {
		t.Fatalf("keep-control: %v", err)
	}
}

func TestVersionJSRefusesUnsafeValues(t *testing.T) {
	for _, tc := range [][3]string{
		{"umbree", "0.2.1\"});alert(1);//", "v0.2.1.2026.10.09.0a1b2c3d"},
		{"umbree", "0.2.1", "v0.2.1 2026"},
		{"umbree", "", "v0.2.1.2026.10.09.0a1b2c3d"},
		{"nosuch", "0.2.1", "v0.2.1.2026.10.09.0a1b2c3d"},
	} {
		if js, err := static.VersionJS(tc[0], tc[1], tc[2]); err == nil {
			t.Errorf("VersionJS%q = %q, want a refusal", tc, js)
		}
	}
	js, err := static.VersionJS("umbree", "0.2.1", "v0.2.1.2026.10.09.0a1b2c3d")
	if err != nil || string(js) != `__umbreeVersion({"component":"umbree","version":"0.2.1","stamp":"v0.2.1.2026.10.09.0a1b2c3d"});`+"\n" {
		t.Fatalf("keep-control: %q, %v", js, err)
	}
}

const edgeModule = `# module: edge v1
# a whole-line comment
	# an indented one
#
code_one   # a trailing comment is not stripped
# shellcheck disable=SC2086  # a directive keeps its line
echo "a string that spans lines
# this line is string text, not a comment
done"
echo 'single quoted
# also text
done'
echo "escaped \" quote" x
# after an escaped quote, a comment again
n=$#
echo "${#n}" # trailing
set -- a \
  b
x=1 # a comment ending in a backslash does not continue \
# so this comment line can go
echo "dq \
# inside a continued double-quoted string, kept
"
# BEGIN shared inner
# END shared inner
last`

const edgeWant = `# BEGIN edge
code_one   # a trailing comment is not stripped
# shellcheck disable=SC2086
echo "a string that spans lines
# this line is string text, not a comment
done"
echo 'single quoted
# also text
done'
echo "escaped \" quote" x
n=$#
echo "${#n}" # trailing
set -- a \
  b
x=1 # a comment ending in a backslash does not continue \
echo "dq \
# inside a continued double-quoted string, kept
"
# BEGIN shared inner
# END shared inner
last
# END edge
`

func TestRenderStripsModuleComments(t *testing.T) {
	got, err := static.ExpandIncludes([]byte("#!/bin/sh\n# a template comment is the template's own\n@INCLUDE:edge@\n"), moduleNamed(map[string]string{"edge": edgeModule}))
	if err != nil {
		t.Fatal(err)
	}
	if want := "#!/bin/sh\n# a template comment is the template's own\n" + edgeWant; string(got) != want {
		t.Fatalf("expanded\n%s\nwant\n%s", got, want)
	}
}

func TestRenderRefusesCommentAfterContinuation(t *testing.T) {
	for name, body := range map[string]string{
		"continued then comment":          "set -- a \\\n# gone\necho \"n=$#\"\n",
		"continued then indented comment": "x \\\n\t# gone\ny\n",
	} {
		if _, err := static.ExpandIncludes([]byte("@INCLUDE:m@\n"), moduleNamed(map[string]string{"m": body})); !errors.Is(err, static.ErrModuleContinuation) {
			t.Errorf("%s: %v, want ErrModuleContinuation", name, err)
		}
	}
	for name, body := range map[string]string{
		"escaped backslash":       "x \\\\\n# goes\ny\n",
		"single-quoted backslash": "x '\\'\n# goes\ny\n",
		"continued code":          "x \\\n  y\n# goes\n",
		"comment ends in one":     "x # c \\\n# goes\ny\n",
	} {
		if _, err := static.ExpandIncludes([]byte("@INCLUDE:m@\n"), moduleNamed(map[string]string{"m": body})); err != nil {
			t.Errorf("keep-control, %s: %v", name, err)
		}
	}
}

func TestRenderRefusesModuleHeredoc(t *testing.T) {
	for name, body := range map[string]string{
		"heredoc":        "cat <<EOF\n# text\nEOF\n",
		"quoted heredoc": "cat <<'EOF'\nx\nEOF\n",
	} {
		if _, err := static.ExpandIncludes([]byte("@INCLUDE:m@\n"), moduleNamed(map[string]string{"m": body})); !errors.Is(err, static.ErrModuleHeredoc) {
			t.Errorf("%s: %v, want ErrModuleHeredoc", name, err)
		}
	}
	for name, body := range map[string]string{
		"quoted <<":  "echo \"a << b\"\n",
		"comment <<": "x # see <<EOF\n",
	} {
		if _, err := static.ExpandIncludes([]byte("@INCLUDE:m@\n"), moduleNamed(map[string]string{"m": body})); err != nil {
			t.Errorf("keep-control, %s: %v", name, err)
		}
	}
}

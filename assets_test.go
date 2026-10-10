package release_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	release "github.com/umbree-git/release"
)

func assertEmbeddedAsOnDisk(t *testing.T, path string) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s from the tree: %v", path, err)
	}
	got, err := fs.ReadFile(release.Assets, path)
	if err != nil {
		t.Fatalf("%s is not embedded: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the embedded %s differs from the tree's", path)
	}
}

func TestAssetsEmbedTemplateModulesPubkeySite(t *testing.T) {
	for _, path := range []string{"tools/bootstrap.template.sh", "umbree-release.pub", "site/index.html"} {
		assertEmbeddedAsOnDisk(t, path)
	}
	template, err := os.ReadFile("tools/bootstrap.template.sh")
	if err != nil {
		t.Fatal(err)
	}
	includes := regexp.MustCompile(`(?m)^@INCLUDE:([a-z0-9-]+)@$`).FindAllSubmatch(template, -1)
	if len(includes) == 0 {
		t.Fatal("the template names no @INCLUDE line, so this test proves nothing")
	}
	for _, m := range includes {
		assertEmbeddedAsOnDisk(t, "tools/modules/"+string(m[1])+".sh")
	}
	onDisk, err := filepath.Glob("tools/modules/*.sh")
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := fs.Glob(release.Assets, "tools/modules/*.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(onDisk, embedded) {
		t.Fatalf("embedded modules %v, the tree has %v", embedded, onDisk)
	}
}

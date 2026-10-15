package static_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/static"
)

func TestPublishWritesFavicon(t *testing.T) {
	dir := t.TempDir()
	p := &static.Publisher{Assets: release.Assets, Source: sourceFor(t, "umbree"), DownloadsBase: fixtureBase, Dest: static.Dest{Dir: dir}}
	if _, err := p.Publish(context.Background(), "umbree"); err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(release.Assets, "site/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "favicon.svg")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the published favicon.svg is not the embedded site/favicon.svg")
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("favicon.svg mode %v err %v, want 0644", st.Mode().Perm(), err)
	}
}

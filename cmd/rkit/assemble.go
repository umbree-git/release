package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/burrowee-git/release-kit/build"
	"github.com/burrowee-git/release-kit/pack"
)

// stageMigrations lists <srcDir>/install/migrations/ as zip contents under
// migrations/<name>: every *.sh plus ledger and component.conf — the layout
// burrowee's tools/payload.sh stages and the shared runner expects. A missing
// directory stages NOTHING and is not an error: that is what lets this land
// before the features that add a ladder (02 for umbree, 03 for umbreed).
// Anything else under install/ (README.md, tail_test.sh) never ships.
func stageMigrations(srcDir string) ([]pack.Content, error) {
	dir := filepath.Join(srcDir, "install", "migrations")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stageMigrations: %w", err)
	}
	var out []pack.Content
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".sh") || n == "ledger" || n == "component.conf" {
			out = append(out, pack.Content{Src: filepath.Join(dir, n), Name: "migrations/" + n})
		}
	}
	return out, nil
}

// assemble builds one zip per target: the component bin + install.sh + the
// migration ladder (migrations/…, when the tree carries one). Zips land at
// outRoot/stamp/<comp>-<os>-<arch>.zip in sorted-target order. Umbree has no
// dispatcher and no updater — the built bin is the only binary payload.
func assemble(comp, stamp, outRoot, installSh string, migrations []pack.Content, compArts []build.Artifact) ([]string, error) {
	byTarget := map[string][]pack.Content{}
	for _, a := range compArts {
		k := a.OS + "-" + a.Arch
		byTarget[k] = append(byTarget[k], pack.Content{Src: a.Path})
	}

	targets := make([]string, 0, len(byTarget))
	for k := range byTarget {
		targets = append(targets, k)
	}
	sort.Strings(targets)

	zipDir := filepath.Join(outRoot, stamp)
	if err := os.MkdirAll(zipDir, 0o755); err != nil {
		return nil, fmt.Errorf("assemble %s: %w", comp, err)
	}

	var zips []string
	for _, k := range targets {
		contents := append(byTarget[k], pack.Content{Src: installSh, Name: "install.sh"})
		contents = append(contents, migrations...)
		zp := filepath.Join(zipDir, fmt.Sprintf("%s-%s.zip", comp, k))
		if err := pack.Zip(pack.Spec{Out: zp, Contents: contents}); err != nil {
			return nil, fmt.Errorf("assemble %s: zip %s: %w", comp, k, err)
		}
		zips = append(zips, zp)
	}
	return zips, nil
}

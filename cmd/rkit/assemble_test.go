package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/burrowee-git/release-kit/build"
)

func TestAssembleFlatZip(t *testing.T) {
	root := t.TempDir()
	// fake per-target artifact.
	mk := func(p, data string) string {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(data), 0o755)
		return full
	}
	arts := []build.Artifact{
		{OS: "linux", Arch: "arm64", Path: mk("bin/umbree", "A")},
	}
	installSh := mk("install.sh", "#!/bin/sh\n")

	zips, err := assemble("umbree", "v0.1.90.x", root, installSh, nil, arts)
	if err != nil {
		t.Fatal(err)
	}
	if len(zips) != 1 {
		t.Fatalf("want 1 zip, got %d", len(zips))
	}
	if base := filepath.Base(zips[0]); base != "umbree-linux-arm64.zip" {
		t.Fatalf("zip name = %s", base)
	}
	// zip contains umbree, install.sh (flat).
	r, err := zip.OpenReader(zips[0])
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	want := []string{"install.sh", "umbree"}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

// zipNames lists the entries of one zip, sorted.
func zipNames(t *testing.T, zp string) []string {
	t.Helper()
	r, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

// TestAssembleStagesMigrations pins the ladder rule: every *.sh plus ledger
// and component.conf under <src>/install/migrations/ ride EVERY target's zip
// under migrations/, nothing else under install/ ships, and a tree with no
// migrations/ directory stages nothing and is not an error (that rule is
// what lets feature 04 land before 02/03 add a ladder).
func TestAssembleStagesMigrations(t *testing.T) {
	root := t.TempDir()
	mk := func(p, data string) string {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(data), 0o755)
		return full
	}
	arts := []build.Artifact{
		{OS: "linux", Arch: "arm64", Path: mk("bin/linux-arm64/umbreed", "A")},
		{OS: "darwin", Arch: "arm64", Path: mk("bin/darwin-arm64/umbreed", "B")},
	}
	installSh := mk("install.sh", "#!/bin/sh\n")
	src := filepath.Join(root, "src")
	for _, f := range []string{"ledger", "component.conf", "run.sh", "v0_1_to_v0_2.sh", "README.md"} {
		mk("src/install/migrations/"+f, f)
	}
	mk("src/install/tail_test.sh", "never ships")

	migs, err := stageMigrations(src)
	if err != nil {
		t.Fatal(err)
	}
	zips, err := assemble("umbreed", "v0.2.0.x", root, installSh, migs, arts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"install.sh", "migrations/component.conf", "migrations/ledger", "migrations/run.sh", "migrations/v0_1_to_v0_2.sh", "umbreed"}
	if len(zips) != 2 {
		t.Fatalf("want 2 zips, got %v", zips)
	}
	for _, zp := range zips {
		if got := zipNames(t, zp); !reflect.DeepEqual(got, want) {
			t.Errorf("%s entries = %v, want %v", filepath.Base(zp), got, want)
		}
	}

	// Without the directory: bin + install.sh only, no error.
	if err := os.RemoveAll(filepath.Join(src, "install", "migrations")); err != nil {
		t.Fatal(err)
	}
	migs, err = stageMigrations(src)
	if err != nil {
		t.Fatalf("absent migrations/ must not be an error: %v", err)
	}
	if len(migs) != 0 {
		t.Fatalf("absent migrations/ staged %v", migs)
	}
	zips, err = assemble("umbreed", "v0.2.0.y", root, installSh, migs, arts)
	if err != nil {
		t.Fatal(err)
	}
	for _, zp := range zips {
		if got := zipNames(t, zp); !reflect.DeepEqual(got, []string{"install.sh", "umbreed"}) {
			t.Errorf("%s entries = %v, want bin + install.sh only", filepath.Base(zp), got)
		}
	}
}

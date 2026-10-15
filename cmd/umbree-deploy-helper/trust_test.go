//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfNotRootOwnedRefuses(t *testing.T) {
	f := newFixture(t)
	f.h.rootUID = os.Getuid() + 1
	f.refuses("not root", "status")
}

func TestGroupWritableConfRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(filepath.Join(f.root, confRel), 0o664)
	f.refuses("group- or other-writable", "status")
}

func TestGroupWritableTemplateRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(filepath.Join(f.root, templateRel, serviceUnit), 0o666)
	f.refuses("group- or other-writable", "render")
}

func TestGroupWritableAncestorRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(filepath.Join(f.root, "etc"), 0o775)
	f.refuses("group- or other-writable", "status")
}

func TestOtherWritableRootRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(f.root, 0o757)
	f.refuses("group- or other-writable", "status")
}

func TestSymlinkedConfRefuses(t *testing.T) {
	f := newFixture(t)
	conf := filepath.Join(f.root, confRel)
	moved := filepath.Join(f.root, "etc", "elsewhere.conf")
	if err := os.Rename(conf, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, conf); err != nil {
		t.Fatal(err)
	}
	f.refuses("symlink", "status")
}

func (f *fixture) chmod(p string, mode os.FileMode) {
	if err := os.Chmod(p, mode); err != nil {
		f.t.Fatal(err)
	}
}

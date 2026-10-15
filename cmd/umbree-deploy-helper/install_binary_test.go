//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func (f *fixture) installed() string { return filepath.Join(f.root, binRel, binaryName) }

func TestInstallBinaryCopiesStagedBytes(t *testing.T) {
	f := newFixture(t)
	f.mustRun("install-binary")
	if got := f.read(f.installed()); got != "binary v1\n" {
		t.Fatalf("installed %q", got)
	}
	if fi, err := os.Stat(f.installed()); err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode: %v %v", fi, err)
	}
}

func TestSymlinkedStageDirRefuses(t *testing.T) {
	f := newFixture(t)
	link := f.stage + "-link"
	if err := os.Symlink(f.stage, link); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(f.root, confRel), replaceLine(f.read(filepath.Join(f.root, confRel)), "STAGE_DIR=", "STAGE_DIR="+link), 0o644)
	f.refuses("symlink", "install-binary")
	f.noInstall()
}

func TestStageDirOwnedByAnotherUserRefuses(t *testing.T) {
	f := newFixture(t)
	f.h.lookup = otherDeployUser(f.h.lookup)
	f.refuses("not the deploy user", "install-binary")
	f.noInstall()
}

func TestStageDirTooOpenRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(f.stage, 0o755)
	f.refuses("wider than 0750", "install-binary")
	f.noInstall()
}

func TestSymlinkedSourceRefuses(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.stage, stagedName)
	other := filepath.Join(f.stage, "other")
	if err := os.Rename(src, other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, src); err != nil {
		t.Fatal(err)
	}
	f.refuses("symlink", "install-binary")
	f.noInstall()
}

func TestDirectorySourceRefuses(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.stage, stagedName)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	f.mkdir(src, 0o755)
	f.refuses("not a regular file", "install-binary")
	f.noInstall()
}

func TestFifoSourceRefuses(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.stage, stagedName)
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(src, 0o644); err != nil {
		t.Fatal(err)
	}
	f.refuses("not a regular file", "install-binary")
	f.noInstall()
}

func TestSourceOwnedByAnotherUserRefuses(t *testing.T) {
	f := newFixture(t)
	var st syscall.Stat_t
	if err := syscall.Stat(filepath.Join(f.stage, stagedName), &st); err != nil {
		t.Fatal(err)
	}
	st.Uid++
	if err := checkSource(&st, os.Getuid()); err == nil {
		t.Fatal("a source owned by another uid passed checkSource")
	}
}

func TestHardLinkedSourceRefuses(t *testing.T) {
	f := newFixture(t)
	if err := os.Link(filepath.Join(f.stage, stagedName), filepath.Join(f.stage, "second")); err != nil {
		t.Fatal(err)
	}
	f.refuses("2 links", "install-binary")
	f.noInstall()
}

func TestSourceSwappedAfterCheckInstallsHeldBytes(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.stage, stagedName)
	f.h.afterSourceCheck = func() {
		swap := filepath.Join(f.stage, "swap")
		f.write(swap, "evil\n", 0o755)
		if err := os.Rename(swap, src); err != nil {
			t.Fatal(err)
		}
	}
	f.mustRun("install-binary")
	if got := f.read(f.installed()); got != "binary v1\n" {
		t.Fatalf("installed %q, want the bytes of the checked descriptor", got)
	}
}

func (f *fixture) noInstall() {
	f.t.Helper()
	if _, err := os.Lstat(f.installed()); err == nil {
		f.t.Fatal("a refused install-binary wrote the binary")
	}
}

//go:build linux

package main

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreOnFirstDeployStopsBeforeDisablingThenRemoves(t *testing.T) {
	f := newFixture(t)
	f.mustRun("install-binary")
	f.mustRun("render")
	f.calls = nil
	f.mustRun("restore")
	stop, disable := f.callIndex("systemctl stop "+serviceUnit), f.callIndex("systemctl disable "+serviceUnit)
	if stop < 0 || disable < 0 || stop > disable {
		t.Fatalf("restore calls %v: want stop before disable", f.calls)
	}
	for _, p := range []string{f.installed(), filepath.Join(f.h.unitDir, serviceUnit), filepath.Join(f.h.vhostDir, vhostName)} {
		if _, err := os.Lstat(p); err == nil {
			t.Fatalf("%s survived a first-deploy restore", p)
		}
	}
}

func TestRestorePutsPreviousCopiesBack(t *testing.T) {
	f := newFixture(t)
	f.write(f.installed(), "binary v0\n", 0o755)
	f.write(filepath.Join(f.h.unitDir, serviceUnit), "old unit\n", 0o644)
	f.mustRun("install-binary")
	f.mustRun("render")
	f.calls = nil
	f.mustRun("restore")
	if f.read(f.installed()) != "binary v0\n" || f.read(filepath.Join(f.h.unitDir, serviceUnit)) != "old unit\n" {
		t.Fatal("restore did not put the previous copies back")
	}
	if f.callIndex("systemctl stop "+serviceUnit) >= 0 {
		t.Fatalf("a redeploy restore stopped the service: %v", f.calls)
	}
}

func TestActivateRunsReloadEnableRestartNginxReload(t *testing.T) {
	f := newFixture(t)
	f.mustRun("activate")
	want := []string{"systemctl daemon-reload", "systemctl enable " + serviceUnit, "systemctl restart " + serviceUnit, "systemctl reload nginx"}
	if len(f.calls) != len(want) {
		t.Fatalf("activate calls %v", f.calls)
	}
	for i, c := range want {
		if f.calls[i] != c {
			t.Fatalf("activate call %d = %q, want %q", i, f.calls[i], c)
		}
	}
}

func treeDigest(t *testing.T, root string) map[string][32]byte {
	out := map[string][32]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		out[p] = sha256.Sum256(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNoVerbWritesTheHelperOrItsConfig(t *testing.T) {
	f := newFixture(t)
	before := treeDigest(t, filepath.Join(f.root, "etc"))
	helper := f.read(filepath.Join(f.root, helperRel))
	for _, v := range []string{"preflight", "install-binary", "render", "activate", "status", "restore"} {
		f.runVerb(v)
	}
	after := treeDigest(t, filepath.Join(f.root, "etc"))
	if len(before) != len(after) || f.read(filepath.Join(f.root, helperRel)) != helper {
		t.Fatal("a verb changed the helper or etc/manage")
	}
	for p, sum := range before {
		if after[p] != sum {
			t.Fatalf("a verb changed %s", p)
		}
	}
}

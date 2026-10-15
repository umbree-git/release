//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderWritesUnitsAndVhostFromTemplates(t *testing.T) {
	f := newFixture(t)
	f.mustRun("install-binary")
	f.mustRun("render")
	unit := f.read(filepath.Join(f.h.unitDir, serviceUnit))
	if !strings.Contains(unit, "User=umbree-release") || !strings.Contains(unit, "ExecStart="+f.h.binDir()+"/"+binaryName+" serve") {
		t.Fatalf("rendered unit:\n%s", unit)
	}
	vhost := f.read(filepath.Join(f.h.vhostDir, vhostName))
	if strings.Contains(vhost, "root  <STATIC_DIR>;") || !strings.Contains(vhost, "server 127.0.0.1:8788;") {
		t.Fatalf("rendered vhost keeps a placeholder or lost the upstream")
	}
	if f.callIndex("nginx -t") < 0 || !strings.HasPrefix(f.calls[len(f.calls)-1], "systemd-analyze verify") {
		t.Fatalf("render calls: %v", f.calls)
	}
}

func (f *fixture) badTemplate(old, replacement string) {
	p := filepath.Join(f.root, templateRel, serviceUnit)
	text := f.read(p)
	if !strings.Contains(text, old) {
		f.t.Fatalf("template lacks %q", old)
	}
	f.write(p, strings.Replace(text, old, replacement, 1), 0o644)
}

func (f *fixture) renderRefuses(want string) {
	f.t.Helper()
	f.refuses(want, "render")
	if _, err := os.Lstat(filepath.Join(f.h.unitDir, serviceUnit)); err == nil {
		f.t.Fatal("a refused render wrote the unit")
	}
	if len(f.calls) != 0 {
		f.t.Fatalf("a refused render ran %v", f.calls)
	}
}

func TestRenderRefusesUserRoot(t *testing.T) {
	f := newFixture(t)
	f.badTemplate("User=<SERVICE_USER>", "User=root")
	f.renderRefuses("User=root")
}

func TestRenderRefusesEmptyUser(t *testing.T) {
	f := newFixture(t)
	f.badTemplate("User=<SERVICE_USER>", "User=")
	f.renderRefuses("User=")
}

func TestRenderRefusesPrivilegedExecPrefixes(t *testing.T) {
	for _, prefix := range []string{"+", "!", "!!"} {
		f := newFixture(t)
		f.badTemplate("ExecStart=<MANAGE_BIN_DIR>", "ExecStart="+prefix+"<MANAGE_BIN_DIR>")
		f.renderRefuses("privileged prefix")
	}
}

func TestRenderRefusesPermissionsStartOnly(t *testing.T) {
	f := newFixture(t)
	f.badTemplate("Restart=on-failure", "Restart=on-failure\nPermissionsStartOnly=yes")
	f.renderRefuses("PermissionsStartOnly")
}

func TestRenderRefusesDirectiveOutsideTemplateSet(t *testing.T) {
	for _, d := range []string{"RootDirectory=/", "BindPaths=/etc", "ExecStartPre=/bin/true", "AmbientCapabilities=CAP_SYS_ADMIN"} {
		f := newFixture(t)
		f.badTemplate("Restart=on-failure", "Restart=on-failure\n"+d)
		f.renderRefuses("which its template set does not allow")
	}
}

func TestRenderNginxFailureRestoresPreviousFiles(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.h.vhostDir, vhostName), "old vhost\n", 0o644)
	f.mustRun("install-binary")
	f.failOn["nginx -t"] = true
	f.refuses("previous units and vhost are back", "render")
	if got := f.read(filepath.Join(f.h.vhostDir, vhostName)); got != "old vhost\n" {
		t.Fatalf("vhost after nginx -t failure: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(f.h.unitDir, serviceUnit)); err == nil {
		t.Fatal("a first-deploy unit survived the failed render")
	}
}

func TestRenderSystemdAnalyzeFailureRestoresPreviousFiles(t *testing.T) {
	f := newFixture(t)
	f.write(filepath.Join(f.h.unitDir, serviceUnit), "old unit\n", 0o644)
	f.mustRun("install-binary")
	f.failOn["systemd-analyze verify"] = true
	f.refuses("previous units and vhost are back", "render")
	if got := f.read(filepath.Join(f.h.unitDir, serviceUnit)); got != "old unit\n" {
		t.Fatalf("unit after systemd-analyze failure: %q", got)
	}
}

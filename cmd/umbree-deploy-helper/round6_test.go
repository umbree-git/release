//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRealRunnerNeverResolvesThroughPATH(t *testing.T) {
	f := newFixture(t)
	fake := filepath.Join(f.base(), "evil")
	f.mkdir(fake, 0o755)
	marker := filepath.Join(f.base(), "evil.ran")
	for _, name := range []string{"systemctl", "ss", "nginx", "systemd-analyze"} {
		f.write(filepath.Join(fake, name), "#!/bin/sh\ntouch "+marker+"\n", 0o755)
	}
	t.Setenv("PATH", fake+":"+os.Getenv("PATH"))
	f.h.run = runCommand
	f.runVerb("preflight")
	f.runVerb("activate")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a command on the caller's PATH ran")
	}
	log := f.read(filepath.Join(f.base(), "tools.log"))
	for _, want := range []string{"tools/ss -ltnp sport = :8788", "tools/systemctl restart " + serviceUnit} {
		if !strings.Contains(log, want) {
			t.Fatalf("the configured absolute tool did not run %q; log:\n%s", want, log)
		}
	}
}

func TestGroupWritableToolRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(f.tool(f.base(), "systemctl"), 0o775)
	f.refuses("group- or other-writable", "activate")
	if len(f.calls) != 0 {
		t.Fatalf("an untrusted tool still ran: %v", f.calls)
	}
}

func TestSymlinkedToolRefuses(t *testing.T) {
	f := newFixture(t)
	p := f.tool(f.base(), "nginx")
	if err := os.Rename(p, p+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p+".real", p); err != nil {
		t.Fatal(err)
	}
	f.refuses("symlink", "render")
}

func TestRelativeToolPathRefuses(t *testing.T) {
	f := newFixture(t)
	conf := filepath.Join(f.root, confRel)
	f.write(conf, replaceLine(f.read(conf), "SS=", "SS=ss"), 0o644)
	f.refuses("SS", "preflight")
}

func TestGroupWritableAncestorAboveRootRefuses(t *testing.T) {
	f := newFixture(t)
	f.chmod(f.base(), 0o775)
	f.refuses("group- or other-writable", "status")
}

func TestTimerKeyInServiceTemplateRefuses(t *testing.T) {
	f := newFixture(t)
	f.badTemplate("Restart=on-failure", "Restart=on-failure\nOnCalendar=daily")
	f.renderRefuses("which its template set does not allow")
}

func TestOversizedStagedBinaryRefuses(t *testing.T) {
	f := newFixture(t)
	if err := os.Truncate(filepath.Join(f.stage, stagedName), maxBinarySize+1); err != nil {
		t.Fatal(err)
	}
	f.refuses("larger than", "install-binary")
	f.noInstall()
}

func (f *fixture) deploy() {
	f.t.Helper()
	f.calls = nil
	for _, v := range []string{"install-binary", "render", "activate"} {
		f.mustRun(v)
	}
}

func TestUnchangedRenderWritesNothingAndReloadsNothing(t *testing.T) {
	f := newFixture(t)
	f.deploy()
	unit := filepath.Join(f.h.unitDir, serviceUnit)
	before, err := os.Stat(unit)
	if err != nil {
		t.Fatal(err)
	}
	f.deploy()
	after, err := os.Stat(unit)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("an unchanged render replaced the unit")
	}
	for _, c := range []string{"systemctl reload nginx", "systemctl daemon-reload", "nginx -t"} {
		if f.callIndex(c) >= 0 {
			t.Fatalf("an unchanged redeploy ran %q: %v", c, f.calls)
		}
	}
	if f.callIndex("systemctl restart "+serviceUnit) < 0 {
		t.Fatalf("the redeploy did not restart the service: %v", f.calls)
	}
	if exists(filepath.Join(f.h.varDir(), serviceUnit+".prev")) {
		t.Fatal("an unchanged unit kept a .prev")
	}
}

func TestChangedVhostAloneReloadsNginx(t *testing.T) {
	f := newFixture(t)
	f.deploy()
	p := filepath.Join(f.root, templateRel, vhostName)
	f.write(p, f.read(p)+"\n", 0o644)
	f.deploy()
	if f.callIndex("systemctl reload nginx") < 0 || f.callIndex("nginx -t") < 0 {
		t.Fatalf("a changed vhost did not reload nginx: %v", f.calls)
	}
	if f.callIndex("systemctl daemon-reload") >= 0 {
		t.Fatalf("an unchanged unit set ran daemon-reload: %v", f.calls)
	}
	if !exists(filepath.Join(f.h.varDir(), vhostName+".prev")) {
		t.Fatal("the replaced vhost has no .prev")
	}
}

//go:build linux

package main

import (
	"strings"
	"testing"
)

const (
	oldSSHeader = "State      Recv-Q Send-Q Local Address:Port               Peer Address:Port\n"
	oldSSRow    = "LISTEN     0      128    127.0.0.1:8788                     *:*                   users:((\"umbree-release-m\",pid=8736,fd=3))\n"
)

func oldHostFixture(t *testing.T, ss, mainPID, version string) *fixture {
	f := newFixture(t)
	f.replies["ss "] = ss
	f.replies["systemctl show"] = mainPID
	f.replies["systemctl --version"] = version
	return f
}

func TestPreflightPortHeldByServiceOnOldSS(t *testing.T) {
	f := oldHostFixture(t, oldSSHeader+oldSSRow, "MainPID=8736\n", "systemd 229\n+PAM +AUDIT\n")
	f.mustRun("preflight")
	if !strings.Contains(f.out.String(), "MainPID 8736, a redeploy") {
		t.Fatalf("preflight output %q, want the redeploy line", f.out.String())
	}
}

func TestPreflightRefusesForeignPidOnOldSS(t *testing.T) {
	f := oldHostFixture(t, oldSSHeader+oldSSRow, "MainPID=8737\n", "systemd 229\n")
	f.refuses("bound by pid 8736", "preflight")
}

func TestPreflightHeaderOnlySSIsFree(t *testing.T) {
	f := oldHostFixture(t, oldSSHeader, "MainPID=8737\n", "systemd 229\n")
	f.mustRun("preflight")
	for _, c := range f.calls {
		if strings.HasPrefix(c, "systemctl show") {
			t.Fatalf("a free port read the MainPID: %q", c)
		}
	}
}

func TestPreflightMalformedMainPIDFailsClosed(t *testing.T) {
	for _, reply := range []string{"MainPID=0\n", "", "8736\n", "MainPID=8736x\n"} {
		t.Run(reply, func(t *testing.T) {
			f := oldHostFixture(t, oldSSHeader+oldSSRow, reply, "systemd 229\n")
			f.refuses("bound by pid 8736", "preflight")
		})
	}
}

func TestPreflightArgvHasNoNewFlags(t *testing.T) {
	f := oldHostFixture(t, oldSSHeader+oldSSRow, "MainPID=8736\n", "systemd 229\n")
	f.mustRun("preflight")
	want := map[string]bool{"ss -ltnp sport = :8788": false, "systemctl show -p MainPID " + serviceUnit: false}
	for _, c := range f.calls {
		if strings.Contains(c, "-ltnpH") || strings.Contains(c, " -H") || strings.Contains(c, "--value") {
			t.Fatalf("call %q uses a flag newer than the host floor", c)
		}
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for c, seen := range want {
		if !seen {
			t.Fatalf("no call %q in %q", c, f.calls)
		}
	}
}

func TestPreflightSystemdFloor(t *testing.T) {
	f := oldHostFixture(t, oldSSHeader, "", "systemd 228\n")
	f.refuses("systemd 228", "preflight")
	f = oldHostFixture(t, oldSSHeader, "", "systemd 229\n")
	f.mustRun("preflight")
	if !strings.Contains(f.errOut.String(), "systemd 229 ignores ProtectSystem=strict") {
		t.Fatalf("stderr %q, want the < 232 warning", f.errOut.String())
	}
}

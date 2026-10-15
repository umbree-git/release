//go:build linux

package main

import (
	"path/filepath"
	"testing"
)

func TestPreflightPassesOnAFreePort(t *testing.T) {
	f := newFixture(t)
	f.mustRun("preflight")
}

func TestPreflightRefusesTrustedProxy(t *testing.T) {
	f := newFixture(t)
	env := filepath.Join(filepath.Dir(f.root), "etc", "manage.env")
	f.write(env, "UMBREE_MANAGE_LISTEN=127.0.0.1:8788\nUMBREE_MANAGE_TRUSTED_PROXY=127.0.0.1\n", 0o640)
	f.refuses("UMBREE_MANAGE_TRUSTED_PROXY", "preflight")
}

func TestPreflightRefusesUnwritableStaticDir(t *testing.T) {
	f := newFixture(t)
	f.chmod(filepath.Join(filepath.Dir(f.root), "static"), 0o555)
	f.refuses("not writable by umbree-release", "preflight")
}

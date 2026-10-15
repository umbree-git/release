//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	rootDir     = "/usr/local/umbree-release"
	confRel     = "etc/manage/helper.conf"
	confPath    = rootDir + "/" + confRel
	templateRel = "etc/manage/templates"
	varRel      = "var/manage-deploy"
	binRel      = "bin"
	helperRel   = "bin/umbree-deploy-helper"
	binaryName  = "umbree-release-manage"
	serviceUnit = "umbree-release-manage.service"
	vhostName   = "release.umbree.org.conf"
	execPath    = "/usr/sbin:/usr/bin:/sbin:/bin"
)

var unitNames = []string{serviceUnit, "umbree-release-manage-retain.service", "umbree-release-manage-retain.timer"}

type account struct {
	uid int
	gid int
}

type host struct {
	root             string
	unitDir          string
	vhostDir         string
	rootUID          int
	euid             func() int
	lookup           func(name string) (account, error)
	run              func(name string, args ...string) ([]byte, error)
	afterSourceCheck func()
	stdout           io.Writer
	stderr           io.Writer
}

func realHost() *host {
	return &host{
		root:     rootDir,
		unitDir:  "/etc/systemd/system",
		vhostDir: "/etc/nginx/sites-enabled",
		rootUID:  0,
		euid:     os.Geteuid,
		lookup:   lookupAccount,
		run:      runCommand,
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
}

func lookupAccount(name string) (account, error) {
	u, err := user.Lookup(name)
	if err != nil {
		return account{}, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return account{}, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return account{}, err
	}
	return account{uid: uid, gid: gid}, nil
}

func runCommand(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = []string{"PATH=" + execPath, "LC_ALL=C"}
	cmd.Dir = "/"
	return cmd.CombinedOutput()
}

func (h *host) path(rel string) string { return filepath.Join(h.root, rel) }

func (h *host) binDir() string { return h.path(binRel) }

func (h *host) varDir() string { return h.path(varRel) }

func (h *host) say(format string, a ...any) {
	fmt.Fprintf(h.stdout, "-> "+format+"\n", a...)
}

func (h *host) ok(format string, a ...any) {
	fmt.Fprintf(h.stdout, "ok "+format+"\n", a...)
}

func (h *host) warn(format string, a ...any) {
	fmt.Fprintf(h.stderr, "! "+format+"\n", a...)
}

func (h *host) fail(format string, a ...any) {
	fmt.Fprintf(h.stderr, "!! "+format+"\n", a...)
}

//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixture struct {
	t      *testing.T
	h      *host
	root   string
	stage  string
	calls  []string
	failOn map[string]bool
	out    bytes.Buffer
	errOut bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: filepath.Join(base, "root"), stage: filepath.Join(base, "stage"), failOn: map[string]bool{}}
	for _, d := range []string{"bin", "etc/manage/templates", "var/manage-deploy"} {
		f.mkdir(filepath.Join(f.root, d), 0o755)
	}
	for _, d := range []string{"units", "vhosts", "data", "etc"} {
		f.mkdir(filepath.Join(base, d), 0o755)
	}
	f.mkdir(f.stage, 0o750)
	f.mkdir(filepath.Join(base, "static"), 0o775)
	f.write(filepath.Join(f.root, helperRel), "helper", 0o755)
	f.mkdir(filepath.Join(base, "tools"), 0o755)
	for _, t := range []string{"systemctl", "nginx", "ss", "systemd-analyze"} {
		f.write(f.tool(base, t), "#!/bin/sh\necho \"$0 $*\" >> "+filepath.Join(base, "tools.log")+"\n", 0o755)
	}
	f.write(filepath.Join(base, "etc", "manage.env"), "UMBREE_MANAGE_LISTEN=127.0.0.1:8788\n", 0o640)
	f.write(filepath.Join(f.root, confRel), f.conf(base), 0o644)
	f.copyTemplates()
	f.write(filepath.Join(f.stage, stagedName), "binary v1\n", 0o755)
	uid, gid := os.Getuid(), os.Getgid()
	f.h = &host{
		root: f.root, unitDir: filepath.Join(base, "units"), vhostDir: filepath.Join(base, "vhosts"),
		rootUID: uid, euid: func() int { return 0 },
		lookup: func(name string) (account, error) {
			if name == "umbree-release" || name == "deployer" {
				return account{uid: uid, gid: gid}, nil
			}
			return account{}, errors.New("no such user")
		},
		run: f.record, stdout: &f.out, stderr: &f.errOut, top: base,
	}
	return f
}

func (f *fixture) conf(base string) string {
	return strings.Join([]string{
		"SERVICE_USER=umbree-release", "DEPLOY_USER=deployer",
		"STAGE_DIR=" + f.stage, "MANAGE_DATA_DIR=" + filepath.Join(base, "data"),
		"MANAGE_ENV_FILE=" + filepath.Join(base, "etc", "manage.env"),
		"STATIC_DIR=" + filepath.Join(base, "static"), "MANAGE_LISTEN=127.0.0.1:8788",
		"SYSTEMCTL=" + f.tool(base, "systemctl"), "NGINX=" + f.tool(base, "nginx"),
		"SS=" + f.tool(base, "ss"), "SYSTEMD_ANALYZE=" + f.tool(base, "systemd-analyze"), "",
	}, "\n")
}

func (f *fixture) tool(base, name string) string { return filepath.Join(base, "tools", name) }

func (f *fixture) base() string { return filepath.Dir(f.root) }

func (f *fixture) copyTemplates() {
	src := map[string]string{vhostName: "../../ops/nginx/" + vhostName}
	for _, u := range unitNames {
		src[u] = "../../ops/systemd/" + u
	}
	for name, p := range src {
		data, err := os.ReadFile(p)
		if err != nil {
			f.t.Fatal(err)
		}
		f.write(filepath.Join(f.root, templateRel, name), string(data), 0o644)
	}
}

func (f *fixture) record(name string, args ...string) ([]byte, error) {
	call := strings.TrimSpace(filepath.Base(name) + " " + strings.Join(args, " "))
	f.calls = append(f.calls, call)
	for key := range f.failOn {
		if strings.HasPrefix(call, key) {
			return []byte("simulated failure"), fmt.Errorf("exit status 1")
		}
	}
	return nil, nil
}

func (f *fixture) mkdir(p string, mode os.FileMode) {
	if err := os.MkdirAll(p, mode); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) write(p, content string, mode os.FileMode) {
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

func (f *fixture) runVerb(args ...string) int {
	f.out.Reset()
	f.errOut.Reset()
	return run(f.h, args)
}

func (f *fixture) mustRun(verb string) {
	f.t.Helper()
	if code := f.runVerb(verb); code != 0 {
		f.t.Fatalf("%s exit %d: %s", verb, code, f.errOut.String())
	}
}

func (f *fixture) refuses(want string, args ...string) {
	f.t.Helper()
	code := f.runVerb(args...)
	if code == 0 {
		f.t.Fatalf("%v succeeded, want a refusal containing %q", args, want)
	}
	if !strings.Contains(f.errOut.String(), want) {
		f.t.Fatalf("%v refused with %q, want it to contain %q", args, f.errOut.String(), want)
	}
}

func (f *fixture) callIndex(call string) int {
	for i, c := range f.calls {
		if c == call {
			return i
		}
	}
	return -1
}

//go:build linux

package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

var (
	envListen  = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?UMBREE_MANAGE_LISTEN=["']?([^"'\s]*)`)
	envProxy   = regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?UMBREE_MANAGE_TRUSTED_PROXY=`)
	pidField   = regexp.MustCompile(`pid=([0-9]+)`)
	systemdVer = regexp.MustCompile(`^systemd ([0-9]+)`)
)

func preflight(h *host, cfg *config) error {
	checks := []func(*host, *config) error{checkEnvFile, checkStage, checkStaticWritable, checkPort}
	for _, c := range checks {
		if err := c(h, cfg); err != nil {
			return err
		}
	}
	warnOldSystemd(h)
	h.ok("preflight passed")
	return nil
}

func checkEnvFile(h *host, cfg *config) error {
	data, err := os.ReadFile(cfg.envFile)
	if err != nil {
		return fmt.Errorf("env file: %w", err)
	}
	m := envListen.FindAllSubmatch(data, -1)
	got := ""
	if len(m) > 0 {
		got = string(m[len(m)-1][1])
	}
	if got != cfg.listen {
		return fmt.Errorf("UMBREE_MANAGE_LISTEN in %s is %q, helper.conf says %s", cfg.envFile, got, cfg.listen)
	}
	if envProxy.Match(data) {
		return fmt.Errorf("%s sets UMBREE_MANAGE_TRUSTED_PROXY, and the edge sends no X-Forwarded-For", cfg.envFile)
	}
	return nil
}

func checkStage(h *host, cfg *config) error {
	deploy, err := h.lookup(cfg.deployUser)
	if err != nil {
		return fmt.Errorf("DEPLOY_USER %s: %w", cfg.deployUser, err)
	}
	fd, err := openStageDir(cfg.stageDir, deploy.uid)
	if err != nil {
		return err
	}
	return syscall.Close(fd)
}

func checkStaticWritable(h *host, cfg *config) error {
	svc, err := h.lookup(cfg.serviceUser)
	if err != nil {
		return fmt.Errorf("SERVICE_USER %s: %w", cfg.serviceUser, err)
	}
	fi, err := os.Stat(cfg.staticDir)
	if err != nil {
		return fmt.Errorf("STATIC_DIR: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.IsDir() {
		return fmt.Errorf("STATIC_DIR %s is not a directory", cfg.staticDir)
	}
	perm := fi.Mode().Perm()
	if (int(st.Uid) == svc.uid && perm&0o200 != 0) || (int(st.Gid) == svc.gid && perm&0o020 != 0) {
		return nil
	}
	return fmt.Errorf("STATIC_DIR %s is not writable by %s — promote could not republish", cfg.staticDir, cfg.serviceUser)
}

func checkPort(h *host, cfg *config) error {
	out, err := h.run("ss", "-ltnpH", "sport = :"+cfg.port)
	if err != nil {
		return fmt.Errorf("ss: %v: %s", err, tail(out))
	}
	var pids []string
	for _, m := range pidField.FindAllStringSubmatch(string(out), -1) {
		pids = append(pids, m[1])
	}
	if len(pids) == 0 {
		return nil
	}
	sort.Strings(pids)
	mainOut, _ := h.run("systemctl", "show", "-p", "MainPID", "--value", serviceUnit)
	main := strings.TrimSpace(string(mainOut))
	if main != "" && main != "0" && pids[0] == main && pids[len(pids)-1] == main {
		h.ok("port %s held by %s (MainPID %s, a redeploy)", cfg.port, serviceUnit, main)
		return nil
	}
	return fmt.Errorf("port %s is bound by pid %s, not %s (MainPID %q)", cfg.port, strings.Join(pids, ","), serviceUnit, main)
}

func warnOldSystemd(h *host) {
	out, err := h.run("systemctl", "--version")
	m := systemdVer.FindSubmatch(out)
	if err != nil || m == nil {
		h.warn("could not read the systemd version; ProtectSystem=strict and ReadWritePaths= may be ignored")
		return
	}
	if v, _ := strconv.Atoi(string(m[1])); v < 232 {
		h.warn("systemd %d ignores ProtectSystem=strict and ReadWritePaths= — file ownership is the control", v)
	}
}

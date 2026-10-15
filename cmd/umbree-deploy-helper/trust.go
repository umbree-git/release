//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func templateNames() []string { return append(append([]string{}, unitNames...), vhostName) }

func (h *host) checkTrusted(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", p)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: no ownership information", p)
	}
	if int(st.Uid) != h.rootUID {
		return fmt.Errorf("%s is owned by uid %d, not root", p, st.Uid)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is group- or other-writable (%04o)", p, fi.Mode().Perm())
	}
	return nil
}

func within(base, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(base), p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func (h *host) checkInTree(p string) error {
	if !within(h.root, p) {
		return fmt.Errorf("%s is outside %s", p, h.root)
	}
	return h.checkChain(p)
}

func (h *host) checkChain(p string) error {
	top := filepath.Clean(h.top)
	if !within(top, p) {
		return fmt.Errorf("%s is outside %s", p, top)
	}
	for cur := p; ; cur = filepath.Dir(cur) {
		if err := h.checkTrusted(cur); err != nil {
			return err
		}
		if cur == top || cur == filepath.Dir(cur) {
			return nil
		}
	}
}

func (h *host) loadTrusted() (*config, error) {
	for _, rel := range []string{helperRel, confRel, templateRel, varRel} {
		if err := h.checkInTree(h.path(rel)); err != nil {
			return nil, err
		}
	}
	for _, name := range templateNames() {
		if err := h.checkTrusted(filepath.Join(h.path(templateRel), name)); err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(h.path(confRel))
	if err != nil {
		return nil, err
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return nil, err
	}
	for k, p := range cfg.tools {
		if err := h.checkChain(p); err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
	}
	return cfg, nil
}

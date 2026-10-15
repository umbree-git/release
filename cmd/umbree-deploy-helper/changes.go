//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
)

const changesName = "render.changed"

func (h *host) recordChanges(replaced []artefact) error {
	var lines []string
	for _, a := range replaced {
		if a.name == vhostName {
			lines = append(lines, "vhost")
		} else if !contains(lines, "units") {
			lines = append(lines, "units")
		}
	}
	return writeAtomic(h.varDir(), changesName, []byte(strings.Join(lines, "\n")), 0o600)
}

func (h *host) renderChanges() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(filepath.Join(h.varDir(), changesName))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(data), "\n") {
		if l != "" {
			out[l] = true
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

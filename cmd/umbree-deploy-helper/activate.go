//go:build linux

package main

import "fmt"

func activate(h *host, _ *config) error {
	steps := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", serviceUnit},
		{"systemctl", "restart", serviceUnit},
		{"systemctl", "reload", "nginx"},
	}
	for _, s := range steps {
		if out, err := h.run(s[0], s[1:]...); err != nil {
			return fmt.Errorf("%v: %v: %s", s, err, tail(out))
		}
	}
	h.ok("%s enabled and restarted, nginx reloaded", serviceUnit)
	return nil
}

func restore(h *host, _ *config) error {
	service := artefact{dir: h.unitDir, name: serviceUnit}
	if exists(h.marker(service, "absent")) {
		_, _ = h.run("systemctl", "stop", serviceUnit)
		_, _ = h.run("systemctl", "disable", serviceUnit)
	}
	err := h.restoreFiles(h.allArtefacts())
	_, _ = h.run("systemctl", "daemon-reload")
	if exists(h.binaryArtefact().dest()) {
		_, _ = h.run("systemctl", "restart", serviceUnit)
	}
	if _, nerr := h.run("nginx", "-t"); nerr == nil {
		_, _ = h.run("systemctl", "reload", "nginx")
	}
	if err != nil {
		return err
	}
	h.ok("previous binary, units and vhost restored; files that had none removed")
	return nil
}

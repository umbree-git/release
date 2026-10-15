//go:build linux

package main

import "fmt"

func activate(h *host, _ *config) error {
	changed := h.renderChanges()
	var steps [][]string
	if changed["units"] {
		steps = append(steps, []string{"SYSTEMCTL", "daemon-reload"})
	}
	steps = append(steps, []string{"SYSTEMCTL", "enable", serviceUnit}, []string{"SYSTEMCTL", "restart", serviceUnit})
	if changed["vhost"] {
		steps = append(steps, []string{"SYSTEMCTL", "reload", "nginx"})
	}
	for _, s := range steps {
		if out, err := h.exec(s[0], s[1:]...); err != nil {
			return fmt.Errorf("%v: %v: %s", s, err, tail(out))
		}
	}
	h.ok("%s enabled and restarted; units changed: %t, nginx reloaded: %t", serviceUnit, changed["units"], changed["vhost"])
	return nil
}

func restore(h *host, _ *config) error {
	service := artefact{dir: h.unitDir, name: serviceUnit}
	if exists(h.marker(service, "absent")) {
		_, _ = h.exec("SYSTEMCTL", "stop", serviceUnit)
		_, _ = h.exec("SYSTEMCTL", "disable", serviceUnit)
	}
	err := h.restoreFiles(h.allArtefacts())
	_, _ = h.exec("SYSTEMCTL", "daemon-reload")
	if exists(h.binaryArtefact().dest()) {
		_, _ = h.exec("SYSTEMCTL", "restart", serviceUnit)
	}
	if _, nerr := h.exec("NGINX", "-t"); nerr == nil {
		_, _ = h.exec("SYSTEMCTL", "reload", "nginx")
	}
	if err != nil {
		return err
	}
	h.ok("previous binary, units and vhost restored; files that had none removed")
	return nil
}

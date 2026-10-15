//go:build linux

package main

import "strings"

func status(h *host, _ *config) error {
	bin := h.binaryArtefact().dest()
	if sum, _, err := sha256File(bin); err == nil {
		h.say("%s sha256 %s", bin, sum)
	} else {
		h.say("%s absent", bin)
	}
	out, _ := h.exec("SYSTEMCTL", "is-active", serviceUnit)
	h.say("%s is %s", serviceUnit, strings.TrimSpace(string(out)))
	for _, a := range h.allArtefacts() {
		state := "no snapshot"
		if exists(h.marker(a, "prev")) {
			state = "previous copy held"
		} else if exists(h.marker(a, "absent")) {
			state = "absent before the last deploy"
		}
		h.say("%s: %s", a.dest(), state)
	}
	return nil
}

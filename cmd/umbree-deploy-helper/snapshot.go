//go:build linux

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type artefact struct {
	dir  string
	name string
	mode os.FileMode
}

func (a artefact) dest() string { return filepath.Join(a.dir, a.name) }

func (h *host) binaryArtefact() artefact {
	return artefact{dir: h.binDir(), name: binaryName, mode: 0o755}
}

func (h *host) renderedArtefacts() []artefact {
	var out []artefact
	for _, u := range unitNames {
		out = append(out, artefact{dir: h.unitDir, name: u, mode: 0o644})
	}
	return append(out, artefact{dir: h.vhostDir, name: vhostName, mode: 0o644})
}

func (h *host) allArtefacts() []artefact {
	return append([]artefact{h.binaryArtefact()}, h.renderedArtefacts()...)
}

func (h *host) marker(a artefact, kind string) string {
	return filepath.Join(h.varDir(), a.name+"."+kind)
}

func (h *host) snapshot() error {
	for _, a := range h.allArtefacts() {
		for _, kind := range []string{"prev", "absent"} {
			if err := os.Remove(h.marker(a, kind)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		fi, err := os.Lstat(a.dest())
		switch {
		case errors.Is(err, fs.ErrNotExist):
			err = writeAtomic(h.varDir(), a.name+".absent", nil, 0o600)
		case err != nil:
		case !fi.Mode().IsRegular():
			err = fmt.Errorf("%s is not a regular file", a.dest())
		default:
			err = copyAtomic(a.dest(), h.varDir(), a.name+".prev", 0o600)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func (h *host) restoreFiles(arts []artefact) error {
	var first error
	for _, a := range arts {
		var err error
		switch {
		case exists(h.marker(a, "prev")):
			err = copyAtomic(h.marker(a, "prev"), a.dir, a.name, a.mode)
		case exists(h.marker(a, "absent")):
			if err = os.Remove(a.dest()); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
		}
		if err != nil && first == nil {
			first = fmt.Errorf("restore %s: %w", a.dest(), err)
		}
	}
	return first
}

//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func installBinary(h *host, cfg *config) error {
	deploy, err := h.lookup(cfg.deployUser)
	if err != nil {
		return fmt.Errorf("DEPLOY_USER %s: %w", cfg.deployUser, err)
	}
	src, err := openStagedBinary(cfg.stageDir, deploy.uid)
	if err != nil {
		return err
	}
	defer src.Close()
	if h.afterSourceCheck != nil {
		h.afterSourceCheck()
	}
	if err := h.snapshot(); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	sum, err := copyHeld(src, h.binaryArtefact())
	if err != nil {
		return err
	}
	h.ok("installed %s, sha256 %s", h.binaryArtefact().dest(), sum)
	return nil
}

func copyHeld(src *os.File, a artefact) (string, error) {
	f, tmp, err := createTemp(a.dir, a.name, a.mode)
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), src)
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	want := hex.EncodeToString(sum.Sum(nil))
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	got, size, err := sha256File(tmp)
	if err == nil && (got != want || size != n) {
		err = fmt.Errorf("temp %s re-read as %s (%d bytes), copied %s (%d bytes)", tmp, got, size, want, n)
	}
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	return want, commitTemp(f, tmp, filepath.Join(a.dir, a.name), a.mode)
}

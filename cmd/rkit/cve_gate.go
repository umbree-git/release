package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/umbree-git/release/internal/relconfig"
)

const (
	gateMissingExit       = 127
	gateNotExecutableExit = 126
)

func cveGatePath() string {
	if p := os.Getenv("CVE_GATE"); p != "" {
		return p
	}
	return filepath.Join(os.Getenv("HOME"), ".agents", "scripts", "cve-gate.sh")
}

func cveGateArgs(o buildOpts, distDir string) []string {
	return []string{
		"--report", filepath.Join(distDir, "cve"),
		"--gowork", "off",
		"--go", o.SrcDir,
		"--waivers", filepath.Join(o.RepoDir, "cve-waivers.tsv"),
	}
}

func checkGateRunnable(gate string) error {
	info, err := os.Stat(gate)
	if err != nil {
		return fmt.Errorf("cve gate: exit %d: %s: %w", gateMissingExit, gate, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("cve gate: exit %d: %s is not executable", gateNotExecutableExit, gate)
	}
	return nil
}

func gateCVE(ctx context.Context, o buildOpts, distDir string) error {
	if o.Channel == relconfig.ChannelBeta {
		fmt.Fprintln(os.Stderr, "cve gate: not run on the beta channel (operator ruling: stable cuts only)")
		return nil
	}
	gate := cveGatePath()
	if err := checkGateRunnable(gate); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, gate, cveGateArgs(o, distDir)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exitErr):
		return fmt.Errorf("cve gate: exit %d", exitErr.ExitCode())
	}
	return fmt.Errorf("cve gate: exit %d: %s: %w", gateNotExecutableExit, gate, err)
}

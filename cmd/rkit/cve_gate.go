package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func cveGateArgs(o buildOpts, distDir string) ([]string, error) {
	d, err := loadCVEGateData(o.RepoDir)
	if err != nil {
		return nil, err
	}
	if err := d.checkComponents(relconfig.Components); err != nil {
		return nil, err
	}
	e := d.entries[o.Component]
	if err := e.checkGoModules(o.SrcDir); err != nil {
		return nil, err
	}
	return append([]string{"--report", filepath.Join(distDir, "cve")}, e.args(o.RepoDir, o.SrcDir)...), nil
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
	return runCVEGate(ctx, o, distDir, os.Stdout)
}

func runCVEGate(ctx context.Context, o buildOpts, distDir string, stdout io.Writer) error {
	if o.Channel == relconfig.ChannelBeta {
		fmt.Fprintln(os.Stderr, "cve gate: not run on the beta channel (operator ruling: stable cuts only)")
		return nil
	}
	gate := cveGatePath()
	if err := checkGateRunnable(gate); err != nil {
		return err
	}
	args, err := cveGateArgs(o, distDir)
	if err != nil {
		return fmt.Errorf("cve gate: %w", err)
	}
	cmd := exec.CommandContext(ctx, gate, args...)
	cmd.Stdout, cmd.Stderr = stdout, os.Stderr
	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &exitErr):
		return &gateExitError{code: exitErr.ExitCode()}
	}
	return fmt.Errorf("cve gate: exit %d: %s: %w", gateNotExecutableExit, gate, err)
}

type gateExitError struct{ code int }

func (e *gateExitError) Error() string { return fmt.Sprintf("cve gate: exit %d", e.code) }

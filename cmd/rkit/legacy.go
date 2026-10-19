package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/burrowee-git/release-kit/build"
	"github.com/burrowee-git/release-kit/sign"

	"github.com/umbree-git/release/internal/relconfig"
)

type legacyBuild struct {
	RepoDir, SrcDir, OutDir string
	Target                  build.Target
	Bins                    []build.BinSpec
	Signer                  sign.Signer
}

func legacyOverlayDir(repoDir string) string {
	return filepath.Join(repoDir, "tools", "legacy", "darwin")
}

func checkLegacyToolchain(ctx context.Context, repoDir string) error {
	pin, err := os.ReadFile(filepath.Join(legacyOverlayDir(repoDir), "GO_VERSION"))
	if err != nil {
		return fmt.Errorf("darwin-amd64-legacy toolchain pin: %w", err)
	}
	want := strings.TrimSpace(string(pin))
	have := "unreadable"
	out, err := exec.CommandContext(ctx, "go", "version").Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) >= 3 {
		have = fields[2]
	}
	if have != want {
		return fmt.Errorf("the darwin-amd64-legacy overlay is pinned to %s; the go on PATH is %s — a stdlib patch is valid for exactly one stdlib (tools/legacy/darwin/README.md)", want, have)
	}
	return nil
}

func compileLegacy(ctx context.Context, lb legacyBuild) ([]build.Artifact, error) {
	if lb.Target != relconfig.LegacyTarget() {
		return nil, fmt.Errorf("the legacy variant is darwin/amd64 only (got %s/%s)", lb.Target.OS, lb.Target.Arch)
	}
	work, err := os.MkdirTemp("", "umbree-legacy-")
	if err != nil {
		return nil, fmt.Errorf("legacy overlay workspace: %w", err)
	}
	defer os.RemoveAll(work)
	overlay, err := prepareLegacyOverlay(ctx, legacyOverlayDir(lb.RepoDir), work)
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(lb.OutDir, lb.Target.OS+"-"+relconfig.LegacyArch)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	var arts []build.Artifact
	for _, b := range lb.Bins {
		a, err := compileLegacyBin(ctx, lb, b, overlay, filepath.Join(outDir, b.Name))
		if err != nil {
			return nil, err
		}
		arts = append(arts, a)
	}
	return arts, nil
}

func prepareLegacyOverlay(ctx context.Context, dir, work string) (string, error) {
	guard := exec.CommandContext(ctx, "bash", filepath.Join(dir, "overlay.test.sh"))
	if out, err := guard.CombinedOutput(); err != nil {
		return "", fmt.Errorf("the legacy overlay drift guard failed — refusing to build darwin-amd64-legacy:\n%s", out)
	}
	overlay := filepath.Join(work, "overlay.json")
	writer := exec.CommandContext(ctx, "bash", filepath.Join(dir, "overlay-json.sh"), overlay)
	if out, err := writer.CombinedOutput(); err != nil {
		return "", fmt.Errorf("overlay-json.sh: %w\n%s", err, out)
	}
	return overlay, nil
}

func compileLegacyBin(ctx context.Context, lb legacyBuild, b build.BinSpec, overlay, outPath string) (build.Artifact, error) {
	buildDir := lb.SrcDir
	if b.SubDir != "" {
		buildDir = filepath.Join(lb.SrcDir, b.SubDir)
	}
	goWork := b.GoWork
	if goWork == "" {
		goWork = "off"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-overlay", overlay,
		"-ldflags", b.Ldflags, "-o", outPath, b.Package)
	cmd.Dir = buildDir
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0", "GOOS="+lb.Target.OS, "GOARCH="+lb.Target.Arch, "GOWORK="+goWork)
	if out, err := cmd.CombinedOutput(); err != nil {
		return build.Artifact{}, fmt.Errorf("build %s (darwin-amd64-legacy): %w\n%s", b.Name, err, out)
	}
	if err := assertLegacySymbols(ctx, lb.RepoDir, outPath); err != nil {
		os.Remove(outPath)
		return build.Artifact{}, fmt.Errorf("%s: refusing a darwin-amd64-legacy binary that would still abort in dyld below macOS 12: %w", b.Name, err)
	}
	a := build.Artifact{Bin: b.Name, OS: lb.Target.OS, Arch: relconfig.LegacyArch, Path: outPath}
	if lb.Signer != nil {
		if err := lb.Signer.Sign(ctx, outPath); err != nil {
			return build.Artifact{}, fmt.Errorf("sign %s: %w", outPath, err)
		}
		a.Signed = true
	}
	return a, nil
}

func assertLegacySymbols(ctx context.Context, repoDir, bin string) error {
	cmd := exec.CommandContext(ctx, "bash", "-c", `source "$1" && assert_legacy_symbols "$2"`, "_",
		filepath.Join(legacyOverlayDir(repoDir), "symbols.sh"), bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}

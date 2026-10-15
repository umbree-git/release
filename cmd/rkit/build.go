package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/burrowee-git/release-kit/build"
	"github.com/burrowee-git/release-kit/checksum"
	"github.com/burrowee-git/release-kit/minisign"
	"github.com/burrowee-git/release-kit/sign"
	"github.com/burrowee-git/release-kit/vulncheck"

	"github.com/umbree-git/release/internal/relconfig"
)

type Options struct {
	Component, OutDir, RepoDir string
	SrcDir                     string
	MinisignKey                string
	SkipGate                   bool
	Apple                      bool
	DryRun                     bool
	Channel                    string
}

type Result struct {
	Stamp         string
	Zips          []string
	Sums, Minisig string
}

type buildOpts struct {
	Component, RepoDir, SrcDir, SignKey string
	Apple, DryRun, NoVulncheck          bool
	Bump                                string
	Channel                             string
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	var o buildOpts
	fs.StringVar(&o.Component, "component", "", "umbree")
	fs.StringVar(&o.RepoDir, "repo", ".", "release repo worktree")
	fs.StringVar(&o.SrcDir, "src", "", "component source worktree (default: resolved from UMBREE_SRC_UMBREE)")
	fs.StringVar(&o.SignKey, "sign-key", "", "minisign secret key (required for a real cut; --dry-run defaults to the TEST key)")
	appleFlag := fs.Bool("apple", false, "Developer-ID sign + notarize macOS binaries")
	publicFlag := fs.Bool("public", false, "public release: apple sign+notarize + CVE gate (standard ship path)")
	publicReleaseFlag := fs.Bool("public-release", false, "alias for --public")
	fs.BoolVar(&o.DryRun, "dry-run", false, "build without bumping the version or requiring a real sign key")
	fs.BoolVar(&o.NoVulncheck, "no-vulncheck", false, "skip the CVE gate (default: the gate runs)")
	bumpPatch := fs.Bool("bump-patch", false, "bump the component's patch version before building")
	bumpMinor := fs.Bool("bump-minor", false, "bump the component's minor version before building (prompts unless UMBREE_RELEASE_YES=1)")
	bumpMajor := fs.Bool("bump-major", false, "bump the component's major version before building (prompts unless UMBREE_RELEASE_YES=1)")
	fs.StringVar(&o.Channel, "channel", relconfig.ChannelStable, "release channel: stable | beta (beta reads versions/<comp>.beta and stamps v<X.Y.Z>.beta.<date>.<sha>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := relconfig.VersionFile(o.Channel, "", ""); err != nil {
		return err
	}
	if *publicFlag || *publicReleaseFlag {
		o.Apple = true
		o.NoVulncheck = false
	}
	if *appleFlag {
		o.Apple = true
	}
	if (*bumpPatch && *bumpMinor) || (*bumpPatch && *bumpMajor) || (*bumpMinor && *bumpMajor) {
		return fmt.Errorf("only one of --bump-patch|--bump-minor|--bump-major may be set")
	}
	switch {
	case *bumpPatch:
		o.Bump = "patch"
	case *bumpMinor:
		o.Bump = "minor"
	case *bumpMajor:
		o.Bump = "major"
	}
	if o.SrcDir == "" {
		s, err := srcDirFor(o.Component)
		if err != nil {
			return err
		}
		o.SrcDir = s
	}
	if o.Apple {
		if err := loadAppleAccount(o.RepoDir); err != nil {
			return err
		}
	}
	return buildRun(o)
}

func srcDirFor(comp string) (string, error) {
	key := "UMBREE_SRC_" + strings.ToUpper(comp)
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no source worktree for %q: set %s or pass --src", comp, key)
}

func buildRun(o buildOpts) (err error) {
	if abs, aerr := filepath.Abs(o.RepoDir); aerr == nil {
		o.RepoDir = abs
	} else {
		return fmt.Errorf("resolve --repo %q: %w", o.RepoDir, aerr)
	}
	if o.SrcDir == "" {
		o.SrcDir = o.RepoDir
	} else if abs, aerr := filepath.Abs(o.SrcDir); aerr == nil {
		o.SrcDir = abs
	} else {
		return fmt.Errorf("resolve --src %q: %w", o.SrcDir, aerr)
	}

	if !o.DryRun && o.SignKey == "" {
		return fmt.Errorf("--sign-key is required for a real build (only --dry-run defaults to the test key)")
	}
	if !o.DryRun {
		if _, err := os.Stat(o.SignKey); err != nil {
			return fmt.Errorf("--sign-key %s: %w", o.SignKey, err)
		}
	}

	if o.Channel == "" {
		o.Channel = relconfig.ChannelStable
	}
	versionsDir := filepath.Join(o.RepoDir, "versions")
	versionFile, err := relconfig.VersionFile(o.Channel, versionsDir, o.Component)
	if err != nil {
		return err
	}
	versionSh := filepath.Join(o.RepoDir, "tools", "version.sh")

	if o.Channel == relconfig.ChannelBeta {
		cmd := exec.Command("bash", versionSh, o.Component, "--channel", "beta", "--assert-beta-above-stable")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("beta version assert: %w", err)
		}
	}

	defer func() {
		if err != nil || o.DryRun {
			for _, f := range []string{"versions/" + o.Component, "versions/" + o.Component + ".beta"} {
				exec.Command("git", "-C", o.RepoDir, "restore", "--staged", "--worktree", "--", f).Run()
			}
		}
	}()

	if !o.DryRun && o.Bump != "" {
		cmd := exec.Command("bash", versionSh, o.Component, "--channel", o.Channel, "--bump-"+o.Bump)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("version bump: %w", err)
		}
	}

	ctx := context.Background()
	stamp, err := relconfig.StampFor(ctx, o.Channel, versionsDir, o.Component, o.SrcDir)
	if err != nil {
		return fmt.Errorf("stamp from %s: %w", versionFile, err)
	}
	distDir := filepath.Join(o.RepoDir, "dist", stamp)

	if !o.NoVulncheck {
		if err = vulncheck.Gate(ctx, []vulncheck.Module{{Name: o.Component, Dir: o.SrcDir}},
			vulncheck.GateOpts{ReportDir: filepath.Join(distDir, "vulncheck")}); err != nil {
			return fmt.Errorf("cve gate: %w", err)
		}
	}

	key := o.SignKey
	if key == "" {
		key = filepath.Join(o.RepoDir, "tools", "testkeys", "test.key")
	}

	_, err = orchestrate(ctx, Options{
		Component: o.Component, OutDir: filepath.Join(o.RepoDir, "dist"),
		RepoDir: o.RepoDir, SrcDir: o.SrcDir,
		MinisignKey: key,
		SkipGate:    true,
		Apple:       o.Apple, DryRun: o.DryRun,
		Channel: o.Channel,
	})
	return err
}

func selectSigner(apple bool) sign.Signer {
	if apple {
		return sign.AppleSigner{ToolPath: "modernech-sign"}
	}
	return sign.AdHocSigner{}
}

func notarizerFor(apple bool) (sign.Notarizer, bool) {
	if apple {
		return sign.Notarizer{ToolPath: "modernech-sign"}, true
	}
	return sign.Notarizer{}, false
}

func renderInstall(comp, stamp, srcDir, repoDir, dst, channel string) (fromTree bool, err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	var data []byte
	switch comp {
	case "umbree":
		tree := filepath.Join(srcDir, "install", "install.sh")
		if raw, terr := os.ReadFile(tree); terr == nil {
			data = raw
			fromTree = true
			break
		}
		if channel == relconfig.ChannelBeta {
			return false, fmt.Errorf("renderInstall umbree: beta build has no install/install.sh in %s — feature 02 (cli/install/) has not reached beta; a beta must ship the installer from the tree it was built from", srcDir)
		}
		fallback := filepath.Join(repoDir, "inner", comp, "install.sh")
		raw, ferr := os.ReadFile(fallback)
		if ferr != nil {
			return false, fmt.Errorf("renderInstall %s: neither %s nor %s: %w", comp, tree, fallback, ferr)
		}
		fmt.Fprintf(os.Stderr, "⚠ renderInstall umbree: %s is a fallback — the cli tree carries no install/install.sh; this copy already assumes a `service` verb the stable cli may not have (release README, feature 02)\n", fallback)
		data = raw
	case "umbreed":
		src := filepath.Join(srcDir, "install", "install.sh.in")
		raw, rerr := os.ReadFile(src)
		if rerr != nil {
			return false, fmt.Errorf("renderInstall %s: canonical installer template missing: %s (set UMBREE_SRC_UMBREED to the daemon source worktree): %w", comp, src, rerr)
		}
		data = []byte(strings.ReplaceAll(string(raw), "__UMBREED_VERSION__", stamp))
		fromTree = true
	default:
		return false, fmt.Errorf("renderInstall: unknown component %q", comp)
	}
	return fromTree, os.WriteFile(dst, data, 0o755)
}

func orchestrate(ctx context.Context, o Options) (*Result, error) {
	if o.SrcDir == "" {
		o.SrcDir = o.RepoDir
	}
	if o.Channel == "" {
		o.Channel = relconfig.ChannelStable
	}
	if !o.SkipGate {
		if err := vulncheck.Gate(ctx, []vulncheck.Module{{Name: o.Component, Dir: o.SrcDir}},
			vulncheck.GateOpts{ReportDir: filepath.Join(o.OutDir, "vulncheck")}); err != nil {
			return nil, fmt.Errorf("cve gate: %w", err)
		}
	}
	stamp, err := relconfig.StampFor(ctx, o.Channel, filepath.Join(o.RepoDir, "versions"), o.Component, o.SrcDir)
	if err != nil {
		return nil, err
	}
	bins, err := relconfig.Bins(o.Component, stamp)
	if err != nil {
		return nil, err
	}
	arts, err := build.Compile(ctx, build.Spec{
		SrcDir: o.SrcDir, OutDir: filepath.Join(o.OutDir, stamp),
		Targets: relconfig.Targets(), Bins: bins, Signer: selectSigner(o.Apple),
	})
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", o.Component, err)
	}
	res := &Result{Stamp: stamp}

	guardArgs := make([]string, 0, len(arts))
	for _, a := range arts {
		guardArgs = append(guardArgs, a.Path)
	}
	guard := exec.CommandContext(ctx, "bash", filepath.Join(o.RepoDir, "tools", "verify-no-env.sh"))
	guard.Args = append(guard.Args, guardArgs...)
	guard.Stdout, guard.Stderr = os.Stderr, os.Stderr
	if err := guard.Run(); err != nil {
		return nil, fmt.Errorf("verify-no-env: %w", err)
	}

	installSh := filepath.Join(o.OutDir, stamp, "install.sh")
	installFromTree, err := renderInstall(o.Component, stamp, o.SrcDir, o.RepoDir, installSh, o.Channel)
	if err != nil {
		return nil, fmt.Errorf("install.sh: %w", err)
	}

	migs, err := stageMigrations(o.Component, o.SrcDir, o.RepoDir, installFromTree)
	if err != nil {
		return nil, err
	}
	if o.Channel == relconfig.ChannelBeta && len(migs) == 0 && installFromTree {
		feature := map[string]string{"umbree": "02", "umbreed": "03"}[o.Component]
		fmt.Fprintf(os.Stderr, "⚠ %s: beta build ships an installer from %s but no install/migrations/ — feature %s adds the ladder; this beta carries none\n", o.Component, o.SrcDir, feature)
	}

	zips, err := assemble(o.Component, stamp, o.OutDir, installSh, migs, arts)
	if err != nil {
		return nil, fmt.Errorf("assemble: %w", err)
	}
	res.Zips = zips

	if n, do := notarizerFor(o.Apple); do {
		for _, zp := range res.Zips {
			if !strings.Contains(filepath.Base(zp), "-darwin-") {
				continue
			}
			if o.DryRun {
				fmt.Fprintf(os.Stderr, "dry-run: skipping notarize of %s\n", zp)
				continue
			}
			if err := n.Notarize(ctx, zp); err != nil {
				return nil, fmt.Errorf("notarize %s: %w", zp, err)
			}
		}
	}

	sums := filepath.Join(o.OutDir, stamp, "SHA256SUMS.txt")
	if err := checksum.WriteSums(res.Zips, sums); err != nil {
		return nil, fmt.Errorf("checksum: %w", err)
	}
	key := o.MinisignKey
	if key == "" {
		key = filepath.Join(o.RepoDir, "tools", "testkeys", "test.key")
	}
	if _, statErr := os.Stat(key); statErr != nil {
		if o.MinisignKey != "" {
			return nil, fmt.Errorf("minisign key %s: %w", key, statErr)
		}
	} else {
		if err := minisign.Sign(ctx, sums, key); err != nil {
			return nil, fmt.Errorf("minisign: %w", err)
		}
		res.Minisig = sums + ".minisig"
	}
	res.Sums = sums
	return res, nil
}

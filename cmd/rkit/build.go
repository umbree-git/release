package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	HostOS                     string
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
	bump, err := bumpOf(*bumpPatch, *bumpMinor, *bumpMajor)
	if err != nil {
		return err
	}
	o.Bump = bump
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

func bumpOf(patch, minor, major bool) (string, error) {
	if (patch && minor) || (patch && major) || (minor && major) {
		return "", fmt.Errorf("only one of --bump-patch|--bump-minor|--bump-major may be set")
	}
	switch {
	case patch:
		return "patch", nil
	case minor:
		return "minor", nil
	case major:
		return "major", nil
	}
	return "", nil
}

func srcDirFor(comp string) (string, error) {
	key := "UMBREE_SRC_" + strings.ToUpper(comp)
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no source worktree for %q: set %s or pass --src", comp, key)
}

func buildRun(o buildOpts) (err error) {
	if err := resolveBuildDirs(&o); err != nil {
		return err
	}
	if err := checkSignKeyFile(o); err != nil {
		return err
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
	if err := assertBetaAboveStable(o, versionSh); err != nil {
		return err
	}
	ctx := context.Background()
	if err := checkLegacyToolchain(ctx, o.RepoDir); err != nil {
		return err
	}
	defer func() { restoreVersionFiles(o, err) }()
	if err := bumpVersion(o, versionSh); err != nil {
		return err
	}
	stamp, err := relconfig.StampFor(ctx, o.Channel, versionsDir, o.Component, o.SrcDir)
	if err != nil {
		return fmt.Errorf("stamp from %s: %w", versionFile, err)
	}
	if err := gateCVE(ctx, o, filepath.Join(o.RepoDir, "dist", stamp)); err != nil {
		return err
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

func restoreVersionFiles(o buildOpts, err error) {
	if err == nil && !o.DryRun {
		return
	}
	for _, f := range []string{"versions/" + o.Component, "versions/" + o.Component + ".beta"} {
		exec.Command("git", "-C", o.RepoDir, "restore", "--staged", "--worktree", "--", f).Run()
	}
}

func resolveBuildDirs(o *buildOpts) error {
	abs, err := filepath.Abs(o.RepoDir)
	if err != nil {
		return fmt.Errorf("resolve --repo %q: %w", o.RepoDir, err)
	}
	o.RepoDir = abs
	if o.SrcDir == "" {
		o.SrcDir = o.RepoDir
		return nil
	}
	abs, err = filepath.Abs(o.SrcDir)
	if err != nil {
		return fmt.Errorf("resolve --src %q: %w", o.SrcDir, err)
	}
	o.SrcDir = abs
	return nil
}

func checkSignKeyFile(o buildOpts) error {
	if o.DryRun {
		return nil
	}
	if o.SignKey == "" {
		return fmt.Errorf("--sign-key is required for a real build (only --dry-run defaults to the test key)")
	}
	if _, err := os.Stat(o.SignKey); err != nil {
		return fmt.Errorf("--sign-key %s: %w", o.SignKey, err)
	}
	return nil
}

func assertBetaAboveStable(o buildOpts, versionSh string) error {
	if o.Channel != relconfig.ChannelBeta {
		return nil
	}
	cmd := exec.Command("bash", versionSh, o.Component, "--channel", "beta", "--assert-beta-above-stable")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("beta version assert: %w", err)
	}
	return nil
}

func bumpVersion(o buildOpts, versionSh string) error {
	if o.DryRun || o.Bump == "" {
		return nil
	}
	cmd := exec.Command("bash", versionSh, o.Component, "--channel", o.Channel, "--bump-"+o.Bump)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("version bump: %w", err)
	}
	return nil
}

func gateCVE(ctx context.Context, o buildOpts, distDir string) error {
	if o.NoVulncheck {
		return nil
	}
	if err := vulncheck.Gate(ctx, []vulncheck.Module{{Name: o.Component, Dir: o.SrcDir}},
		vulncheck.GateOpts{ReportDir: filepath.Join(distDir, "vulncheck")}); err != nil {
		return fmt.Errorf("cve gate: %w", err)
	}
	return nil
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
	if err := checkLegacyToolchain(ctx, o.RepoDir); err != nil {
		return nil, err
	}
	stamp, err := relconfig.StampFor(ctx, o.Channel, filepath.Join(o.RepoDir, "versions"), o.Component, o.SrcDir)
	if err != nil {
		return nil, err
	}
	arts, err := compileAll(ctx, o, stamp)
	if err != nil {
		return nil, err
	}
	if err := verifyNoEnv(ctx, o.RepoDir, arts); err != nil {
		return nil, err
	}
	zips, err := assembleZips(o, stamp, arts)
	if err != nil {
		return nil, err
	}
	if err := notarizeZips(ctx, o, zips); err != nil {
		return nil, err
	}
	res := &Result{Stamp: stamp, Zips: zips}
	if err := writeSignedSums(ctx, o, stamp, res); err != nil {
		return nil, err
	}
	return res, nil
}

func compileAll(ctx context.Context, o Options, stamp string) ([]build.Artifact, error) {
	bins, err := relconfig.Bins(o.Component, stamp)
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(o.OutDir, stamp)
	signer := selectSigner(o.Apple)
	arts, err := build.Compile(ctx, build.Spec{
		SrcDir: o.SrcDir, OutDir: outDir,
		Targets: relconfig.Targets(), Bins: bins, Signer: signer,
	})
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", o.Component, err)
	}
	lb := legacyBuild{RepoDir: o.RepoDir, SrcDir: o.SrcDir, OutDir: outDir, Target: relconfig.LegacyTarget(), Bins: bins}
	if hostOS(o) == "darwin" {
		lb.Signer = signer
	}
	legacy, err := compileLegacy(ctx, lb)
	if err != nil {
		return nil, fmt.Errorf("compile %s: %w", o.Component, err)
	}
	return append(arts, legacy...), nil
}

func hostOS(o Options) string {
	if o.HostOS != "" {
		return o.HostOS
	}
	return runtime.GOOS
}

func verifyNoEnv(ctx context.Context, repoDir string, arts []build.Artifact) error {
	guard := exec.CommandContext(ctx, "bash", filepath.Join(repoDir, "tools", "verify-no-env.sh"))
	guard.Args = append(guard.Args, build.Paths(arts)...)
	guard.Stdout, guard.Stderr = os.Stderr, os.Stderr
	if err := guard.Run(); err != nil {
		return fmt.Errorf("verify-no-env: %w", err)
	}
	return nil
}

func assembleZips(o Options, stamp string, arts []build.Artifact) ([]string, error) {
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
	return zips, nil
}

func notarizeZips(ctx context.Context, o Options, zips []string) error {
	n, do := notarizerFor(o.Apple)
	if !do {
		return nil
	}
	for _, zp := range zips {
		if !strings.Contains(filepath.Base(zp), "-darwin-") {
			continue
		}
		if o.DryRun {
			fmt.Fprintf(os.Stderr, "dry-run: skipping notarize of %s\n", zp)
			continue
		}
		if err := n.Notarize(ctx, zp); err != nil {
			return fmt.Errorf("notarize %s: %w", zp, err)
		}
	}
	return nil
}

func writeSignedSums(ctx context.Context, o Options, stamp string, res *Result) error {
	sums := filepath.Join(o.OutDir, stamp, "SHA256SUMS.txt")
	if err := checksum.WriteSums(res.Zips, sums); err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	key := o.MinisignKey
	if key == "" {
		key = filepath.Join(o.RepoDir, "tools", "testkeys", "test.key")
	}
	if _, statErr := os.Stat(key); statErr != nil {
		if o.MinisignKey != "" {
			return fmt.Errorf("minisign key %s: %w", key, statErr)
		}
	} else {
		if err := minisign.Sign(ctx, sums, key); err != nil {
			return fmt.Errorf("minisign: %w", err)
		}
		res.Minisig = sums + ".minisig"
	}
	res.Sums = sums
	return nil
}

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
	// SrcDir is the COMPONENT source worktree (e.g. cli/code/main) — distinct
	// from RepoDir, which is the release repo holding versions/, inner/, and
	// tools/. Defaults to RepoDir when empty, so the fixture-based
	// orchestrate tests (which double one dir as both) keep working
	// unchanged.
	SrcDir      string
	MinisignKey string
	// SkipGate bypasses the mandatory vulncheck.Gate. buildRun always passes
	// true here because it has already run (or explicitly bypassed via
	// --no-vulncheck) the gate itself before calling orchestrate — gating
	// twice would rerun govulncheck for no reason. The zero value (false)
	// keeps orchestrate fail-closed for any other caller, which is why
	// TestOrchestrateSkipGateBypassesCVEGate exercises it directly.
	SkipGate bool
	// Apple selects the Developer-ID signer (selectSigner) for build.Compile and
	// gates darwin zips through Notarizer.Notarize after assembly. Zero value
	// (false) keeps the existing ad-hoc, non-notarized behavior.
	Apple bool
	// DryRun, when Apple is set, skips the real notarize submission (logs intent
	// instead) — a dry run's artifacts are throwaway and notarization is a real
	// Apple API call.
	DryRun bool
	// Channel is "stable" (the zero value resolves to it) or "beta": it picks
	// the version file (versions/<comp> vs versions/<comp>.beta), the stamp
	// scheme (relconfig.StampFor), and whether renderInstall may fall back to
	// the release repo's inner installer (never on beta).
	Channel string
}

type Result struct {
	Stamp         string
	Zips          []string
	Sums, Minisig string
}

// buildOpts configures `rkit build` — the real release-cut entry point:
// output lands at <RepoDir>/dist/<stamp>/ (never an arbitrary --out), an
// optional version bump shells the proven tools/version.sh with a
// revert-on-failure/dry-run trap, and the CVE gate runs by DEFAULT (unlike
// orchestrate's SkipGate, which buildRun always sets once the gate above has
// already run).
type buildOpts struct {
	Component, RepoDir, SrcDir, SignKey string
	Apple, DryRun, NoVulncheck          bool
	// Bump is "", "patch", "minor", or "major" — the tools/version.sh
	// --bump-<kind> action to run before stamping. Empty means no bump.
	Bump string
	// Channel is "stable" or "beta" (see Options.Channel). Beta asserts
	// versions/<comp>.beta sorts above versions/<comp> BEFORE any bump.
	Channel string
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
	// Refused before srcDirFor and before anything touches the tree: a
	// misspelled channel must not silently cut stable.
	if _, err := relconfig.VersionFile(o.Channel, "", ""); err != nil {
		return err
	}
	// --public / --public-release is the standard ship path: Apple sign+notarize + CVE gate.
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
		// Fatal, not advisory: an unresolved account means the Developer-ID path
		// runs with no account plugin, producing an ad-hoc signed build the
		// operator believes is Developer-ID signed and notarized.
		if err := loadAppleAccount(o.RepoDir); err != nil {
			return err
		}
	}
	return buildRun(o)
}

// srcDirFor resolves a component's source worktree from its UMBREE_SRC_<COMP>
// environment variable.
//
// There is deliberately NO default. This repository is public, so any default
// would be an absolute path on one operator's machine — which is what it used
// to ship. The build already fails fast without a source tree, so refusing
// here costs nothing and leaks nothing.
func srcDirFor(comp string) (string, error) {
	key := "UMBREE_SRC_" + strings.ToUpper(comp)
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no source worktree for %q: set %s or pass --src", comp, key)
}

// buildRun is the testable seam behind runBuild. It resolves dirs, optionally
// bumps the component's version (registering a revert that fires on error or
// --dry-run), runs the CVE gate unless NoVulncheck, then reuses orchestrate to
// build+assemble+checksum+sign into <RepoDir>/dist/<stamp>/.
func buildRun(o buildOpts) (err error) {
	// Absolutize --repo before it's used to derive OutDir, the version.sh
	// path, and the dist dir: build.Compile execs `go build -o <OutDir>/...`
	// with cmd.Dir set to the component SOURCE worktree, so a relative
	// RepoDir (e.g. the "." default) makes OutDir resolve inside the source
	// tree instead of the release repo — the binary lands in the wrong place
	// and the later codesign step (run from the orchestrator's own cwd) can't
	// find it.
	if abs, aerr := filepath.Abs(o.RepoDir); aerr == nil {
		o.RepoDir = abs
	} else {
		return fmt.Errorf("resolve --repo %q: %w", o.RepoDir, aerr)
	}
	if o.SrcDir == "" {
		o.SrcDir = o.RepoDir
	} else if abs, aerr := filepath.Abs(o.SrcDir); aerr == nil {
		// Defensive: --src (or the UMBREE_SRC_<COMP> value srcDirFor read it
		// from) isn't guaranteed to be absolute.
		o.SrcDir = abs
	} else {
		return fmt.Errorf("resolve --src %q: %w", o.SrcDir, aerr)
	}

	// Fail fast: a real cut requires a real sign key. Check this before the
	// bump + CVE gate so a doomed real cut doesn't waste either of them.
	// --dry-run defaults to the TEST key further down, where it's used.
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

	// Beta: refuse before touching anything. A beta that does not read newer
	// than its stable sibling would ship a `version` a beta host (and the
	// follow-on stable cut) cannot trust — and refusing here, before the
	// bump, leaves versions/<comp>.beta exactly as it was.
	if o.Channel == relconfig.ChannelBeta {
		cmd := exec.Command("bash", versionSh, o.Component, "--channel", "beta", "--assert-beta-above-stable")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("beta version assert: %w", err)
		}
	}

	// Revert the version bump if the build fails, or unconditionally on
	// --dry-run — a dry run must never leave a bumped versions/<comp> behind.
	// Registered BEFORE the bump step below so it also covers the bump
	// step's own failure (version.sh writes the file then `git add` fails).
	// Both channels' files are restored, EACH IN ITS OWN CALL: git aborts a
	// restore whose pathspec matches nothing tracked, and versions/<comp>.beta
	// is untracked whenever no cycle is open — one combined call would then
	// restore neither and leave a stable bump staged after a failed build.
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

	// CVE gate — ON BY DEFAULT for a real build (orchestrate's own SkipGate
	// exists only so buildRun can avoid gating twice below). --no-vulncheck
	// bypasses it.
	if !o.NoVulncheck {
		if err = vulncheck.Gate(ctx, []vulncheck.Module{{Name: o.Component, Dir: o.SrcDir}},
			vulncheck.GateOpts{ReportDir: filepath.Join(distDir, "vulncheck")}); err != nil {
			return fmt.Errorf("cve gate: %w", err)
		}
	}

	key := o.SignKey
	if key == "" {
		// Reached only when o.DryRun (the real-cut case already returned above).
		key = filepath.Join(o.RepoDir, "tools", "testkeys", "test.key")
	}

	_, err = orchestrate(ctx, Options{
		Component: o.Component, OutDir: filepath.Join(o.RepoDir, "dist"),
		RepoDir: o.RepoDir, SrcDir: o.SrcDir,
		MinisignKey: key,
		SkipGate:    true, // the gate above already ran (or was explicitly bypassed)
		Apple:       o.Apple, DryRun: o.DryRun,
		Channel: o.Channel,
	})
	return err
}

// selectSigner picks build.Compile's Signer: a real Developer-ID signature via
// the product's modernech-sign helper when --apple is set, otherwise the
// existing ad-hoc codesign (macOS needs any signature to run, unsigned or
// ad-hoc, on non-apple/non-darwin cuts).
func selectSigner(apple bool) sign.Signer {
	if apple {
		return sign.AppleSigner{ToolPath: "modernech-sign"}
	}
	return sign.AdHocSigner{}
}

// notarizerFor returns the Notarizer to submit darwin zips to Apple when
// --apple is set, and whether notarization should run at all. Non-apple cuts
// never notarize.
func notarizerFor(apple bool) (sign.Notarizer, bool) {
	if apple {
		return sign.Notarizer{ToolPath: "modernech-sign"}, true
	}
	return sign.Notarizer{}, false
}

// renderInstall writes comp's install.sh into the stamp dir and reports
// whether it came from the component tree. ONE rule for both components: the
// installer travels with the source being built (burrowee relay's shape) —
// the tree copy is the one that matches the binary by construction, and a
// release-repo copy can only ever match by coincidence:
//
//   - umbree (the client) ships <srcDir>/install/install.sh verbatim when the
//     cli tree carries one (feature 02 of the brand-root project moves it
//     there). Until it does, STABLE falls back to this repo's committed
//     inner/umbree/install.sh with a loud warning — that path is unchanged
//     and already hazardous (it assumes a `service` verb the stable cli may
//     not have; release README). BETA refuses the fallback outright: a beta
//     that ships the release repo's copy would be the split burrowee is
//     stuck in. Once feature 02 is on main the fallback and inner/umbree/
//     are deleted together.
//   - umbreed (the daemon) ships its own repo's canonical
//     install/install.sh.in, rendered with the build stamp substituted for
//     the __UMBREED_VERSION__ placeholder — the same rule, a different file
//     name. There used to be an inner/umbreed/install.sh in this repo too —
//     see the comment at the "umbreed" case below for why it was deleted.
//
// Mirrors render_inner in clawee's tools/release.sh, which draws the same
// line between clawee and claweed for the same reason.
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
		// No inner/umbreed/install.sh in this repo — deliberately. Clawee
		// used to keep one, "kept current for shellcheck + reference", but
		// nothing could actually enforce currency from a repo that cannot
		// see the canonical file: it drifted 600+ lines out of date while
		// still documenting a retired setuid installer tier. A second copy
		// of a file this repo does not own is a lie waiting to happen — read
		// the daemon repo's canonical template instead, at build time, from
		// its own source worktree (SrcDir, resolved from UMBREE_SRC_UMBREED).
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
	// 1. CVE gate (fail-closed) — scan the component module. Skipped only
	//    when o.SkipGate is set, which buildRun does once the gate has
	//    already run (or been explicitly bypassed) itself; any other caller
	//    gets the gate mandatory by default.
	if !o.SkipGate {
		if err := vulncheck.Gate(ctx, []vulncheck.Module{{Name: o.Component, Dir: o.SrcDir}},
			vulncheck.GateOpts{ReportDir: filepath.Join(o.OutDir, "vulncheck")}); err != nil {
			return nil, fmt.Errorf("cve gate: %w", err)
		}
	}
	// 2. Stamp (read-only, no bump), from the channel's version file.
	stamp, err := relconfig.StampFor(ctx, o.Channel, filepath.Join(o.RepoDir, "versions"), o.Component, o.SrcDir)
	if err != nil {
		return nil, err
	}
	// 3. Build component matrix.
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

	// env-literal guard: parity with release.sh — abort the cut if any built bin
	// embeds a forbidden config-env literal (see tools/verify-no-env.sh).
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

	// 4. install.sh — from the component tree when it carries one (see
	//    renderInstall).
	installSh := filepath.Join(o.OutDir, stamp, "install.sh")
	installFromTree, err := renderInstall(o.Component, stamp, o.SrcDir, o.RepoDir, installSh, o.Channel)
	if err != nil {
		return nil, fmt.Errorf("install.sh: %w", err)
	}

	// 4b. The migration ladder beside the installer — <SrcDir>/install/
	//     migrations/, staged into every zip under migrations/. Absent ⇒
	//     nothing staged (stageMigrations). A beta that ships a tree
	//     installer with no ladder is still a valid beta; say so where the
	//     operator reads .release.log rather than refusing.
	migs, err := stageMigrations(o.SrcDir)
	if err != nil {
		return nil, err
	}
	if o.Channel == relconfig.ChannelBeta && len(migs) == 0 && installFromTree {
		feature := map[string]string{"umbree": "02", "umbreed": "03"}[o.Component]
		fmt.Fprintf(os.Stderr, "⚠ %s: beta build ships an installer from %s but no install/migrations/ — feature %s adds the ladder; this beta carries none\n", o.Component, o.SrcDir, feature)
	}

	// 5. Assemble one flat zip per target: component bins + install.sh +
	//    the ladder.
	zips, err := assemble(o.Component, stamp, o.OutDir, installSh, migs, arts)
	if err != nil {
		return nil, fmt.Errorf("assemble: %w", err)
	}
	res.Zips = zips

	// 5b. Notarize darwin zips when --apple is set. Notarization submits the
	//     zip to Apple for review; it does NOT alter zip bytes (bare-binary
	//     zips aren't stapled — the ticket lives in Apple's online DB, checked
	//     at gatekeeper-assess time). --dry-run skips the real submission
	//     (logs intent) since dry-run artifacts are throwaway and notarizing
	//     is a real, rate-limited Apple API call.
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

	// 6. Checksum + sign the assembled zips. Per-target zip names are unique
	//    (unlike the raw artifacts, where every target ships the same bin
	//    basenames), so WriteSums's duplicate-basename guard never trips here.
	sums := filepath.Join(o.OutDir, stamp, "SHA256SUMS.txt")
	if err := checksum.WriteSums(res.Zips, sums); err != nil {
		return nil, fmt.Errorf("checksum: %w", err)
	}
	key := o.MinisignKey
	if key == "" {
		key = filepath.Join(o.RepoDir, "tools", "testkeys", "test.key")
	}
	if _, statErr := os.Stat(key); statErr != nil {
		// An explicit MinisignKey that doesn't resolve to a real file must fail
		// the cut, not silently skip signing (a signed release with no
		// SHA256SUMS.txt.minisig is unpublishable). Only the o.MinisignKey==""
		// fixture fallback (test key, absent in unit-test repos) tolerates a
		// missing file.
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

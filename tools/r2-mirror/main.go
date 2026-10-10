package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"umbree-release-r2-mirror/layout"
	"umbree-release-r2-mirror/manifest"
	"umbree-release-r2-mirror/r2"
)

const (
	sumsName    = "SHA256SUMS.txt"
	minisigName = "SHA256SUMS.txt.minisig"
	storePublic = "public"
	storeGated  = "gated"
)

type config struct {
	account  string
	bucket   string
	stageDir string
	comp     string
	channel  string
	version  string
	stamp    string
	creds    string
	dryRun   bool
	store    string
	receipt  string
}

func (c config) keyPrefix() string {
	if c.channel == "beta" {
		return c.comp + "/beta/"
	}
	return c.comp + "/"
}

func plannedKeys(cfg config, artifacts []string) []string {
	keys := make([]string, 0, len(artifacts)+1)
	for _, name := range artifacts {
		keys = append(keys, cfg.keyPrefix()+cfg.stamp+"/"+name)
	}
	return append(keys, cfg.keyPrefix()+"latest.json")
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ r2-mirror: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var cfg config
	flag.StringVar(&cfg.account, "account", "", "Cloudflare R2 account id")
	flag.StringVar(&cfg.bucket, "bucket", "", "R2 bucket name (e.g. umbree-downloads); required for the gated store, never defaulted")
	flag.StringVar(&cfg.stageDir, "stage-dir", "", "per-stamp dist directory to mirror")
	flag.StringVar(&cfg.comp, "comp", "", "component name (umbree | umbreed)")
	flag.StringVar(&cfg.channel, "channel", "stable", "release channel: stable | beta on the public store (beta keys go under <comp>/beta/); production on the gated store")
	flag.StringVar(&cfg.version, "version", "", "human semver, e.g. 0.1.66")
	flag.StringVar(&cfg.stamp, "stamp", "", "full release stamp, e.g. v0.1.66.2026.06.28.12e6b0fc")
	flag.StringVar(&cfg.creds, "creds", "", "path to the r2.key TOML (access_key_id + secret_access_key)")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "print the planned keys and upload nothing")
	flag.StringVar(&cfg.store, "store", storePublic, "public: artifacts then <comp>/[beta/]latest.json last | gated: artifacts only under <comp>/<channel>/<stamp>/, no manifest")
	flag.StringVar(&cfg.receipt, "receipt", "", "gated store: path the upload receipt (key, size, sha256 per object) is written to")
	flag.Parse()
	return execute(context.Background(), cfg, os.Stdout, nil)
}

func execute(ctx context.Context, cfg config, out io.Writer, doer r2.Doer) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	artifacts, zips, err := collectArtifacts(cfg.stageDir)
	if err != nil {
		return err
	}
	keys, err := uploadPlan(cfg, artifacts)
	if err != nil {
		return err
	}
	if cfg.dryRun {
		printPlan(out, cfg, keys)
		return nil
	}
	accessKeyID, secret, err := r2.ReadCreds(cfg.creds)
	if err != nil {
		return err
	}
	client := r2.New(cfg.account, cfg.bucket, accessKeyID, secret, doer)
	if cfg.isGated() {
		return stageGated(ctx, cfg, out, client, artifacts, keys)
	}
	return mirrorPublic(ctx, cfg, out, client, artifacts, keys, zips)
}

func uploadPlan(cfg config, artifacts []string) ([]string, error) {
	if cfg.isGated() {
		return gatedKeys(cfg, artifacts)
	}
	return plannedKeys(cfg, artifacts), nil
}

func printPlan(out io.Writer, cfg config, keys []string) {
	if cfg.isGated() {
		fmt.Fprintf(out, "dry-run: would stage %d objects to the gated store:\n", len(keys))
	} else {
		fmt.Fprintf(out, "dry-run: would upload %d objects to bucket %q:\n", len(keys), cfg.bucket)
	}
	for _, key := range keys {
		fmt.Fprintf(out, "  %s  (%s)\n", key, contentType(key))
	}
	if cfg.isGated() {
		fmt.Fprintln(out, "no manifest (gated store)")
	}
}

func uploadAll(ctx context.Context, out io.Writer, client *r2.Client, stageDir string, artifacts, keys []string) ([]receiptObject, error) {
	objects := make([]receiptObject, 0, len(artifacts))
	for i, name := range artifacts {
		key := keys[i]
		body, err := os.ReadFile(filepath.Join(stageDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		if err := client.Put(ctx, key, body, contentType(name)); err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "  uploaded %s (%d bytes)\n", key, len(body))
		objects = append(objects, newReceiptObject(key, body))
	}
	return objects, nil
}

func mirrorPublic(ctx context.Context, cfg config, out io.Writer, client *r2.Client, artifacts, keys, zips []string) error {
	manifestBody, err := buildManifest(cfg, zips).Encode()
	if err != nil {
		return err
	}
	manifestKey := keys[len(keys)-1]
	if _, err := uploadAll(ctx, out, client, cfg.stageDir, artifacts, keys); err != nil {
		return err
	}
	if err := client.Put(ctx, manifestKey, manifestBody, contentType(manifestKey)); err != nil {
		return err
	}
	fmt.Fprintf(out, "  uploaded %s (%d bytes)\n", manifestKey, len(manifestBody))
	fmt.Fprintf(out, "✓ mirrored %s %s to bucket %q\n", cfg.comp, cfg.stamp, cfg.bucket)
	return nil
}

func missingFlag(name, val string) error {
	if strings.TrimSpace(val) == "" {
		return fmt.Errorf("missing required flag --%s", name)
	}
	return nil
}

func (c config) validate() error {
	for _, f := range []struct{ name, val string }{
		{"comp", c.comp}, {"version", c.version}, {"stamp", c.stamp}, {"stage-dir", c.stageDir},
	} {
		if err := missingFlag(f.name, f.val); err != nil {
			return err
		}
	}
	var err error
	switch c.store {
	case storePublic, "":
		err = c.validatePublic()
	case storeGated:
		err = c.validateGated()
	default:
		err = fmt.Errorf("unknown store %q (want %s | %s)", c.store, storePublic, storeGated)
	}
	if err != nil {
		return err
	}
	if err := c.validateStageDir(); err != nil || c.dryRun {
		return err
	}
	for _, f := range []struct{ name, val string }{
		{"account", c.account}, {"bucket", c.bucket}, {"creds", c.creds},
	} {
		if err := missingFlag(f.name, f.val); err != nil {
			return err
		}
	}
	return nil
}

func (c config) validatePublic() error {
	if !slices.Contains(layout.Components, c.comp) {
		return fmt.Errorf("unknown component %q (want umbree | umbreed)", c.comp)
	}
	switch c.channel {
	case "stable":
		if !layout.StableStampRe.MatchString(c.stamp) {
			return fmt.Errorf("stamp %q is not a stable stamp (want v<X.Y.Z>.<YYYY>.<MM>.<DD>.<sha8>); a beta stamp needs --channel beta", c.stamp)
		}
	case "beta":
		if !layout.BetaStampRe.MatchString(c.stamp) {
			return fmt.Errorf("stamp %q is not a beta stamp (want v<X.Y.Z>.beta.<YYYY>.<MM>.<DD>.<sha8>); --channel beta takes only beta stamps", c.stamp)
		}
	default:
		return fmt.Errorf("unknown channel %q (want stable | beta)", c.channel)
	}
	return nil
}

func (c config) validateStageDir() error {
	info, err := os.Stat(c.stageDir)
	if err != nil {
		return fmt.Errorf("stage-dir %q: %w", c.stageDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("stage-dir %q is not a directory", c.stageDir)
	}
	return nil
}

func collectArtifacts(stageDir string) (artifacts, zips []string, err error) {
	entries, err := os.ReadDir(stageDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read stage-dir %q: %w", stageDir, err)
	}
	var hasSums, hasMinisig bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".zip"):
			artifacts = append(artifacts, name)
			zips = append(zips, name)
		case name == sumsName:
			artifacts = append(artifacts, name)
			hasSums = true
		case name == minisigName:
			artifacts = append(artifacts, name)
			hasMinisig = true
		}
	}
	if len(zips) == 0 {
		return nil, nil, fmt.Errorf("no *.zip artifacts in %q", stageDir)
	}
	if !hasSums {
		return nil, nil, fmt.Errorf("%s missing from %q", sumsName, stageDir)
	}
	if !hasMinisig {
		return nil, nil, fmt.Errorf("%s missing from %q", minisigName, stageDir)
	}
	slices.Sort(artifacts)
	slices.Sort(zips)
	return artifacts, zips, nil
}

func buildManifest(cfg config, zips []string) manifest.Manifest {
	return manifest.Build(cfg.comp, cfg.keyPrefix(), cfg.version, cfg.stamp, zips, time.Now())
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".zip"):
		return "application/zip"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	default:
		return "text/plain"
	}
}

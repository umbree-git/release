// Command r2-prune applies retention to the public Cloudflare R2 bucket behind
// downloads.umbree.org, ONE CHANNEL per run: it keeps the newest N per-stamp
// directories for each component under that channel's prefix (<comp>/ on
// stable, <comp>/beta/ on beta) and deletes every object beneath the older
// ones. Stable keeps 10, beta keeps 1 (prune.DefaultKeep).
//
// R2 is the install-time fallback mirror on stable (GitHub Releases stay
// primary) and the ONLY home of beta bytes, so it accumulated every stamp ever
// cut. This is the pass that bounds it.
//
// Usage:
//
//	r2-prune [--comp umbree|umbreed|all] [--channel stable|beta] [--keep N] [--execute]
//	         --account <id> --bucket <name> --creds <path to the r2 creds TOML>
//
// Dry-run by default: it prints the planned deletions and removes nothing.
// --execute performs them. Account, bucket and the S3 credentials are the
// operator's — flags, or the same UMBREE_R2_ACCOUNT / UMBREE_R2_BUCKET /
// UMBREE_R2_CREDS environment tools/release.sh reads; this file names none of
// them and the secret is never printed.
//
// ORDERING: run tools/prune-releases.sh (the GitHub side) BEFORE this, on the
// same channel. Draining R2 first leaves GitHub tags whose bytes are gone.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"umbree-release-r2-mirror/prune"
	"umbree-release-r2-mirror/r2"
)

// components is the full set r2-mirror publishes, and so the full set
// retention applies to. Literal here (this module must not import the parent
// repo's internal/relconfig), exactly as r2-mirror's own validate() spells it.
var components = []string{"umbree", "umbreed"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "✗ r2-prune: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	account := flag.String("account", os.Getenv("UMBREE_R2_ACCOUNT"), "Cloudflare R2 account id (default: $UMBREE_R2_ACCOUNT)")
	bucket := flag.String("bucket", envOr("UMBREE_R2_BUCKET", "umbree-downloads"), "R2 bucket name (default: $UMBREE_R2_BUCKET, else umbree-downloads)")
	creds := flag.String("creds", os.Getenv("UMBREE_R2_CREDS"), "path to the r2 creds TOML: access_key_id + secret_access_key (default: $UMBREE_R2_CREDS)")
	comp := flag.String("comp", "all", "component: umbree | umbreed | all")
	channel := flag.String("channel", "stable", "release channel: stable | beta")
	keep := flag.Int("keep", 0, "stamps to retain per component (default: 10 on stable, 1 on beta)")
	execute := flag.Bool("execute", false, "actually delete (default: dry-run)")
	flag.Parse()

	if flag.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q (this command takes flags only)", flag.Arg(0))
	}
	if *channel != "stable" && *channel != "beta" {
		return fmt.Errorf("unknown channel %q (want stable | beta)", *channel)
	}
	if *keep == 0 {
		*keep = prune.DefaultKeep(*channel)
	}

	comps := components
	if *comp != "all" {
		if !contains(components, *comp) {
			return fmt.Errorf("unknown component %q (want umbree | umbreed | all)", *comp)
		}
		comps = []string{*comp}
	}
	if *account == "" {
		return fmt.Errorf("no --account and UMBREE_R2_ACCOUNT is unset")
	}
	if *creds == "" {
		return fmt.Errorf("no --creds and UMBREE_R2_CREDS is unset")
	}
	accessKeyID, secret, err := readCreds(*creds)
	if err != nil {
		return err
	}

	client := r2.New(*account, *bucket, accessKeyID, secret, nil)
	ctx := context.Background()

	mode := "DRY-RUN"
	if *execute {
		mode = "EXECUTE"
	}
	fmt.Printf("bucket=%s  channel=%s  keep=%d  components=[%s]  mode=%s\n\n", *bucket, *channel, *keep, strings.Join(comps, " "), mode)

	total := 0
	for _, c := range comps {
		n, err := prune.Prune(ctx, client, c, *channel, *keep, *execute, os.Stdout)
		total += n
		if err != nil {
			return fmt.Errorf("prune %s: %w", c, err)
		}
	}

	fmt.Println()
	if *execute {
		fmt.Printf("✓ done — removed %d object(s); kept newest %d %s stamp(s) per component.\n", total, *keep, *channel)
	} else {
		fmt.Printf("DRY-RUN: %d object(s) would be removed. Re-run with --execute to apply.\n", total)
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// readCreds parses access_key_id + secret_access_key from a minimal TOML file
// (`key = "value"` or `key = value`, one per line; '#' comments allowed). The
// secret is returned to the caller and never logged. Same shape as the one in
// r2-mirror's main.go — the two binaries read the same file.
func readCreds(path string) (accessKeyID, secret string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read creds %q: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		switch strings.TrimSpace(key) {
		case "access_key_id":
			accessKeyID = val
		case "secret_access_key":
			secret = val
		}
	}
	if accessKeyID == "" || secret == "" {
		return "", "", fmt.Errorf("creds %q: missing access_key_id or secret_access_key", path)
	}
	return accessKeyID, secret, nil
}

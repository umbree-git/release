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

var components = []string{"umbree", "umbreed"}

const protectUsage = "permanent pin list (default: tools/retain-permanent or ../retain-permanent)"

func loadProtect(path string) (map[string]struct{}, error) {
	if path == "" {
		for _, p := range []string{"tools/retain-permanent", "../retain-permanent"} {
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				path = p
				break
			}
		}
	}
	return prune.LoadProtectFile(path)
}

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
	keep := flag.Int("keep", 0, fmt.Sprintf("stamps to retain per component (default: %d on stable, %d on beta)", prune.DefaultKeepStable, prune.DefaultKeepBeta))
	protectPath := flag.String("protect", "", protectUsage)
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

	protect, err := loadProtect(*protectPath)
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
		n, err := prune.PruneProtect(ctx, client, c, *channel, *keep, *execute, os.Stdout, protect)
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

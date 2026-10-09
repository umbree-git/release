package main

import (
	"flag"
	"strings"
)

const defaultListen = "127.0.0.1:8787"

type options struct {
	dataDir       string
	listen        string
	r2Account     string
	r2Creds       string
	gatedBucket   string
	publicBucket  string
	publicBaseURL string
	secretKey     string
	passwordStdin bool
	check         bool
	reason        string
	component     string
	args          []string
}

type envTwin struct {
	flag string
	env  string
	dest func(*options) *string
}

var envTwins = []envTwin{
	{"data-dir", "UMBREE_MANAGE_DATA_DIR", func(o *options) *string { return &o.dataDir }},
	{"listen", "UMBREE_MANAGE_LISTEN", func(o *options) *string { return &o.listen }},
	{"r2-account", "UMBREE_R2_ACCOUNT", func(o *options) *string { return &o.r2Account }},
	{"r2-creds", "UMBREE_R2_CREDS", func(o *options) *string { return &o.r2Creds }},
	{"gated-bucket", "UMBREE_R2_GATED_BUCKET", func(o *options) *string { return &o.gatedBucket }},
	{"public-bucket", "UMBREE_R2_BUCKET", func(o *options) *string { return &o.publicBucket }},
	{"public-base-url", "UMBREE_PUBLIC_BASE_URL", func(o *options) *string { return &o.publicBaseURL }},
}

var flagUsage = map[string]string{
	"data-dir":        "the `dir` holding the catalog (required; no default)",
	"listen":          "the `address` to bind (default " + defaultListen + ", loopback)",
	"r2-account":      "the R2 `account` id",
	"r2-creds":        "the `file` holding the R2 token",
	"gated-bucket":    "the private gated `bucket`; must differ from --public-bucket",
	"public-bucket":   "the public download `bucket`",
	"public-base-url": "the public download `url` the manifests are served from",
}

func twinUsage(name string) string {
	for _, t := range envTwins {
		if t.flag == name {
			return flagUsage[name] + " (env " + t.env + ")"
		}
	}
	return flagUsage[name]
}

func registerDataDir(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.dataDir, "data-dir", "", twinUsage("data-dir"))
}

func registerServe(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	for _, t := range envTwins[1:] {
		fs.StringVar(t.dest(o), t.flag, "", twinUsage(t.flag))
	}
}

func registerMigrate(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	fs.BoolVar(&o.check, "check", false, "report the ledger against this binary's migrations and write nothing (required)")
}

func (o *options) applyEnv(set map[string]bool, getenv func(string) string) {
	for _, t := range envTwins {
		dest := t.dest(o)
		if set[t.flag] {
			continue
		}
		*dest = strings.TrimSpace(getenv(t.env))
	}
	if o.listen == "" {
		o.listen = defaultListen
	}
}

func registerMarkYanked(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	fs.StringVar(&o.reason, "reason", "", "why the row is yanked by hand; recorded in the audit log (required)")
}

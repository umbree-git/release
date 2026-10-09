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
	trustedProxy  string
	staticDest    string
	staticSSHKey  string
	passwordStdin bool
	check         bool
	dryRun        bool
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
	{"secret-key", "UMBREE_MANAGE_SECRET_KEY", func(o *options) *string { return &o.secretKey }},
	{"trusted-proxy", "UMBREE_MANAGE_TRUSTED_PROXY", func(o *options) *string { return &o.trustedProxy }},
	{"r2-account", "UMBREE_R2_ACCOUNT", func(o *options) *string { return &o.r2Account }},
	{"r2-creds", "UMBREE_R2_CREDS", func(o *options) *string { return &o.r2Creds }},
	{"gated-bucket", "UMBREE_R2_GATED_BUCKET", func(o *options) *string { return &o.gatedBucket }},
	{"public-bucket", "UMBREE_R2_BUCKET", func(o *options) *string { return &o.publicBucket }},
	{"public-base-url", "UMBREE_PUBLIC_BASE_URL", func(o *options) *string { return &o.publicBaseURL }},
	{"static-dest", "UMBREE_MANAGE_STATIC_DEST", func(o *options) *string { return &o.staticDest }},
	{"static-ssh-key", "UMBREE_MANAGE_STATIC_SSH_KEY", func(o *options) *string { return &o.staticSSHKey }},
}

var flagUsage = map[string]string{
	"data-dir":        "the `dir` holding the catalog (required; no default)",
	"listen":          "the `address` to bind (default " + defaultListen + ", loopback)",
	"trusted-proxy":   "the TLS front's `ip` as this service sees it; only a peer with this exact address may name the client in X-Forwarded-For (rightmost entry). Unset: no forwarded header is read",
	"secret-key":      "the operator-provisioned `file` that seals TOTP secrets: an absolute, clean path to a regular 0600 file of 32 random bytes; never created here (required)",
	"r2-account":      "the R2 `account` id",
	"r2-creds":        "the `file` holding the R2 token",
	"gated-bucket":    "the private gated `bucket`; must differ from --public-bucket",
	"public-bucket":   "the public download `bucket`",
	"public-base-url": "the public download `url` the manifests are served from",
	"static-dest":     "where promote and yank republish the static surface: an absolute `dir` on this host, or <host>:<absolute dir> over scp",
	"static-ssh-key":  "the ssh `file` scp uses for a remote --static-dest: a key restricted on that host to writing under the static dir; never the operator's own",
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

func twinFlag(fs *flag.FlagSet, o *options, name string) {
	for _, t := range envTwins {
		if t.flag == name {
			fs.StringVar(t.dest(o), t.flag, "", twinUsage(t.flag))
		}
	}
}

func registerAdminKeyed(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	twinFlag(fs, o, "secret-key")
}

func registerAdminAdd(fs *flag.FlagSet, o *options) {
	registerAdminKeyed(fs, o)
	fs.BoolVar(&o.passwordStdin, "password-stdin", false, "read the password from the first line of stdin instead of prompting on the terminal")
}

func registerNothing(*flag.FlagSet, *options) {}

func registerUnlock(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	fs.StringVar(&o.reason, "reason", "", "why the admin is unlocked; recorded in the audit log (required)")
}

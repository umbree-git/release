package main

import (
	"flag"
	"fmt"
	"strings"

	"umbree-release-r2-mirror/r2"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

var newPublicStore = r2PublicStore

func r2PublicStore(o *options) (backend.Public, error) {
	accessKeyID, secret, err := r2.ReadCreds(o.r2Creds)
	if err != nil {
		return nil, err
	}
	return r2.New(o.r2Account, o.publicBucket, accessKeyID, secret, (&backend.Guard{}).Client()), nil
}

func registerBackfill(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	fs.StringVar(&o.component, "component", "", "the `component` whose public releases become catalog rows (required)")
	for _, name := range []string{"r2-account", "r2-creds", "public-bucket"} {
		for _, t := range envTwins {
			if t.flag == name {
				fs.StringVar(t.dest(o), t.flag, "", twinUsage(t.flag))
			}
		}
	}
}

func runBackfill(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	for _, req := range []struct{ flag, val string }{
		{"--data-dir", o.dataDir}, {"--component", o.component},
		{"--r2-account", o.r2Account}, {"--r2-creds", o.r2Creds}, {"--public-bucket", o.publicBucket},
	} {
		if strings.TrimSpace(req.val) == "" {
			return usagef(v, "%s is required", req.flag)
		}
	}
	public, err := newPublicStore(o)
	if err != nil {
		return err
	}
	key, err := intake.ReleaseMinisignKey()
	if err != nil {
		return err
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	d := publish.Deps{Store: st, Public: public, Key: key, Locks: publish.NewLocks(publish.DefaultLockWait)}
	rep, err := publish.Backfill(e.ctx, d, o.component)
	printBackfill(e, o.component, rep)
	return err
}

func printBackfill(e *env, component string, rep publish.BackfillReport) {
	fmt.Fprintf(e.stdout, "backfill %s: %d inserted, %d already catalogued, %d skipped, %d failed\n",
		component, len(rep.Inserted), len(rep.Existing), len(rep.Skipped), len(rep.Failed))
	if rep.Current != "" {
		fmt.Fprintf(e.stdout, "  current: %s\n", rep.Current)
	}
	for _, s := range rep.Skipped {
		fmt.Fprintf(e.stdout, "  skipped: %s\n", s)
	}
	for _, f := range rep.Failed {
		fmt.Fprintf(e.stdout, "  failed: %s\n", f)
	}
}

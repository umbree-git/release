package main

import (
	"flag"
	"fmt"
	"log/slog"
	"strings"

	"umbree-release-r2-mirror/r2"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
)

var newRetentionStores = r2RetentionStores

func r2Clients(o *options) (gated, public *r2.Client, err error) {
	accessKeyID, secret, err := r2.ReadCreds(o.r2Creds)
	if err != nil {
		return nil, nil, err
	}
	client := (&backend.Guard{}).Client()
	return r2.New(o.r2Account, o.gatedBucket, accessKeyID, secret, client),
		r2.New(o.r2Account, o.publicBucket, accessKeyID, secret, client), nil
}

func r2RetentionStores(o *options) (retention.Deleter, retention.PublicStore, error) {
	gated, public, err := r2Clients(o)
	if err != nil {
		return nil, nil, err
	}
	return gated, public, nil
}

func registerRetain(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	for _, name := range []string{"r2-account", "r2-creds", "gated-bucket", "public-bucket"} {
		twinFlag(fs, o, name)
	}
	fs.BoolVar(&o.dryRun, "dry-run", false, "print each window's plan, the keys it would delete and the rows it would expire, and change nothing")
}

func runRetain(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	for _, req := range []struct{ flag, val string }{
		{"--data-dir", o.dataDir}, {"--r2-account", o.r2Account}, {"--r2-creds", o.r2Creds},
		{"--gated-bucket", o.gatedBucket}, {"--public-bucket", o.publicBucket},
	} {
		if strings.TrimSpace(req.val) == "" {
			return usagef(v, "%s is required", req.flag)
		}
	}
	if o.gatedBucket == o.publicBucket {
		return usagef(v, "--gated-bucket and --public-bucket are both %q", o.gatedBucket)
	}
	gated, public, err := newRetentionStores(o)
	if err != nil {
		return err
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	r := &retention.Retainer{Store: st, Gated: gated, Public: public, Locks: publish.NewLocks(publish.DefaultLockWait),
		Log: slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))}
	if o.dryRun {
		plans, err := r.PlanAll(e.ctx)
		printPlans(e, plans)
		return err
	}
	reports, err := r.RetainAll(e.ctx, retention.ActorNightly)
	printReports(e, reports)
	return err
}

func printPlans(e *env, plans []retention.Plan) {
	for _, p := range plans {
		where := p.Component + "/" + p.Channel
		fmt.Fprintf(e.stdout, "plan %s %s %s\n", p.Window, where, p.Fingerprint())
		for _, k := range p.Kept {
			fmt.Fprintf(e.stdout, "  keep %s %s %s\n", p.Window, where, k)
		}
		for _, t := range p.Targets {
			for _, key := range t.Keys {
				fmt.Fprintf(e.stdout, "  DELETE %s %s\n", p.Window, key)
			}
			if t.Expires {
				fmt.Fprintf(e.stdout, "  EXPIRE %s %s %s\n", p.Window, where, t.Stamp)
			}
		}
		for _, s := range p.Skipped {
			fmt.Fprintf(e.stdout, "  SKIP %s %s %s: %s\n", p.Window, s.Stamp, s.Key, s.Reason)
		}
	}
}

func printReports(e *env, reports []retention.Report) {
	for _, r := range reports {
		fmt.Fprintln(e.stdout, r.Summary())
		for _, s := range r.Skipped {
			fmt.Fprintf(e.stdout, "  skipped %s %s: %s\n", s.Stamp, s.Key, s.Reason)
		}
	}
}

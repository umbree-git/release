package main

import (
	"flag"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"umbree-release-r2-mirror/r2"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/static"
)

const knownHostsFile = "static_known_hosts"

var newStaticSource = r2StaticSource

func r2StaticSource(o *options) (static.ManifestSource, error) {
	accessKeyID, secret, err := r2.ReadCreds(o.r2Creds)
	if err != nil {
		return nil, err
	}
	return r2.New(o.r2Account, o.publicBucket, accessKeyID, secret, (&backend.Guard{}).Client()), nil
}

func staticDestOf(o *options) (static.Dest, error) {
	return static.ParseDest(o.staticDest, o.staticSSHKey, filepath.Join(o.dataDir, knownHostsFile))
}

func registerPublishStatic(fs *flag.FlagSet, o *options) {
	registerDataDir(fs, o)
	for _, name := range []string{"r2-account", "r2-creds", "public-bucket", "public-base-url", "static-dest", "static-ssh-key"} {
		twinFlag(fs, o, name)
	}
}

func checkPublishStatic(v *verb, o *options) (static.Dest, error) {
	if len(o.args) != 1 || !catalog.ValidComponent(o.args[0]) {
		return static.Dest{}, usagef(v, "name one component (%s)", strings.Join(catalog.Components, " | "))
	}
	for _, req := range []struct{ flag, val string }{
		{"--data-dir", o.dataDir}, {"--r2-account", o.r2Account}, {"--r2-creds", o.r2Creds},
		{"--public-bucket", o.publicBucket}, {"--public-base-url", o.publicBaseURL}, {"--static-dest", o.staticDest},
	} {
		if strings.TrimSpace(req.val) == "" {
			return static.Dest{}, usagef(v, "%s is required", req.flag)
		}
	}
	dest, err := staticDestOf(o)
	if err != nil {
		return static.Dest{}, usagef(v, "--static-dest: %v", err)
	}
	return dest, nil
}

func runPublishStatic(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	dest, err := checkPublishStatic(v, o)
	if err != nil {
		return err
	}
	src, err := newStaticSource(o)
	if err != nil {
		return err
	}
	locks, err := publish.NewSharedLocks(publish.DefaultLockWait, o.dataDir)
	if err != nil {
		return err
	}
	d := publish.Deps{Locks: locks, Log: slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Static: &static.Publisher{Assets: release.Assets, Source: src, DownloadsBase: o.publicBaseURL, Dest: dest}}
	actor := "host:" + strings.TrimSpace(e.getenv("USER"))
	summary, err := publish.RepublishStatic(e.ctx, d, o.args[0], catalog.ChannelProduction, actor)
	if err != nil {
		return err
	}
	fmt.Fprintln(e.stdout, summary)
	return nil
}

package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"umbree-release-r2-mirror/r2"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/web"
)

const failureRetention = 30 * 24 * time.Hour

func buildService(o *options, log *slog.Logger) (http.Handler, *store.Store, error) {
	if log == nil {
		log = slog.Default()
	}
	sealer, err := auth.LoadSealer(o.secretKey)
	if err != nil {
		return nil, nil, fmt.Errorf("--secret-key: %w", err)
	}
	deps, err := publishDeps(o, log)
	if err != nil {
		return nil, nil, err
	}
	key, err := intake.ReleaseKey()
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	if err := st.PurgeExpiredSessions(now, now.Add(-failureRetention)); err != nil {
		log.Warn("could not purge expired sessions", "err", err)
	}
	deps.Store = st
	svc := auth.New(st, sealer, nil, log)
	if o.trustedProxy != "" {
		svc.TrustedProxy = netip.MustParseAddr(o.trustedProxy)
	}
	srv, err := web.New(web.Config{Store: st, Auth: svc, Intake: intake.New(st, key, nil, log),
		Publish: deps, PublicBaseURL: o.publicBaseURL, Log: log})
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return srv.Handler(), st, nil
}

func publishDeps(o *options, log *slog.Logger) (publish.Deps, error) {
	accessKeyID, secret, err := r2.ReadCreds(o.r2Creds)
	if err != nil {
		return publish.Deps{}, err
	}
	minisignKey, err := intake.ReleaseMinisignKey()
	if err != nil {
		return publish.Deps{}, err
	}
	guard := &backend.Guard{}
	client := guard.Client()
	return publish.Deps{
		Gated:   r2.New(o.r2Account, o.gatedBucket, accessKeyID, secret, client),
		Public:  r2.New(o.r2Account, o.publicBucket, accessKeyID, secret, client),
		Key:     minisignKey,
		Locks:   publish.NewLocks(publish.DefaultLockWait),
		Log:     log,
		Confirm: &publish.Confirmer{BaseURL: o.publicBaseURL, Fetcher: backend.NewFetcher(guard)},
	}, nil
}

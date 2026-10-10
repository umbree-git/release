package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	release "github.com/umbree-git/release"
	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/static"
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
	deps, retainer, err := publishDeps(o, log)
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
	deps.Store, retainer.Store = st, st
	deps.AfterPromote = retainer.AfterPromote
	in := intake.New(st, key, nil, log)
	in.RetainAfterStage(retainer.AfterStage, intake.RetentionBudget)
	svc := auth.New(st, sealer, nil, log)
	if o.trustedProxy != "" {
		svc.TrustedProxy = netip.MustParseAddr(o.trustedProxy)
	}
	srv, err := web.New(web.Config{Store: st, Auth: svc, Intake: in,
		Publish: deps, Retention: retainer, PublicBaseURL: o.publicBaseURL, Log: log})
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return srv.Handler(), st, nil
}

func publishDeps(o *options, log *slog.Logger) (publish.Deps, *retention.Retainer, error) {
	gated, public, err := r2Clients(o)
	if err != nil {
		return publish.Deps{}, nil, err
	}
	minisignKey, err := intake.ReleaseMinisignKey()
	if err != nil {
		return publish.Deps{}, nil, err
	}
	guard := &backend.Guard{}
	locks, err := publish.NewSharedLocks(publish.DefaultLockWait, o.dataDir)
	if err != nil {
		return publish.Deps{}, nil, err
	}
	dest, err := staticDestOf(o)
	if err != nil && !errors.Is(err, static.ErrNoDest) {
		return publish.Deps{}, nil, err
	}
	return publish.Deps{
			Gated:   gated,
			Public:  public,
			Key:     minisignKey,
			Locks:   locks,
			Log:     log,
			Confirm: &publish.Confirmer{BaseURL: o.publicBaseURL, Fetcher: backend.NewFetcher(guard)},
			Static:  &static.Publisher{Assets: release.Assets, Source: public, DownloadsBase: o.publicBaseURL, Dest: dest},
		},
		&retention.Retainer{Gated: gated, Public: public, Locks: locks, Log: log}, nil
}

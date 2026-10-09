package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/store"
)

func runServe(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	if err := checkServeOptions(v, o); err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	key, err := intake.ReleaseKey()
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	intake.New(st, key, nil, log).Routes(mux)
	ln, err := net.Listen("tcp", o.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.listen, err)
	}
	fmt.Fprintf(e.stdout, "%s listening on %s\n", toolName, ln.Addr())
	return serveUntilDone(e.ctx, ln, mux, log)
}

func checkServeOptions(v *verb, o *options) error {
	if o.dataDir == "" {
		return usagef(v, "--data-dir is required; a guessed data directory is either an empty second catalog or another deployment's")
	}
	if o.gatedBucket != "" && o.gatedBucket == o.publicBucket {
		return usagef(v, "--gated-bucket and --public-bucket are both %q; the gated store must be a different, private bucket", o.gatedBucket)
	}
	return nil
}

func serveUntilDone(ctx context.Context, ln net.Listener, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

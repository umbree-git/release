package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/backend"
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
	h, st, err := buildService(o, log)
	if err != nil {
		return err
	}
	defer st.Close()
	ln, err := net.Listen("tcp", o.listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.listen, err)
	}
	fmt.Fprintf(e.stdout, "%s listening on %s\n", toolName, ln.Addr())
	return serveUntilDone(e.ctx, ln, h, log)
}

var r2AccountRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func checkServeOptions(v *verb, o *options) error {
	if strings.TrimSpace(o.dataDir) == "" {
		return usagef(v, "--data-dir is required; a guessed data directory is either an empty second catalog or another deployment's")
	}
	if o.gatedBucket != "" && o.gatedBucket == o.publicBucket {
		return usagef(v, "--gated-bucket and --public-bucket are both %q; the gated store must be a different, private bucket", o.gatedBucket)
	}
	for _, req := range []struct{ flag, val string }{
		{"--secret-key", o.secretKey}, {"--r2-account", o.r2Account}, {"--r2-creds", o.r2Creds},
		{"--gated-bucket", o.gatedBucket}, {"--public-bucket", o.publicBucket}, {"--public-base-url", o.publicBaseURL},
	} {
		if strings.TrimSpace(req.val) == "" {
			return usagef(v, "%s is required; the console cannot sign anyone in, promote or link without it", req.flag)
		}
	}
	if !r2AccountRe.MatchString(o.r2Account) {
		return usagef(v, "--r2-account %q is not an account id; it becomes part of the storage host name", o.r2Account)
	}
	u, err := url.Parse(o.publicBaseURL)
	if err == nil {
		err = (&backend.Guard{}).CheckURL(u)
	}
	if err != nil {
		return usagef(v, "--public-base-url: %v", err)
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

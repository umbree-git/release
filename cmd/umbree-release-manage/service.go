package main

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/umbree-git/release/internal/manage/store"
)

func buildService(o *options, log *slog.Logger) (http.Handler, *store.Store, error) {
	return nil, nil, errors.New("not built")
}

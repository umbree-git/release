package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

type PublishAPI struct {
	Deps publish.Deps
}

type act func(*publish.Run, context.Context, io.Writer) error

func (a PublishAPI) Promote(w http.ResponseWriter, r *http.Request, actor string) {
	a.serve(w, r, actor, (*publish.Run).Promote)
}

func (a PublishAPI) Yank(w http.ResponseWriter, r *http.Request, actor string) {
	a.serve(w, r, actor, (*publish.Run).Yank)
}

func (a PublishAPI) serve(w http.ResponseWriter, r *http.Request, actor string, do act) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "row id is not a positive integer")
		return
	}
	run, status, msg := begin(r.Context(), a.Deps, id, actor)
	if run == nil {
		writeError(w, status, msg)
		return
	}
	defer run.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = do(run, r.Context(), w)
}

func begin(ctx context.Context, d publish.Deps, id int64, actor string) (*publish.Run, int, string) {
	run, err := publish.Begin(ctx, d, id, actor)
	switch {
	case errors.Is(err, publish.ErrBusy):
		return nil, http.StatusConflict, "channel busy"
	case errors.Is(err, store.ErrNotFound):
		return nil, http.StatusNotFound, "no such row"
	case err != nil:
		return nil, http.StatusInternalServerError, "could not start"
	}
	return run, http.StatusOK, ""
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

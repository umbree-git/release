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

func (a PublishAPI) Promote(w http.ResponseWriter, r *http.Request) {
	a.serve(w, r, (*publish.Run).Promote)
}

func (a PublishAPI) Yank(w http.ResponseWriter, r *http.Request) {
	a.serve(w, r, (*publish.Run).Yank)
}

func (a PublishAPI) serve(w http.ResponseWriter, r *http.Request, act func(*publish.Run, context.Context, io.Writer) error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "row id is not a positive integer")
		return
	}
	run, err := publish.Begin(r.Context(), a.Deps, id)
	switch {
	case errors.Is(err, publish.ErrBusy):
		writeError(w, http.StatusConflict, "channel busy")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such row")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not start")
		return
	}
	defer run.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = act(run, r.Context(), w)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

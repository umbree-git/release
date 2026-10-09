package web

import (
	"net/http"

	"github.com/umbree-git/release/internal/manage/publish"
)

type PublishAPI struct {
	Deps publish.Deps
}

func (a PublishAPI) Promote(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

func (a PublishAPI) Yank(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotImplemented)
}

package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
)

const versionCallback = "__umbreeVersion"

type versionPayload struct {
	Component string  `json:"component"`
	Channel   string  `json:"channel"`
	Version   *string `json:"version"`
	Stamp     *string `json:"stamp"`
}

func (s *Server) versionJS(component string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		payload := versionPayload{Component: component, Channel: catalog.ChannelProduction}
		current, err := s.cfg.Store.Current(component, catalog.ChannelProduction)
		switch {
		case err == nil:
			payload.Version, payload.Stamp = &current.Version, &current.Stamp
		case !errors.Is(err, store.ErrNotFound):
			s.cfg.Log.Error("version.js", "err", err, "component", component)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		body, err := json.Marshal(payload)
		if err != nil {
			s.cfg.Log.Error("version.js", "err", err, "component", component)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write([]byte(versionCallback + "(" + string(body) + ");\n"))
	}
}

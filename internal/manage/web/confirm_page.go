package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/umbree-git/release/internal/manage/store"
)

const (
	actionPromote = "promote"
	actionYank    = "yank"
	confirmField  = "confirm"
	confirmHeader = "X-Confirm-Token"
)

type confirmPage struct {
	pageData
	Row       rowView
	Component string
	Channel   string
	Verb      string
	Note      string
	Danger    bool
	Action    string
	Token     string
	Back      string
}

var actionNotes = map[string]string{
	actionPromote: "verifies the gated bytes, copies them to the public surface and writes the manifest last",
	actionYank:    "re-points the manifest at the newest other public release; no bytes are deleted",
}

func (s *Server) handleAction(action string, api bool) guarded {
	return func(w http.ResponseWriter, r *http.Request, sess *store.Session) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "row id is not a positive integer", http.StatusBadRequest)
			return
		}
		row, err := s.cfg.Store.Get(id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			s.pageError(w, r, err)
			return
		}
		token := r.Header.Get(confirmHeader)
		if token == "" {
			token = r.PostFormValue(confirmField)
		}
		if token == "" {
			s.askConfirm(w, r, sess, action, row, api)
			return
		}
		if err := s.confirm.consume(token, sess.ID, action, id); err != nil {
			http.Error(w, errBadConfirm.Error()+"; ask again from the overview", http.StatusForbidden)
			return
		}
		s.act(w, r, sess, action, row, api)
	}
}

func (s *Server) act(w http.ResponseWriter, r *http.Request, sess *store.Session, action string, row *store.ReleaseVersion, api bool) {
	switch {
	case api && action == actionPromote:
		s.api.Promote(w, r, sess.Admin)
	case api:
		s.api.Yank(w, r, sess.Admin)
	default:
		s.runPage(w, r, sess, action, row)
	}
}

func (s *Server) askConfirm(w http.ResponseWriter, r *http.Request, sess *store.Session, action string, row *store.ReleaseVersion, api bool) {
	token, err := s.confirm.mint(sess.ID, action, row.ID)
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	if api {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusPreconditionRequired)
		_ = json.NewEncoder(w).Encode(map[string]any{"confirm": token, "action": action, "row": row.ID})
		return
	}
	page := confirmPage{
		Row: s.view(*row), Component: row.Component, Channel: row.Channel,
		Verb: action, Note: actionNotes[action], Danger: action == actionYank,
		Action: r.URL.Path, Token: token, Back: pagePath(row.Channel, row.Component),
	}
	page.pageData = s.consolePage(w, r, sess, "Confirm "+action)
	s.render(w, r, "confirm", http.StatusOK, page)
}

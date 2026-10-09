package web

import (
	"errors"
	"net/http"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

type republishPage struct {
	pageData
	Channel   string
	Component string
	Action    string
	Back      string
	Token     string
	Result    string
	Failure   string
}

func republishPath(channel, component string) string {
	return "/manage/republish/" + channel + "/" + component
}

func republishAction(channel, component string) string {
	return "republish static " + component + "/" + channel
}

func (s *Server) handleRepublish(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	channel, comp, ok := pageTarget(r)
	if !ok || s.cfg.Publish.Static == nil {
		http.NotFound(w, r)
		return
	}
	page := republishPage{Channel: channel, Component: comp, Action: republishPath(channel, comp), Back: pagePath(channel, comp)}
	token := r.PostFormValue(confirmField)
	if token == "" {
		var err error
		if page.Token, err = s.confirm.mint(sess.ID, republishAction(channel, comp), 0); err != nil {
			s.pageError(w, r, err)
			return
		}
		page.pageData = s.consolePage(w, r, sess, "Republish static · "+comp)
		s.render(w, r, "republish", http.StatusOK, page)
		return
	}
	if err := s.confirm.consume(token, sess.ID, republishAction(channel, comp), 0); err != nil {
		http.Error(w, errBadConfirm.Error()+"; start again from the overview", http.StatusForbidden)
		return
	}
	summary, err := publish.RepublishStatic(r.Context(), s.cfg.Publish, comp, channel, sess.Admin)
	status := http.StatusOK
	switch {
	case errors.Is(err, publish.ErrBusy):
		status, page.Failure = http.StatusConflict, err.Error()+"; try again when the promote or yank finishes"
	case err != nil:
		status, page.Failure = http.StatusBadGateway, err.Error()
	default:
		page.Result = summary
	}
	page.pageData = s.consolePage(w, r, sess, "Republish static · "+comp)
	s.render(w, r, "republish", status, page)
}

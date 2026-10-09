package web

import (
	"errors"
	"net/http"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
)

const fingerprintField = "fingerprint"

type retentionPage struct {
	pageData
	Channel     string
	Component   string
	Window      string
	Action      string
	Back        string
	Plan        *retention.Plan
	Fingerprint string
	Token       string
	Report      *retention.Report
	Failure     string
}

func retentionPath(channel, component string, w retention.Window) string {
	return pagePath(channel, component) + "/retention/" + string(w)
}

func retentionAction(w retention.Window, channel, component, fingerprint string) string {
	return "retention " + string(w) + " " + component + "/" + channel + " " + fingerprint
}

func (s *Server) handleRetention(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	http.NotFound(w, r)
	return
	channel, comp, ok := pageTarget(r)
	win, err := retention.ParseWindow(r.PathValue("window"))
	if !ok || err != nil || s.cfg.Retention == nil {
		http.NotFound(w, r)
		return
	}
	page := retentionPage{Channel: channel, Component: comp, Window: string(win),
		Action: retentionPath(channel, comp, win), Back: pagePath(channel, comp)}
	token := r.PostFormValue(confirmField)
	if token == "" {
		s.previewRetention(w, r, sess, win, page)
		return
	}
	fingerprint := r.PostFormValue(fingerprintField)
	if err := s.confirm.consume(token, sess.ID, retentionAction(win, channel, comp, fingerprint), 0); err != nil {
		http.Error(w, errBadConfirm.Error()+"; preview again from the overview", http.StatusForbidden)
		return
	}
	s.confirmRetention(w, r, sess, win, fingerprint, page)
}

func (s *Server) previewRetention(w http.ResponseWriter, r *http.Request, sess *store.Session, win retention.Window, page retentionPage) {
	plan, err := s.cfg.Retention.Plan(r.Context(), page.Component, page.Channel, win)
	if err != nil {
		s.cfg.Log.Error("retention preview", "window", win, "component", page.Component, "err", err)
		http.Error(w, "the retention plan could not be computed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	page.Plan, page.Fingerprint = &plan, plan.Fingerprint()
	page.Token, err = s.confirm.mint(sess.ID, retentionAction(win, page.Channel, page.Component, page.Fingerprint), 0)
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	page.pageData = s.consolePage(w, r, sess, "Clean "+string(win)+" · "+page.Component)
	s.render(w, r, "retention", http.StatusOK, page)
}

func (s *Server) confirmRetention(w http.ResponseWriter, r *http.Request, sess *store.Session, win retention.Window, fingerprint string, page retentionPage) {
	rep, err := s.cfg.Retention.Confirm(r.Context(), page.Component, page.Channel, win, fingerprint, sess.Admin)
	status := http.StatusOK
	switch {
	case errors.Is(err, retention.ErrPlanChanged):
		status, page.Failure = http.StatusConflict, err.Error()+"; preview again"
	case errors.Is(err, publish.ErrBusy):
		status, page.Failure = http.StatusConflict, err.Error()+"; try again when the promote or yank finishes"
	case err != nil:
		page.Failure = err.Error()
		page.Report = &rep
	default:
		page.Report = &rep
	}
	s.cfg.Log.Info("release retention", "actor", sess.Admin, "window", win, "component", page.Component,
		"fingerprint", fingerprint, "result", rep.Summary(), "err", err)
	page.pageData = s.consolePage(w, r, sess, "Clean "+string(win)+" · "+page.Component)
	s.render(w, r, "retention", status, page)
}

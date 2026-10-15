package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
)

const refusalMessage = "Sign-in refused."

type pageData struct {
	Title string
	Admin string
	Who   string
	CSRF  string
	Error string
	Nav   []navItem
	Tabs  []navItem
}

type overviewPage struct {
	pageData
	Channel    string
	Component  string
	Current    *rowView
	Promotable *rowView
	Retention  []navItem
	Republish  string
}

type historyPage struct {
	pageData
	Channel   string
	Component string
	Rows      []rowView
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, status int, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := s.pages[name].ExecuteTemplate(w, "layout", data); err != nil {
		s.cfg.Log.Error("render", "template", name, "err", err, "path", r.URL.Path)
	}
}

func (s *Server) consolePage(w http.ResponseWriter, r *http.Request, sess *store.Session, title, channel, comp string) pageData {
	csrf, err := s.cfg.Auth.CSRFToken(w, r, sess)
	if err != nil {
		s.cfg.Log.Error("csrf token", "err", err, "path", r.URL.Path)
	}
	return pageData{Title: title, Admin: sess.Admin, Who: localPart(sess.Admin), CSRF: csrf, Nav: componentNav(channel, comp, pagePath)}
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	http.Redirect(w, r, pagePath(catalog.ChannelProduction, catalog.Components[0]), http.StatusSeeOther)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	channel, comp, ok := pageTarget(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	page := overviewPage{Channel: channel, Component: comp}
	current, err := s.cfg.Store.Current(comp, channel)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.pageError(w, r, err)
		return
	}
	if current != nil {
		v := s.view(*current)
		page.Current = &v
	}
	promotable, err := s.cfg.Store.Promotable(comp, channel)
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	if len(promotable) > 0 {
		v := s.view(promotable[0])
		page.Promotable = &v
	}
	if s.cfg.Retention != nil {
		for _, win := range retention.Windows {
			page.Retention = append(page.Retention, navItem{Name: string(win), Path: retentionPath(channel, comp, win)})
		}
	}
	if s.cfg.Publish.Static != nil {
		page.Republish = republishPath(channel, comp)
	}
	page.pageData = s.consolePage(w, r, sess, comp+" · "+channel, channel, comp)
	page.Tabs = pageTabs(channel, comp, false)
	s.render(w, r, "index", http.StatusOK, page)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	channel, comp, ok := pageTarget(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rows, err := s.cfg.Store.List(comp, channel)
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	page := historyPage{Channel: channel, Component: comp}
	for _, rv := range rows {
		page.Rows = append(page.Rows, s.view(rv))
	}
	page.pageData = s.consolePage(w, r, sess, comp+" history · "+channel, channel, comp)
	page.Tabs = pageTabs(channel, comp, true)
	s.render(w, r, "history", http.StatusOK, page)
}

func (s *Server) pageError(w http.ResponseWriter, r *http.Request, err error) {
	s.cfg.Log.Error("manage page", "err", err, "path", r.URL.Path)
	http.Error(w, "the catalog could not be read", http.StatusInternalServerError)
}

func (s *Server) refuse(w http.ResponseWriter, r *http.Request) {
	auth.ClearCookies(w)
	s.render(w, r, "login", http.StatusUnauthorized, pageData{Title: "Sign in", Error: refusalMessage})
}

func localPart(admin string) string {
	name, _, _ := strings.Cut(admin, "@")
	return name
}

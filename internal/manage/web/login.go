package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/umbree-git/release/internal/manage/auth"
)

const maxLoginForm = 16 << 10

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, err := s.cfg.Auth.Session(r); err == nil {
		http.Redirect(w, r, "/manage", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", http.StatusOK, pageData{Title: "Sign in"})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxLoginForm)
	if err := r.ParseForm(); err != nil {
		s.cfg.Log.Info("sign-in refused: unreadable form", "ip", auth.ClientIP(r), "err", err)
		s.refuse(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	csrf, err := s.cfg.Auth.StartLogin(w, r, name, r.PostFormValue("password"))
	switch {
	case errors.Is(err, auth.ErrRefused):
		s.cfg.Log.Info("sign-in refused at the password step", "name", name, "ip", auth.ClientIP(r))
		s.refuse(w, r)
	case errors.Is(err, auth.ErrRateLimited):
		s.render(w, r, "login", http.StatusTooManyRequests, pageData{Title: "Sign in", Error: "Too many attempts; wait a few minutes."})
	case errors.Is(err, auth.ErrBusy):
		w.Header().Set("Retry-After", "2")
		s.render(w, r, "login", http.StatusServiceUnavailable, pageData{Title: "Sign in", Error: "Sign-in is busy; try again in a moment."})
	case err != nil:
		s.cfg.Log.Error("sign-in", "err", err)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
	default:
		s.render(w, r, "totp", http.StatusOK, pageData{Title: "Verification code", CSRF: csrf})
	}
}

func (s *Server) handleTOTPPage(w http.ResponseWriter, r *http.Request) {
	sess, err := s.cfg.Auth.PendingSession(r)
	if err != nil {
		http.Redirect(w, r, "/manage/login", http.StatusSeeOther)
		return
	}
	csrf, err := s.cfg.Auth.CSRFToken(w, r, sess)
	if err != nil {
		s.cfg.Log.Error("csrf token", "err", err)
	}
	s.render(w, r, "totp", http.StatusOK, pageData{Title: "Verification code", CSRF: csrf})
}

func (s *Server) handleTOTPSubmit(w http.ResponseWriter, r *http.Request) {
	sess, err := s.cfg.Auth.PendingSession(r)
	if err != nil {
		s.refuse(w, r)
		return
	}
	if err := s.cfg.Auth.CheckCSRF(r, sess); err != nil {
		http.Error(w, "missing or invalid CSRF token", http.StatusForbidden)
		return
	}
	err = s.cfg.Auth.CompleteTOTP(w, r, r.PostFormValue("code"))
	switch {
	case errors.Is(err, auth.ErrRefused):
		s.cfg.Log.Info("sign-in refused at the code step", "admin", sess.Admin, "ip", auth.ClientIP(r))
		s.refuse(w, r)
	case errors.Is(err, auth.ErrRateLimited):
		s.render(w, r, "totp", http.StatusTooManyRequests, pageData{Title: "Verification code", CSRF: r.PostFormValue(auth.CSRFField), Error: "Too many attempts; wait a few minutes."})
	case err != nil:
		s.cfg.Log.Error("second factor", "err", err)
		http.Error(w, "sign-in failed", http.StatusInternalServerError)
	default:
		http.Redirect(w, r, "/manage", http.StatusSeeOther)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, err := s.cfg.Auth.Session(r)
	if err != nil {
		sess, err = s.cfg.Auth.PendingSession(r)
	}
	if err == nil {
		if err := s.cfg.Auth.CheckCSRF(r, sess); err != nil {
			http.Error(w, "missing or invalid CSRF token", http.StatusForbidden)
			return
		}
	}
	s.cfg.Auth.Logout(w, r)
	http.Redirect(w, r, "/manage/login", http.StatusSeeOther)
}

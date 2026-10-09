package web

import (
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
)

//go:embed templates static
var assets embed.FS

var pageNames = []string{"login", "totp", "index", "history", "confirm", "progress", "retention"}

type Routes interface {
	Routes(mux *http.ServeMux)
}

type Config struct {
	Store         *store.Store
	Auth          *auth.Service
	Intake        Routes
	Publish       publish.Deps
	Retention     *retention.Retainer
	PublicBaseURL string
	Log           *slog.Logger
	Now           func() time.Time
}

type Server struct {
	cfg     Config
	pages   map[string]*template.Template
	confirm *confirmer
	api     PublishAPI
}

type guarded func(w http.ResponseWriter, r *http.Request, sess *store.Session)

func New(cfg Config) (*Server, error) {
	if cfg.Store == nil || cfg.Auth == nil {
		return nil, errors.New("web: a store and an auth service are required")
	}
	if u, err := url.Parse(cfg.PublicBaseURL); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("web: the public base URL %q is not an https URL", cfg.PublicBaseURL)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &Server{cfg: cfg, pages: map[string]*template.Template{}, api: PublishAPI{Deps: cfg.Publish}}
	confirm, err := newConfirmer(func() int64 { return s.cfg.Now().Unix() })
	if err != nil {
		return nil, err
	}
	s.confirm = confirm
	for _, name := range pageNames {
		t, err := template.ParseFS(assets, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("web: parse template %q: %w", name, err)
		}
		s.pages[name] = t
	}
	return s, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	if s.cfg.Intake != nil {
		s.cfg.Intake.Routes(mux)
	}
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.HandleFunc("GET /manage/static/site.css", handleStylesheet)
	mux.HandleFunc("GET /manage/login", s.handleLoginPage)
	mux.HandleFunc("POST /manage/login", s.handleLoginSubmit)
	mux.HandleFunc("GET /manage/login/totp", s.handleTOTPPage)
	mux.HandleFunc("POST /manage/login/totp", s.handleTOTPSubmit)
	mux.HandleFunc("POST /manage/logout", s.handleLogout)
	mux.HandleFunc("GET /manage", s.guard(s.handleRoot))
	mux.HandleFunc("GET /manage/{$}", s.guard(s.handleRoot))
	mux.HandleFunc("GET /manage/{channel}/{component}", s.guard(s.handleOverview))
	mux.HandleFunc("GET /manage/{channel}/{component}/history", s.guard(s.handleHistory))
	for _, action := range []string{actionPromote, actionYank} {
		mux.HandleFunc("POST /manage/releases/{id}/"+action, s.writeGuard(s.handleAction(action, false)))
		mux.HandleFunc("POST /manage/api/releases/{id}/"+action, s.writeGuard(s.handleAction(action, true)))
	}
	mux.HandleFunc("POST /manage/retention/{channel}/{component}/{window}", s.writeGuard(s.handleRetention))
	mux.HandleFunc("/manage/", s.guard(handleNoPage))
	mux.HandleFunc("/", http.NotFound)
	return secureHeaders(mux)
}

func (s *Server) guard(h guarded) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.cfg.Auth.Session(r)
		if err != nil {
			if !errors.Is(err, auth.ErrUnauthorized) {
				s.cfg.Log.Error("session", "err", err, "path", r.URL.Path)
			}
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				http.Redirect(w, r, "/manage/login", http.StatusSeeOther)
				return
			}
			http.Error(w, "sign in at /manage/login", http.StatusUnauthorized)
			return
		}
		h(w, r, sess)
	}
}

func (s *Server) writeGuard(h guarded) http.HandlerFunc {
	return s.guard(func(w http.ResponseWriter, r *http.Request, sess *store.Session) {
		if err := s.cfg.Auth.CheckCSRF(r, sess); err != nil {
			if !errors.Is(err, auth.ErrCSRF) {
				s.cfg.Log.Error("csrf", "err", err, "path", r.URL.Path)
			}
			http.Error(w, "missing or invalid CSRF token", http.StatusForbidden)
			return
		}
		h(w, r, sess)
	})
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

func handleStylesheet(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	http.ServeFileFS(w, r, assets, "static/site.css")
}

func handleNoPage(w http.ResponseWriter, r *http.Request, _ *store.Session) {
	http.Error(w, "no such manage page", http.StatusNotFound)
}

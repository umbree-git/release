package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

type Routes interface {
	Routes(mux *http.ServeMux)
}

type Config struct {
	Store         *store.Store
	Auth          *auth.Service
	Intake        Routes
	Publish       publish.Deps
	PublicBaseURL string
	Log           *slog.Logger
	Now           func() time.Time
}

type Server struct {
	cfg Config
}

func New(cfg Config) (*Server, error) { return &Server{cfg: cfg}, nil }

func (s *Server) Handler() http.Handler { return http.NewServeMux() }

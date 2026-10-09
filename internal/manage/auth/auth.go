package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

const (
	SessionCookie = "umbree_manage_session"
	CSRFCookie    = "umbree_manage_csrf"
	CSRFHeader    = "X-CSRF-Token"
	CSRFField     = "csrf_token"
	CookiePath    = "/manage"
	SessionTTL    = 12 * time.Hour
	PendingTTL    = 10 * time.Minute
	Issuer        = "Umbree Release"
)

var (
	ErrRefused      = errors.New("sign-in refused")
	ErrRateLimited  = errors.New("auth: too many attempts")
	ErrUnauthorized = errors.New("auth: no valid session")
	ErrCSRF         = errors.New("auth: missing or invalid CSRF token")
	errNotBuilt     = errors.New("auth: not built")
)

type Service struct {
	Store  *store.Store
	Sealer *Sealer
	Now    func() time.Time
	Log    *slog.Logger
}

type Enrolment struct {
	Secret     string
	OTPAuthURL string
}

func New(st *store.Store, sealer *Sealer, now func() time.Time, log *slog.Logger) *Service {
	return &Service{Store: st, Sealer: sealer, Now: now, Log: log}
}

func (s *Service) AddAdmin(name, password string) (*Enrolment, error) { return nil, errNotBuilt }

func (s *Service) ResetTOTP(name string) (*Enrolment, error) { return nil, errNotBuilt }

func (s *Service) StartLogin(w http.ResponseWriter, r *http.Request, name, password string) (string, error) {
	return "", errNotBuilt
}

func (s *Service) CompleteTOTP(w http.ResponseWriter, r *http.Request, code string) error {
	return errNotBuilt
}

func (s *Service) Session(r *http.Request) (*store.Session, error) { return nil, errNotBuilt }

func (s *Service) PendingSession(r *http.Request) (*store.Session, error) { return nil, errNotBuilt }

func (s *Service) CheckCSRF(r *http.Request, sess *store.Session) error { return errNotBuilt }

func (s *Service) CSRFToken(w http.ResponseWriter, r *http.Request, sess *store.Session) (string, error) {
	return "", errNotBuilt
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {}

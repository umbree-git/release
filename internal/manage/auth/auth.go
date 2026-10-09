package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
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
)

type Service struct {
	Store  *store.Store
	Sealer *Sealer
	Now    func() time.Time
	Log    *slog.Logger

	decoyOnce sync.Once
	decoy     string
}

func New(st *store.Store, sealer *Sealer, now func() time.Time, log *slog.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Store: st, Sealer: sealer, Now: now, Log: log}
}

func (s *Service) Session(r *http.Request) (*store.Session, error) {
	sess, err := s.cookieSession(r)
	if err != nil {
		return nil, err
	}
	if !sess.MFAOK {
		return nil, ErrUnauthorized
	}
	return sess, nil
}

func (s *Service) PendingSession(r *http.Request) (*store.Session, error) {
	sess, err := s.cookieSession(r)
	if err != nil {
		return nil, err
	}
	if sess.MFAOK {
		return nil, ErrUnauthorized
	}
	return sess, nil
}

func (s *Service) cookieSession(r *http.Request) (*store.Session, error) {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return nil, ErrUnauthorized
	}
	sess, err := s.Store.Session(c.Value, s.Now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Service) openSession(w http.ResponseWriter, admin string, mfa bool, now, expires time.Time) (string, error) {
	sid, err := randomToken()
	if err != nil {
		return "", err
	}
	csrf, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateSession(store.Session{ID: sid, Admin: admin, MFAOK: mfa, CreatedAt: now, ExpiresAt: expires}); err != nil {
		return "", err
	}
	if err := s.Store.CreateCSRF(csrf, sid, expires); err != nil {
		return "", err
	}
	setCookie(w, SessionCookie, sid, expires, true)
	setCookie(w, CSRFCookie, csrf, expires, false)
	return csrf, nil
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		if err := s.Store.DeleteSession(c.Value); err != nil {
			s.Log.Warn("logout: delete session", "err", err)
		}
	}
	ClearCookies(w)
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

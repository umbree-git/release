package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/totp"
)

const decoyPassword = "unknown-admin-timing-equaliser"

func (s *Service) StartLogin(w http.ResponseWriter, r *http.Request, name, password string) (string, error) {
	now := s.Now()
	key := "pw\x00" + ClientIP(r) + "\x00" + name
	if !s.allow(key, now) {
		return "", ErrRateLimited
	}
	admin, err := s.Store.Admin(name)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(s.decoyHash(), password)
		s.fail(key, now)
		return "", ErrRefused
	}
	if err != nil {
		return "", err
	}
	ok, err := VerifyPassword(admin.PasswordHash, password)
	if err != nil {
		return "", err
	}
	if !ok {
		s.fail(key, now)
		return "", ErrRefused
	}
	s.succeed(key)
	return s.openSession(w, name, false, now, now.Add(PendingTTL))
}

func (s *Service) CompleteTOTP(w http.ResponseWriter, r *http.Request, code string) error {
	now := s.Now()
	sess, err := s.PendingSession(r)
	if err != nil {
		return err
	}
	key := "totp\x00" + ClientIP(r) + "\x00" + sess.Admin
	if !s.allow(key, now) {
		return ErrRateLimited
	}
	ok, err := s.checkCode(sess.Admin, strings.TrimSpace(code), now)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSession(sess.ID); err != nil {
		return err
	}
	if !ok {
		s.fail(key, now)
		ClearCookies(w)
		return ErrRefused
	}
	s.succeed(key)
	_, err = s.openSession(w, sess.Admin, true, now, now.Add(SessionTTL))
	return err
}

func (s *Service) checkCode(name, code string, now time.Time) (bool, error) {
	admin, err := s.Store.Admin(name)
	if err != nil {
		return false, err
	}
	secret, err := s.Sealer.Open(admin.TOTPSecretEnc)
	if err != nil {
		return false, err
	}
	step, ok := totp.Verify(string(secret), code, now, admin.TOTPLastStep)
	if !ok {
		return false, nil
	}
	return s.Store.SetTOTPStep(name, step)
}

func (s *Service) decoyHash() string {
	s.decoyOnce.Do(func() {
		h, err := HashPassword(decoyPassword)
		if err != nil {
			s.Log.Warn("could not build the unknown-admin decoy hash", "err", err)
		}
		s.decoy = h
	})
	return s.decoy
}

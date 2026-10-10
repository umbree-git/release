package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/totp"
)

const decoyPassword = "unknown-admin-timing-equaliser"

func (s *Service) StartLogin(w http.ResponseWriter, r *http.Request, name, password string) (string, error) {
	if ValidAdminName(name) != nil {
		return "", ErrRefused
	}
	now := s.Now()
	held, err := s.reserve(r.Context(), store.FailureKey{Step: "pw", Source: s.source(r), Name: name}, now)
	if err != nil {
		return "", err
	}
	ok, err := s.checkPassword(r.Context(), name, password)
	if err != nil || ok {
		held.release()
	}
	switch {
	case err != nil:
		return "", err
	case !ok:
		return "", ErrRefused
	}
	return s.openSession(w, name, false, now, now.Add(PendingTTL))
}

func (s *Service) checkPassword(ctx context.Context, name, password string) (bool, error) {
	release, err := s.acquireHash(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	admin, err := s.Store.Admin(name)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(s.decoyHash(), password)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return VerifyPassword(admin.PasswordHash, password)
}

func (s *Service) CompleteTOTP(w http.ResponseWriter, r *http.Request, code string) error {
	now := s.Now()
	sess, err := s.PendingSession(r)
	if err != nil {
		return err
	}
	held, err := s.reserve(r.Context(), store.FailureKey{Step: "totp", Source: s.source(r), Name: sess.Admin}, now)
	if err != nil {
		return err
	}
	ok, err := s.checkCode(sess.Admin, strings.TrimSpace(code), now)
	if err != nil || ok {
		held.release()
	}
	if err != nil {
		return err
	}
	if err := s.Store.DeleteSession(sess.ID); err != nil {
		return err
	}
	if !ok {
		ClearCookies(w)
		return ErrRefused
	}
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

func (s *Service) acquireHash(ctx context.Context) (func(), error) {
	timer := time.NewTimer(s.hashWait)
	defer timer.Stop()
	select {
	case s.hashSlots <- struct{}{}:
		return func() { <-s.hashSlots }, nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ErrBusy
	}
}

package auth

import (
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

const (
	loginWindow      = 15 * time.Minute
	loginMaxFailures = 5
	loginNameCeiling = 50
)

func (s *Service) allow(k store.FailureKey, now time.Time) bool {
	since := now.Add(-loginWindow)
	n, err := s.Store.LoginFailures(k, since)
	if err != nil {
		s.Log.Warn("could not read the sign-in failure count; refusing", "err", err)
		return false
	}
	if n >= loginMaxFailures {
		return false
	}
	total, err := s.Store.NameFailures(k.Step, k.Name, since)
	if err != nil {
		s.Log.Warn("could not read the sign-in failure count; refusing", "err", err)
		return false
	}
	if total >= loginNameCeiling {
		s.Log.Warn("sign-in name at its failure ceiling across all sources; `admin unlock` clears it", "name", k.Name, "step", k.Step)
		return false
	}
	return true
}

func (s *Service) fail(k store.FailureKey, now time.Time) {
	if err := s.Store.RecordLoginFailure(k, now); err != nil {
		s.Log.Warn("could not record a failed sign-in; the rate limit is not counting", "err", err)
	}
}

func (s *Service) succeed(k store.FailureKey) {
	if err := s.Store.ClearLoginFailures(k); err != nil {
		s.Log.Warn("could not clear sign-in failures", "err", err)
	}
}

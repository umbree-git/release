package auth

import "time"

const (
	loginWindow      = 15 * time.Minute
	loginMaxFailures = 5
)

func (s *Service) allow(key string, now time.Time) bool {
	n, err := s.Store.LoginFailures(key, now.Add(-loginWindow))
	if err != nil {
		s.Log.Warn("could not read the login failure count; refusing", "err", err)
		return false
	}
	return n < loginMaxFailures
}

func (s *Service) fail(key string, now time.Time) {
	if err := s.Store.RecordLoginFailure(key, now); err != nil {
		s.Log.Warn("could not record a failed sign-in; the rate limit is not counting", "err", err)
	}
}

func (s *Service) succeed(key string) {
	if err := s.Store.ClearLoginFailures(key); err != nil {
		s.Log.Warn("could not clear sign-in failures", "err", err)
	}
}

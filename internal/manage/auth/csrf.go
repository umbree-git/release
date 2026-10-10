package auth

import (
	"crypto/subtle"
	"net/http"

	"github.com/umbree-git/release/internal/manage/store"
)

func (s *Service) CheckCSRF(r *http.Request, sess *store.Session) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return nil
	}
	token := r.Header.Get(CSRFHeader)
	if token == "" {
		token = r.PostFormValue(CSRFField)
	}
	c, err := r.Cookie(CSRFCookie)
	if token == "" || err != nil || subtle.ConstantTimeCompare([]byte(token), []byte(c.Value)) != 1 {
		return ErrCSRF
	}
	ok, err := s.Store.CSRFValid(token, sess.ID, s.Now())
	if err != nil {
		return err
	}
	if !ok {
		return ErrCSRF
	}
	return nil
}

func (s *Service) CSRFToken(w http.ResponseWriter, r *http.Request, sess *store.Session) (string, error) {
	if c, err := r.Cookie(CSRFCookie); err == nil && c.Value != "" {
		if ok, err := s.Store.CSRFValid(c.Value, sess.ID, s.Now()); err == nil && ok {
			return c.Value, nil
		}
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateCSRF(token, sess.ID, sess.ExpiresAt); err != nil {
		return "", err
	}
	setCookie(w, CSRFCookie, token, sess.ExpiresAt, false)
	return token, nil
}

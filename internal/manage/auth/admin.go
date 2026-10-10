package auth

import (
	"fmt"
	"strings"

	"github.com/umbree-git/release/internal/manage/totp"
)

type Enrolment struct {
	Secret     string
	OTPAuthURL string
}

func (s *Service) AddAdmin(name, password string) (*Enrolment, error) {
	if err := ValidAdminName(name); err != nil {
		return nil, err
	}
	if err := ValidPassword(password); err != nil {
		return nil, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	enrol, sealed, err := s.newEnrolment(name)
	if err != nil {
		return nil, err
	}
	if err := s.Store.CreateAdmin(name, hash, sealed, s.Now()); err != nil {
		return nil, err
	}
	return enrol, nil
}

func (s *Service) ResetTOTP(name string) (*Enrolment, error) {
	enrol, sealed, err := s.newEnrolment(name)
	if err != nil {
		return nil, err
	}
	if err := s.Store.ResetTOTP(name, sealed); err != nil {
		return nil, err
	}
	return enrol, nil
}

func (s *Service) newEnrolment(name string) (*Enrolment, []byte, error) {
	secret, err := totp.GenerateSecret()
	if err != nil {
		return nil, nil, err
	}
	sealed, err := s.Sealer.Seal([]byte(secret))
	if err != nil {
		return nil, nil, err
	}
	return &Enrolment{Secret: secret, OTPAuthURL: totp.OTPAuthURL(Issuer, name, secret)}, sealed, nil
}

func ValidAdminName(name string) error {
	if len(name) < 2 || len(name) > 64 {
		return fmt.Errorf("admin name must be 2–64 characters, got %d", len(name))
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == '@':
		default:
			return fmt.Errorf("admin name %q may only contain lowercase letters, digits, '-', '_', '.' and one '@'", name)
		}
	}
	if at := strings.Count(name, "@"); at > 1 || (at == 1 && (name[0] == '@' || name[len(name)-1] == '@')) {
		return fmt.Errorf("admin name %q: '@' may appear once, between a local part and a domain", name)
	}
	return nil
}

func ValidPassword(password string) error {
	if len([]rune(password)) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	return nil
}

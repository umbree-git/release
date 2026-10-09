package store

import "time"

type Admin struct {
	Name          string
	PasswordHash  string
	TOTPSecretEnc []byte
	TOTPLastStep  int64
	CreatedAt     time.Time
}

func (s *Store) Admin(name string) (*Admin, error) { return nil, ErrNotFound }

type Session struct {
	ID        string
	Admin     string
	MFAOK     bool
	CreatedAt time.Time
	ExpiresAt time.Time
}

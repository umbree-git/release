package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Admin struct {
	Name          string
	PasswordHash  string
	TOTPSecretEnc []byte
	TOTPLastStep  int64
	CreatedAt     time.Time
}

const adminColumns = `name, password_hash, totp_secret_enc, totp_last_step, created_at`

func (s *Store) CreateAdmin(name, passwordHash string, sealedTOTP []byte, at time.Time) error {
	if len(sealedTOTP) == 0 {
		return fmt.Errorf("%w: admin %q has no sealed second factor", ErrBadValue, name)
	}
	_, err := s.db.Exec(`INSERT INTO admins (name, password_hash, totp_secret_enc, created_at) VALUES (?, ?, ?, ?)`,
		name, passwordHash, sealedTOTP, at.Unix())
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: admin %q", ErrDuplicate, name)
	}
	if err != nil {
		return fmt.Errorf("store: create admin %q: %w", name, err)
	}
	return nil
}

func scanAdmin(scan func(...any) error) (Admin, error) {
	var a Admin
	var created int64
	err := scan(&a.Name, &a.PasswordHash, &a.TOTPSecretEnc, &a.TOTPLastStep, &created)
	a.CreatedAt = time.Unix(created, 0).UTC()
	return a, err
}

func (s *Store) Admin(name string) (*Admin, error) {
	a, err := scanAdmin(s.db.QueryRow(`SELECT `+adminColumns+` FROM admins WHERE name = ?`, name).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: admin %q", ErrNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read admin %q: %w", name, err)
	}
	return &a, nil
}

func (s *Store) ListAdmins() ([]Admin, error) {
	rows, err := s.db.Query(`SELECT ` + adminColumns + ` FROM admins ORDER BY created_at, name`)
	if err != nil {
		return nil, fmt.Errorf("store: list admins: %w", err)
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		a, err := scanAdmin(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: scan admin: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAdmin(name string) error {
	res, err := s.db.Exec(`DELETE FROM admins WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("store: delete admin %q: %w", name, err)
	}
	return requireOneRow(res, fmt.Errorf("%w: admin %q", ErrNotFound, name))
}

func (s *Store) ResetTOTP(name string, sealedTOTP []byte) error {
	if len(sealedTOTP) == 0 {
		return fmt.Errorf("%w: admin %q has no sealed second factor", ErrBadValue, name)
	}
	return s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE admins SET totp_secret_enc = ?, totp_last_step = 0 WHERE name = ?`, sealedTOTP, name)
		if err != nil {
			return fmt.Errorf("store: reset TOTP for %q: %w", name, err)
		}
		if err := requireOneRow(res, fmt.Errorf("%w: admin %q", ErrNotFound, name)); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE admin = ?`, name); err != nil {
			return fmt.Errorf("store: end sessions of %q: %w", name, err)
		}
		return nil
	})
}

func (s *Store) SetTOTPStep(name string, step int64) (bool, error) {
	res, err := s.db.Exec(`UPDATE admins SET totp_last_step = ? WHERE name = ? AND totp_last_step < ?`, step, name, step)
	if err != nil {
		return false, fmt.Errorf("store: advance TOTP step for %q: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: rows affected: %w", err)
	}
	return n == 1, nil
}

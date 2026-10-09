package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Session struct {
	ID        string
	Admin     string
	MFAOK     bool
	CreatedAt time.Time
	ExpiresAt time.Time
}

func (s *Store) CreateSession(sess Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions (id, admin, mfa_ok, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		sess.ID, sess.Admin, sess.MFAOK, sess.CreatedAt.Unix(), sess.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("store: create session for %q: %w", sess.Admin, err)
	}
	return nil
}

func (s *Store) Session(id string, now time.Time) (*Session, error) {
	var sess Session
	var created, expires int64
	err := s.db.QueryRow(`SELECT id, admin, mfa_ok, created_at, expires_at FROM sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.Admin, &sess.MFAOK, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: session", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: read session: %w", err)
	}
	sess.CreatedAt = time.Unix(created, 0).UTC()
	sess.ExpiresAt = time.Unix(expires, 0).UTC()
	if !now.Before(sess.ExpiresAt) {
		return nil, fmt.Errorf("%w: session expired", ErrNotFound)
	}
	return &sess, nil
}

func (s *Store) SessionCount(admin string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE admin = ?`, admin).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count sessions: %w", err)
	}
	return n, nil
}

func (s *Store) DeleteSession(id string) error {
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete session: %w", err)
	}
	return nil
}

func (s *Store) CreateCSRF(token, sessionID string, expiresAt time.Time) error {
	if _, err := s.db.Exec(`INSERT INTO csrf (token, session_id, expires_at) VALUES (?, ?, ?)`,
		token, sessionID, expiresAt.Unix()); err != nil {
		return fmt.Errorf("store: issue CSRF token: %w", err)
	}
	return nil
}

func (s *Store) CSRFValid(token, sessionID string, now time.Time) (bool, error) {
	var expires int64
	err := s.db.QueryRow(`SELECT expires_at FROM csrf WHERE token = ? AND session_id = ?`, token, sessionID).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: check CSRF token: %w", err)
	}
	return now.Before(time.Unix(expires, 0)), nil
}

func (s *Store) RecordLoginFailure(key string, at time.Time) error {
	if _, err := s.db.Exec(`INSERT INTO login_failures (key, at) VALUES (?, ?)`, key, at.Unix()); err != nil {
		return fmt.Errorf("store: record login failure: %w", err)
	}
	return nil
}

func (s *Store) LoginFailures(key string, since time.Time) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM login_failures WHERE key = ? AND at > ?`, key, since.Unix()).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: count login failures: %w", err)
	}
	return n, nil
}

func (s *Store) ClearLoginFailures(key string) error {
	if _, err := s.db.Exec(`DELETE FROM login_failures WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: clear login failures: %w", err)
	}
	return nil
}

func (s *Store) PurgeExpiredSessions(now time.Time, failuresBefore time.Time) error {
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM sessions WHERE expires_at <= ?`, now.Unix()},
		{`DELETE FROM csrf WHERE expires_at <= ?`, now.Unix()},
		{`DELETE FROM login_failures WHERE at <= ?`, failuresBefore.Unix()},
	} {
		if _, err := s.db.Exec(q.sql, q.arg); err != nil {
			return fmt.Errorf("store: purge expired sessions: %w", err)
		}
	}
	return nil
}

func (s *Store) UnlockAdmin(name, keySuffix, actor, reason string, at time.Time) (int, error) {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" || keySuffix == "" {
		return 0, fmt.Errorf("%w: unlock needs a key suffix, an actor and a reason", ErrBadValue)
	}
	cleared := 0
	err := s.tx(func(tx *sql.Tx) error {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM admins WHERE name = ?`, name).Scan(&exists); err != nil {
			return fmt.Errorf("store: unlock %q: %w", name, err)
		}
		if exists == 0 {
			return fmt.Errorf("%w: admin %q", ErrNotFound, name)
		}
		keys, err := failureKeys(tx)
		if err != nil {
			return err
		}
		for _, key := range keys {
			if !strings.HasSuffix(key, keySuffix) {
				continue
			}
			res, err := tx.Exec(`DELETE FROM login_failures WHERE key = ?`, key)
			if err != nil {
				return fmt.Errorf("store: unlock %q: %w", name, err)
			}
			n, _ := res.RowsAffected()
			cleared += int(n)
		}
		return auditTx(tx, at, actor, "unlock", 0, fmt.Sprintf("admin %s: %s (%d failed sign-ins cleared)", name, reason, cleared))
	})
	return cleared, err
}

func failureKeys(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT DISTINCT key FROM login_failures`)
	if err != nil {
		return nil, fmt.Errorf("store: read login failures: %w", err)
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("store: read login failures: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

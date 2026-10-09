package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

func (s *Store) IssueNonce(nonce string, createdAt, expiresAt time.Time) error {
	if _, err := s.db.Exec(`INSERT INTO nonces (nonce, created_at, expires_at) VALUES (?, ?, ?)`,
		nonce, createdAt.Unix(), expiresAt.Unix()); err != nil {
		return fmt.Errorf("store: issue nonce: %w", err)
	}
	return nil
}

func (s *Store) ConsumeNonce(nonce string, now time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		var expires, used int64
		err := tx.QueryRow(`SELECT expires_at, used_at FROM nonces WHERE nonce = ?`, nonce).Scan(&expires, &used)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: nonce", ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("store: consume nonce: %w", err)
		}
		if used != 0 {
			return fmt.Errorf("%w: nonce was already used at %s", ErrBadState, time.Unix(used, 0).UTC().Format(time.RFC3339))
		}
		if !now.Before(time.Unix(expires, 0)) {
			return fmt.Errorf("%w: nonce expired at %s", ErrBadState, time.Unix(expires, 0).UTC().Format(time.RFC3339))
		}
		res, err := tx.Exec(`UPDATE nonces SET used_at = ? WHERE nonce = ? AND used_at = 0`, now.Unix(), nonce)
		if err != nil {
			return fmt.Errorf("store: consume nonce: %w", err)
		}
		return requireOneRow(res, fmt.Errorf("%w: nonce was used concurrently", ErrBadState))
	})
}

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/catalog"
)

type AuditEntry struct {
	ID     int64
	At     time.Time
	Actor  string
	Action string
	RowID  int64
	Detail string
}

func (s *Store) MarkYanked(id int64, actor, reason string, at time.Time) error {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: mark-yanked needs an actor and a reason", ErrBadValue)
	}
	return s.tx(func(tx *sql.Tx) error {
		if err := transitionTx(tx, id, catalog.StatePublic, catalog.StateYanked, at); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO audit (at, actor, action, row_id, detail) VALUES (?, ?, 'mark-yanked', ?, ?)`,
			at.Unix(), actor, id, reason)
		return err
	})
}

func (s *Store) AuditLog() ([]AuditEntry, error) {
	rows, err := s.db.Query(`SELECT id, at, actor, action, row_id, detail FROM audit ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("store: read audit: %w", err)
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.Actor, &e.Action, &e.RowID, &e.Detail); err != nil {
			return nil, fmt.Errorf("store: read audit: %w", err)
		}
		e.At = time.Unix(at, 0).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Yank(id, successorID int64, at time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		var component, channel, sComponent, sChannel, sState string
		if err := tx.QueryRow(`SELECT component, channel FROM release_versions WHERE id = ?`, id).Scan(&component, &channel); err != nil {
			return rowErr(id, err)
		}
		if err := tx.QueryRow(`SELECT component, channel, state FROM release_versions WHERE id = ?`, successorID).
			Scan(&sComponent, &sChannel, &sState); err != nil {
			return rowErr(successorID, err)
		}
		if successorID == id || sComponent != component || sChannel != channel || sState != catalog.StatePublic {
			return fmt.Errorf("%w: row %d cannot succeed row %d", ErrBadState, successorID, id)
		}
		if err := transitionTx(tx, id, catalog.StatePublic, catalog.StateYanked, at); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE release_versions SET is_current = 1 WHERE id = ?`, successorID)
		return err
	})
}

func rowErr(id int64, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: release row %d", ErrNotFound, id)
	}
	return fmt.Errorf("store: row %d: %w", id, err)
}

func (s *Store) AdoptCurrent(id int64, actor, reason string, at time.Time) (bool, error) {
	return false, errors.New("store: not built")
}

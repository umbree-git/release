package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"umbree-release-r2-mirror/prune"

	"github.com/umbree-git/release/internal/manage/catalog"
)

func (s *Store) Current(component, channel string) (*ReleaseVersion, error) {
	rows, err := s.List(component, channel, catalog.StatePublic)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].IsCurrent {
			return &rows[i], nil
		}
	}
	return nil, fmt.Errorf("%w: no current %s row on %s", ErrNotFound, component, channel)
}

func (s *Store) Promotable(component, channel string) ([]ReleaseVersion, error) {
	staged, err := s.List(component, channel, catalog.StateStaged)
	if err != nil {
		return nil, err
	}
	current, err := s.Current(component, channel)
	if errors.Is(err, ErrNotFound) {
		return staged, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ReleaseVersion
	for _, rv := range staged {
		if prune.VersionLess(current.Stamp, rv.Stamp) {
			out = append(out, rv)
		}
	}
	return out, nil
}

func (s *Store) IsPromotable(rv ReleaseVersion) (bool, error) {
	rows, err := s.Promotable(rv.Component, rv.Channel)
	if err != nil {
		return false, err
	}
	for _, p := range rows {
		if p.ID == rv.ID && p.State == rv.State && p.Stamp == rv.Stamp {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) Promote(id int64, at time.Time) error {
	return s.tx(func(tx *sql.Tx) error {
		var component, channel string
		err := tx.QueryRow(`SELECT component, channel FROM release_versions WHERE id = ?`, id).Scan(&component, &channel)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: release row %d", ErrNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("store: promote %d: %w", id, err)
		}
		if _, err := tx.Exec(`UPDATE release_versions SET is_current = 0
			WHERE component = ? AND channel = ? AND is_current = 1`, component, channel); err != nil {
			return fmt.Errorf("store: promote %d: clear current: %w", id, err)
		}
		if err := transitionTx(tx, id, catalog.StateStaged, catalog.StatePublic, at); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE release_versions SET is_current = 1 WHERE id = ?`, id)
		return err
	})
}

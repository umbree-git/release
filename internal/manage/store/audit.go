package store

import (
	"errors"
	"time"
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
	return errors.New("store: not built")
}

func (s *Store) AuditLog() ([]AuditEntry, error) { return nil, errors.New("store: not built") }

func (s *Store) Yank(id, successorID int64, at time.Time) error {
	return errors.New("store: not built")
}

package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound       = errors.New("store: not found")
	ErrDuplicate      = errors.New("store: already catalogued")
	ErrBadState       = errors.New("store: illegal state")
	ErrBadValue       = errors.New("store: value outside the catalog vocabulary")
	ErrLedgerMismatch = errors.New("store: migrations ledger does not match this binary")
)

const DBFile = "catalog.db"

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("store: data dir is empty")
	}
	path := filepath.Join(dataDir, DBFile)
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func openReadOnly(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("store: data dir is empty")
	}
	path := filepath.Join(dataDir, DBFile)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: no catalog at %s: %w", path, err)
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?mode=ro&immutable=1")
	if err != nil {
		return nil, fmt.Errorf("store: open %q read-only: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open %q read-only: %w", path, err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) tx(fn func(*sql.Tx) error) error {
	t, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	if err := fn(t); err != nil {
		_ = t.Rollback()
		return err
	}
	return t.Commit()
}

func requireOneRow(res sql.Result, otherwise error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rows affected: %w", err)
	}
	if n != 1 {
		return otherwise
	}
	return nil
}

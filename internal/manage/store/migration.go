package store

import (
	"fmt"
)

type Migration struct {
	Version int
	Name    string
}

type migration struct {
	Migration
	stmts []string
}

var migrations = []migration{
	{
		Migration: Migration{Version: 1, Name: "initial catalog"},
		stmts: []string{
			`CREATE TABLE release_versions (
				id               INTEGER PRIMARY KEY,
				component        TEXT    NOT NULL,
				channel          TEXT    NOT NULL,
				version          TEXT    NOT NULL,
				stamp            TEXT    NOT NULL,
				artifacts_json   TEXT    NOT NULL,
				sums_key         TEXT    NOT NULL,
				minisig_key      TEXT    NOT NULL,
				state            TEXT    NOT NULL,
				is_current       INTEGER NOT NULL DEFAULT 0,
				permanent        INTEGER NOT NULL DEFAULT 0,
				created_at       INTEGER NOT NULL,
				promoted_at      INTEGER NOT NULL DEFAULT 0,
				yanked_at        INTEGER NOT NULL DEFAULT 0,
				expired_at       INTEGER NOT NULL DEFAULT 0,
				gated_pruned_at  INTEGER NOT NULL DEFAULT 0,
				public_pruned_at INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE UNIQUE INDEX release_versions_stamp
				ON release_versions (component, channel, stamp)`,
			`CREATE UNIQUE INDEX release_versions_current
				ON release_versions (component, channel) WHERE is_current = 1`,
			`CREATE INDEX release_versions_state
				ON release_versions (component, channel, state)`,
			`CREATE TABLE nonces (
				nonce      TEXT    PRIMARY KEY,
				created_at INTEGER NOT NULL,
				expires_at INTEGER NOT NULL,
				used_at    INTEGER NOT NULL DEFAULT 0
			)`,
			`CREATE INDEX nonces_expiry ON nonces (expires_at)`,
		},
	},
	{
		Migration: Migration{Version: 2, Name: "audit log"},
		stmts: []string{
			`CREATE TABLE audit (
				id      INTEGER PRIMARY KEY,
				at      INTEGER NOT NULL,
				actor   TEXT    NOT NULL,
				action  TEXT    NOT NULL,
				row_id  INTEGER NOT NULL,
				detail  TEXT    NOT NULL
			)`,
		},
	},
	{
		Migration: Migration{Version: 3, Name: "admins and sessions"},
		stmts: []string{
			`CREATE TABLE admins (
				name            TEXT    PRIMARY KEY,
				password_hash   TEXT    NOT NULL,
				totp_secret_enc BLOB    NOT NULL,
				totp_last_step  INTEGER NOT NULL DEFAULT 0,
				created_at      INTEGER NOT NULL
			)`,
			`CREATE TABLE sessions (
				id         TEXT    PRIMARY KEY,
				admin      TEXT    NOT NULL REFERENCES admins(name) ON DELETE CASCADE,
				mfa_ok     INTEGER NOT NULL DEFAULT 0,
				created_at INTEGER NOT NULL,
				expires_at INTEGER NOT NULL
			)`,
			`CREATE INDEX sessions_admin ON sessions (admin)`,
			`CREATE TABLE csrf (
				token      TEXT    PRIMARY KEY,
				session_id TEXT    NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
				expires_at INTEGER NOT NULL
			)`,
			`CREATE INDEX csrf_session ON csrf (session_id)`,
			`CREATE TABLE login_failures (
				id  INTEGER PRIMARY KEY,
				key TEXT    NOT NULL,
				at  INTEGER NOT NULL
			)`,
			`CREATE INDEX login_failures_key ON login_failures (key, at)`,
		},
	},
	{
		Migration: Migration{Version: 4, Name: "login failures by step, source and name"},
		stmts: []string{
			`ALTER TABLE login_failures ADD COLUMN step TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE login_failures ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE login_failures ADD COLUMN name TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX login_failures_source ON login_failures (step, source, name, at)`,
			`CREATE INDEX login_failures_name ON login_failures (step, name, at)`,
		},
	},
}

func Migrations() []Migration {
	out := make([]Migration, 0, len(migrations))
	for _, m := range migrations {
		out = append(out, m.Migration)
	}
	return out
}

type LedgerReport struct {
	Applied []Migration
	Pending []Migration
}

func (r LedgerReport) IsCurrent() bool { return len(r.Pending) == 0 }

func compareLedger(applied []Migration) (LedgerReport, error) {
	known := Migrations()
	for i, a := range applied {
		if i >= len(known) || a.Version > len(known) {
			return LedgerReport{}, fmt.Errorf("%w: the ledger records migration %d (%s) and this binary knows only %d",
				ErrLedgerMismatch, a.Version, a.Name, len(known))
		}
		if a != known[i] {
			return LedgerReport{}, fmt.Errorf("%w: ledger entry %d is %d %q, this binary's is %d %q",
				ErrLedgerMismatch, i+1, a.Version, a.Name, known[i].Version, known[i].Name)
		}
	}
	return LedgerReport{Applied: applied, Pending: known[len(applied):]}, nil
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: create migrations ledger: %w", err)
	}
	applied, err := s.AppliedMigrations()
	if err != nil {
		return err
	}
	report, err := compareLedger(applied)
	if err != nil {
		return err
	}
	for _, m := range migrations[len(report.Applied):] {
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) applyMigration(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin migration %d: %w", m.Version, err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, stmt := range m.stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("store: migration %d (%s) statement %d: %w", m.Version, m.Name, i+1, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO migrations (version, name, applied_at) VALUES (?, ?, unixepoch())`,
		m.Version, m.Name); err != nil {
		return fmt.Errorf("store: record migration %d: %w", m.Version, err)
	}
	return tx.Commit()
}

func (s *Store) AppliedMigrations() ([]Migration, error) {
	rows, err := s.db.Query(`SELECT version, name FROM migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("store: read migrations ledger: %w", err)
	}
	defer rows.Close()
	var out []Migration
	for rows.Next() {
		var m Migration
		if err := rows.Scan(&m.Version, &m.Name); err != nil {
			return nil, fmt.Errorf("store: read migrations ledger: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func CheckLedger(dataDir string) (LedgerReport, error) {
	s, err := openReadOnly(dataDir)
	if err != nil {
		return LedgerReport{}, err
	}
	defer s.Close()
	var hasLedger int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'migrations'`).
		Scan(&hasLedger); err != nil {
		return LedgerReport{}, fmt.Errorf("store: read schema: %w", err)
	}
	var applied []Migration
	if hasLedger == 1 {
		if applied, err = s.AppliedMigrations(); err != nil {
			return LedgerReport{}, err
		}
	}
	return compareLedger(applied)
}

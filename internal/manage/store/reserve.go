package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var (
	ErrSourceBudgetSpent = errors.New("store: the (source, name) sign-in budget is spent")
	ErrNameBudgetSpent   = errors.New("store: the name's sign-in ceiling is spent")
)

type Budget struct {
	Since     time.Time
	PerSource int
	PerName   int
}

func (s *Store) ReserveFailure(ctx context.Context, k FailureKey, b Budget, at time.Time) (int64, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: reserve a sign-in attempt: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return 0, fmt.Errorf("store: reserve a sign-in attempt: %w", err)
	}
	id, err := reserveIn(ctx, conn, k, b, at)
	if err != nil {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
		return 0, err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `ROLLBACK`)
		return 0, fmt.Errorf("store: reserve a sign-in attempt: commit: %w", err)
	}
	return id, nil
}

func reserveIn(ctx context.Context, conn *sql.Conn, k FailureKey, b Budget, at time.Time) (int64, error) {
	var perSource, perName int
	if err := conn.QueryRowContext(ctx, `SELECT
			COUNT(*) FILTER (WHERE source = ?),
			COUNT(*)
		FROM login_failures WHERE step = ? AND name = ? AND at > ?`,
		k.Source, k.Step, k.Name, b.Since.Unix()).Scan(&perSource, &perName); err != nil {
		return 0, fmt.Errorf("store: count sign-in failures: %w", err)
	}
	if perSource >= b.PerSource {
		return 0, ErrSourceBudgetSpent
	}
	if perName >= b.PerName {
		return 0, ErrNameBudgetSpent
	}
	res, err := conn.ExecContext(ctx, `INSERT INTO login_failures (step, source, name, at) VALUES (?, ?, ?, ?)`,
		k.Step, k.Source, k.Name, at.Unix())
	if err != nil {
		return 0, fmt.Errorf("store: reserve a sign-in attempt: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) ReleaseReservation(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM login_failures WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: release a sign-in reservation: %w", err)
	}
	return nil
}

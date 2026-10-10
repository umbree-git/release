package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/catalog"
)

const (
	CopyGated  = "gated"
	CopyPublic = "public"
)

var prunedColumn = map[string]string{
	CopyGated:  "gated_pruned_at",
	CopyPublic: "public_pruned_at",
}

func (s *Store) RecordPruned(id int64, copyName string, keys []string, actor string, at time.Time) (bool, error) {
	column, ok := prunedColumn[copyName]
	if !ok {
		return false, fmt.Errorf("%w: copy %q", ErrBadValue, copyName)
	}
	if err := requireActor("prune", actor); err != nil {
		return false, err
	}
	expired := false
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE release_versions SET `+column+` = ? WHERE id = ? AND `+column+` = 0`, at.Unix(), id); err != nil {
			return fmt.Errorf("store: record %s pruned on %d: %w", copyName, id, err)
		}
		if err := auditTx(tx, at, actor, "prune-"+copyName, id, pruneDetail(keys)); err != nil {
			return err
		}
		var err error
		expired, err = expireIfSpent(tx, id, actor, at)
		return err
	})
	return expired, err
}

func pruneDetail(keys []string) string {
	if len(keys) == 0 {
		return "no keys deleted"
	}
	return fmt.Sprintf("%d keys deleted: %s", len(keys), strings.Join(keys, " "))
}

func expireIfSpent(tx *sql.Tx, id int64, actor string, at time.Time) (bool, error) {
	var state, stamp string
	var current, permanent bool
	var promoted, gated, public int64
	err := tx.QueryRow(`SELECT state, stamp, is_current, permanent, promoted_at, gated_pruned_at, public_pruned_at
		FROM release_versions WHERE id = ?`, id).Scan(&state, &stamp, &current, &permanent, &promoted, &gated, &public)
	if err != nil {
		return false, rowErr(id, err)
	}
	spent := gated != 0 && (promoted == 0 || public != 0)
	if !spent || current || permanent || state == catalog.StateExpired {
		return false, nil
	}
	if err := transitionTx(tx, id, state, catalog.StateExpired, at); err != nil {
		return false, err
	}
	return true, auditTx(tx, at, actor, "expire", id, stamp+" "+state+" -> expired; no window keeps any of its bytes")
}

func (s *Store) SetPermanent(component, channel, stamp string, pinned bool, actor string, at time.Time) (*ReleaseVersion, error) {
	if err := requireActor("pin", actor); err != nil {
		return nil, err
	}
	rv, err := s.ByStamp(component, channel, stamp)
	if err != nil {
		return nil, err
	}
	if pinned && !rv.PublicPruningAt.IsZero() && rv.PublicPrunedAt.IsZero() {
		return nil, fmt.Errorf("%w: %s %s: its public bytes are being pruned (a pass stopped partway); a pin would strand it half-deleted, so let the next pass finish it",
			ErrBadState, component, stamp)
	}
	if pinned && (rv.PromotedAt.IsZero() || !rv.PublicPrunedAt.IsZero() || rv.State == catalog.StateExpired) {
		return nil, fmt.Errorf("%w: %s %s is %s and holds no public bytes to keep; only a row that was promoted and still has its public bytes is pinned",
			ErrBadState, component, stamp, rv.State)
	}
	action := "unpin"
	if pinned {
		action = "pin"
	}
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE release_versions SET permanent = ? WHERE id = ?`, pinned, rv.ID); err != nil {
			return fmt.Errorf("store: %s %d: %w", action, rv.ID, err)
		}
		return auditTx(tx, at, actor, action, rv.ID, component+" "+stamp)
	})
	if err != nil {
		return nil, err
	}
	rv.Permanent = pinned
	return rv, nil
}

func (s *Store) MarkPublicPruning(id int64, at time.Time) error {
	if _, err := s.db.Exec(`UPDATE release_versions SET public_pruning_at = ? WHERE id = ? AND public_pruning_at = 0`, at.Unix(), id); err != nil {
		return fmt.Errorf("store: mark %d public pruning: %w", id, err)
	}
	return nil
}

func (s *Store) RecordPartialPrune(id int64, copyName string, deleted []string, failure, actor string, at time.Time) error {
	if _, ok := prunedColumn[copyName]; !ok {
		return fmt.Errorf("%w: copy %q", ErrBadValue, copyName)
	}
	if err := requireActor("prune", actor); err != nil {
		return err
	}
	return s.tx(func(tx *sql.Tx) error {
		return auditTx(tx, at, actor, "prune-"+copyName+"-partial", id, pruneDetail(deleted)+"; stopped: "+failure)
	})
}

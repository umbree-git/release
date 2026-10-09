package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"umbree-release-r2-mirror/prune"

	"github.com/umbree-git/release/internal/manage/catalog"
)

type ReleaseVersion struct {
	ID             int64
	Component      string
	Channel        string
	Version        string
	Stamp          string
	ArtifactsJSON  string
	SumsKey        string
	MinisigKey     string
	State          string
	IsCurrent      bool
	Permanent      bool
	CreatedAt      time.Time
	PromotedAt     time.Time
	YankedAt       time.Time
	ExpiredAt      time.Time
	GatedPrunedAt  time.Time
	PublicPrunedAt time.Time
}

type edge struct{ from, to string }

var transitionStampColumn = map[edge]string{
	{catalog.StateStaged, catalog.StatePublic}:  "promoted_at",
	{catalog.StateStaged, catalog.StateExpired}: "expired_at",
	{catalog.StatePublic, catalog.StateYanked}:  "yanked_at",
	{catalog.StatePublic, catalog.StateExpired}: "expired_at",
	{catalog.StateYanked, catalog.StateExpired}: "expired_at",
}

func (s *Store) InsertStaged(rv ReleaseVersion) (int64, error) {
	if err := checkVocabulary(rv.Component, rv.Channel); err != nil {
		return 0, err
	}
	if !catalog.StampMatchesChannel(rv.Stamp, rv.Channel) {
		return 0, fmt.Errorf("%w: stamp %q is not a %s stamp", ErrBadValue, rv.Stamp, rv.Channel)
	}
	res, err := s.db.Exec(`
		INSERT INTO release_versions
			(component, channel, version, stamp, artifacts_json, sums_key, minisig_key, state, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rv.Component, rv.Channel, rv.Version, rv.Stamp, rv.ArtifactsJSON,
		rv.SumsKey, rv.MinisigKey, catalog.StateStaged, rv.CreatedAt.Unix())
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %s %s on %s", ErrDuplicate, rv.Component, rv.Stamp, rv.Channel)
		}
		return 0, fmt.Errorf("store: insert %s %s: %w", rv.Component, rv.Stamp, err)
	}
	return res.LastInsertId()
}

func (s *Store) Transition(id int64, from, to string, at time.Time) error {
	return s.tx(func(tx *sql.Tx) error { return transitionTx(tx, id, from, to, at) })
}

func transitionTx(tx *sql.Tx, id int64, from, to string, at time.Time) error {
	column, ok := transitionStampColumn[edge{from, to}]
	if !ok {
		return fmt.Errorf("%w: %q -> %q is not a catalog transition", ErrBadState, from, to)
	}
	clearCurrent := ""
	if from == catalog.StatePublic {
		clearCurrent = ", is_current = 0"
	}
	var state string
	err := tx.QueryRow(`SELECT state FROM release_versions WHERE id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: release row %d", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("store: transition %d: %w", id, err)
	}
	if state != from {
		return fmt.Errorf("%w: row %d is %s, not %s", ErrBadState, id, state, from)
	}
	res, err := tx.Exec(`UPDATE release_versions SET state = ?, `+column+` = ?`+clearCurrent+`
		WHERE id = ? AND state = ?`, to, at.Unix(), id, from)
	if err != nil {
		return fmt.Errorf("store: transition %d %s -> %s: %w", id, from, to, err)
	}
	return requireOneRow(res, fmt.Errorf("%w: row %d moved concurrently", ErrBadState, id))
}

func (s *Store) Get(id int64) (*ReleaseVersion, error) {
	return s.one(`WHERE id = ?`, id)
}

func (s *Store) ByStamp(component, channel, stamp string) (*ReleaseVersion, error) {
	return s.one(`WHERE component = ? AND channel = ? AND stamp = ?`, component, channel, stamp)
}

func (s *Store) one(where string, args ...any) (*ReleaseVersion, error) {
	rows, err := queryVersions(s.db, `SELECT `+versionColumns+` FROM release_versions `+where, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: release row %v", ErrNotFound, args)
	}
	return &rows[0], nil
}

func (s *Store) List(component, channel string, states ...string) ([]ReleaseVersion, error) {
	if err := checkVocabulary(component, channel); err != nil {
		return nil, err
	}
	query := `SELECT ` + versionColumns + ` FROM release_versions WHERE component = ? AND channel = ?`
	args := []any{component, channel}
	if len(states) > 0 {
		query += ` AND state IN (?` + strings.Repeat(`, ?`, len(states)-1) + `)`
		for _, st := range states {
			args = append(args, st)
		}
	}
	rows, err := queryVersions(s.db, query, args...)
	if err != nil {
		return nil, err
	}
	SortNewestFirst(rows)
	return rows, nil
}

func (s *Store) Newest(component, channel string, states ...string) (*ReleaseVersion, error) {
	rows, err := s.List(component, channel, states...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no %s row on %s in %v", ErrNotFound, component, channel, states)
	}
	return &rows[0], nil
}

func Newer(a, b ReleaseVersion) bool {
	switch {
	case prune.VersionLess(b.Stamp, a.Stamp):
		return true
	case prune.VersionLess(a.Stamp, b.Stamp):
		return false
	case !a.CreatedAt.Equal(b.CreatedAt):
		return a.CreatedAt.After(b.CreatedAt)
	}
	return a.ID > b.ID
}

func SortNewestFirst(rows []ReleaseVersion) {
	slices.SortStableFunc(rows, func(a, b ReleaseVersion) int {
		switch {
		case Newer(a, b):
			return -1
		case Newer(b, a):
			return 1
		}
		return 0
	})
}

func checkVocabulary(component, channel string) error {
	if !catalog.ValidComponent(component) {
		return fmt.Errorf("%w: component %q", ErrBadValue, component)
	}
	if !catalog.ValidChannel(channel) {
		return fmt.Errorf("%w: channel %q", ErrBadValue, channel)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

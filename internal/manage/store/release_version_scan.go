package store

import (
	"database/sql"
	"fmt"
	"time"
)

const versionColumns = `id, component, channel, version, stamp, artifacts_json, sums_key, minisig_key,
	state, is_current, permanent, created_at, promoted_at, yanked_at, expired_at,
	gated_pruned_at, public_pruned_at`

func queryVersions(db *sql.DB, query string, args ...any) ([]ReleaseVersion, error) {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query release rows: %w", err)
	}
	defer rows.Close()
	var out []ReleaseVersion
	for rows.Next() {
		rv, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

func scanVersion(rows *sql.Rows) (ReleaseVersion, error) {
	var rv ReleaseVersion
	var created, promoted, yanked, expired, gatedPruned, publicPruned int64
	if err := rows.Scan(&rv.ID, &rv.Component, &rv.Channel, &rv.Version, &rv.Stamp,
		&rv.ArtifactsJSON, &rv.SumsKey, &rv.MinisigKey, &rv.State, &rv.IsCurrent, &rv.Permanent,
		&created, &promoted, &yanked, &expired, &gatedPruned, &publicPruned); err != nil {
		return ReleaseVersion{}, fmt.Errorf("store: scan release row: %w", err)
	}
	rv.CreatedAt = unixOrZero(created)
	rv.PromotedAt = unixOrZero(promoted)
	rv.YankedAt = unixOrZero(yanked)
	rv.ExpiredAt = unixOrZero(expired)
	rv.GatedPrunedAt = unixOrZero(gatedPruned)
	rv.PublicPrunedAt = unixOrZero(publicPruned)
	return rv, nil
}

func unixOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

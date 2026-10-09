package publish

import (
	"context"
	"errors"
)

type BackfillReport struct {
	Inserted []string
	Existing []string
	Skipped  []string
	Failed   []string
	Current  string
}

func Backfill(ctx context.Context, d Deps, component string) (BackfillReport, error) {
	return BackfillReport{}, errors.New("publish: backfill not built")
}

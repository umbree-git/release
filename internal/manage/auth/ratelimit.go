package auth

import (
	"context"
	"errors"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

const (
	loginWindow      = 15 * time.Minute
	loginMaxFailures = 5
	loginNameCeiling = 50
)

type reservation struct {
	s  *Service
	id int64
}

func (s *Service) reserve(ctx context.Context, k store.FailureKey, now time.Time) (*reservation, error) {
	id, err := s.Store.ReserveFailure(context.WithoutCancel(ctx), k,
		store.Budget{Since: now.Add(-loginWindow), PerSource: loginMaxFailures, PerName: loginNameCeiling}, now)
	switch {
	case errors.Is(err, store.ErrNameBudgetSpent):
		s.Log.Warn("sign-in name at its failure ceiling across all sources; `admin unlock` clears it", "name", k.Name, "step", k.Step)
		return nil, ErrRateLimited
	case errors.Is(err, store.ErrSourceBudgetSpent):
		return nil, ErrRateLimited
	case err != nil:
		return nil, err
	}
	return &reservation{s: s, id: id}, nil
}

func (r *reservation) release() {
	if err := r.s.Store.ReleaseReservation(r.id); err != nil {
		r.s.Log.Warn("could not release a sign-in reservation; it counts as a failure until it ages out", "err", err)
	}
}

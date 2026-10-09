package publish

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/sums"
)

var (
	ErrNotStaged     = errors.New("publish: not staged")
	ErrNotPromotable = errors.New("publish: not promotable")
	ErrNeedsBackfill = errors.New("publish: run backfill first")
	ErrNoSuccessor   = errors.New("publish: no public row with bytes to re-point to")
	ErrNotCurrent    = errors.New("publish: not the current public row")
)

type Deps struct {
	Store        *store.Store
	Gated        backend.Gated
	Public       backend.Public
	Key          sums.PublicKey
	Locks        *Locks
	Now          func() time.Time
	Log          *slog.Logger
	AfterPromote func(ctx context.Context, component, channel string) error
}

type Run struct {
	d       Deps
	row     store.ReleaseVersion
	release func()
}

func Begin(ctx context.Context, d Deps, rowID int64) (*Run, error) {
	rv, err := d.Store.Get(rowID)
	if err != nil {
		return nil, err
	}
	release, err := d.Locks.Acquire(ctx, rv.Component, rv.Channel)
	if err != nil {
		return nil, err
	}
	rv, err = d.Store.Get(rowID)
	if err != nil {
		release()
		return nil, err
	}
	return &Run{d: d, row: *rv, release: release}, nil
}

func (r *Run) Close() { r.release() }

func Promote(ctx context.Context, d Deps, rowID int64, w io.Writer) error {
	st := newStream(w)
	r, err := Begin(ctx, d, rowID)
	if err != nil {
		st.finish(err, rowID, "")
		return err
	}
	defer r.Close()
	return r.promote(ctx, st)
}

func (r *Run) Promote(ctx context.Context, w io.Writer) error { return r.promote(ctx, newStream(w)) }

func (r *Run) now() time.Time {
	if r.d.Now == nil {
		return time.Now()
	}
	return r.d.Now()
}

func (r *Run) log() *slog.Logger {
	if r.d.Log == nil {
		return slog.Default()
	}
	return r.d.Log
}

package publish

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
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
	ErrNoActor       = errors.New("publish: no actor; every promote and yank names who did it")
)

type Deps struct {
	Store        *store.Store
	Gated        backend.Gated
	Public       backend.Public
	Key          sums.PublicKey
	Locks        *Locks
	Now          func() time.Time
	Log          *slog.Logger
	AfterPromote func(ctx context.Context, component, channel string) (string, error)
	Confirm      *Confirmer
}

type Confirmer struct {
	BaseURL string
	Fetcher backend.Fetcher
}

type Run struct {
	actor   string
	d       Deps
	row     store.ReleaseVersion
	release func()
}

func Begin(ctx context.Context, d Deps, rowID int64, actor string) (*Run, error) {
	if strings.TrimSpace(actor) == "" {
		return nil, ErrNoActor
	}
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
	return &Run{d: d, row: *rv, release: release, actor: actor}, nil
}

func (r *Run) Close() { r.release() }

func Promote(ctx context.Context, d Deps, rowID int64, actor string, w io.Writer) error {
	st := newStream(w)
	r, err := Begin(ctx, d, rowID, actor)
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

func (r *Run) logOutcome(action string, err error, detail string) {
	result := "done"
	if err != nil {
		result = err.Error()
	}
	r.log().Info("release "+action, "actor", r.actor, "row", r.row.ID, "component", r.row.Component,
		"stamp", r.row.Stamp, "result", result, "detail", detail)
}

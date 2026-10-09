package publish

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
)

func Yank(ctx context.Context, d Deps, rowID int64, w io.Writer) error {
	st := newStream(w)
	r, err := Begin(ctx, d, rowID)
	if err != nil {
		st.finish(err, rowID, "")
		return err
	}
	defer r.Close()
	return r.yank(ctx, st)
}

func (r *Run) Yank(ctx context.Context, w io.Writer) error { return r.yank(ctx, newStream(w)) }

func (r *Run) yank(ctx context.Context, st *stream) error {
	successor, err := r.runYank(ctx, st)
	done := ""
	if successor != nil {
		done = fmt.Sprintf("%s %s yanked; %s now names %s", r.row.Component, r.row.Stamp, r.row.Channel, successor.Stamp)
	}
	st.finish(err, r.row.ID, done)
	return err
}

func (r *Run) runYank(ctx context.Context, st *stream) (*store.ReleaseVersion, error) {
	if r.row.State != catalog.StatePublic || !r.row.IsCurrent {
		return nil, fmt.Errorf("%w: row %d is %s; only the current public row is yanked", ErrNotCurrent, r.row.ID, r.row.State)
	}
	st.send(Event{Step: "yank", Status: "start", Row: r.row.ID, Message: r.row.Component + " " + r.row.Stamp})
	successor, err := r.successor(ctx)
	if err != nil {
		return nil, err
	}
	arts, err := artifactsOf(successor.ArtifactsJSON)
	if err != nil {
		return nil, fmt.Errorf("successor row %d: %w", successor.ID, err)
	}
	if err := r.writeManifest(ctx, st, successor.Version, successor.Stamp, arts); err != nil {
		return nil, err
	}
	if err := r.d.Store.Yank(r.row.ID, successor.ID, r.now()); err != nil {
		return nil, fmt.Errorf("flip row %d: %w; the manifest already names %s, so re-run the yank", r.row.ID, err, successor.Stamp)
	}
	st.send(Event{Step: "flip", Status: "ok", Row: r.row.ID})
	r.log().Info("yanked", "row", r.row.ID, "component", r.row.Component, "successor", successor.Stamp)
	return successor, nil
}

func (r *Run) successor(ctx context.Context) (*store.ReleaseVersion, error) {
	rows, err := r.d.Store.List(r.row.Component, r.row.Channel, catalog.StatePublic)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].ID == r.row.ID {
			continue
		}
		present, err := r.publicBytesPresent(ctx, rows[i])
		if err != nil {
			return nil, err
		}
		if present {
			return &rows[i], nil
		}
	}
	return nil, fmt.Errorf("%w: refusing to yank %s; pulling the last release is the by-hand procedure in tools/RUNBOOK.md", ErrNoSuccessor, r.row.Stamp)
}

func (r *Run) publicBytesPresent(ctx context.Context, rv store.ReleaseVersion) (bool, error) {
	arts, err := artifactsOf(rv.ArtifactsJSON)
	if err != nil {
		return false, nil
	}
	for _, a := range arts {
		size, err := r.d.Public.Head(ctx, publicKey(rv.Component, rv.Stamp, a.Key))
		if errors.Is(err, backend.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if size != a.Size {
			return false, nil
		}
	}
	return true, nil
}

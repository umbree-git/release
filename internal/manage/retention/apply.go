package retention

import (
	"context"
	"errors"
	"fmt"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
)

const (
	ActorRegistration = "retention: registration"
	ActorPromote      = "retention: promote"
	ActorNightly      = "retention: nightly"
)

func (r *Retainer) apply(ctx context.Context, p Plan, actor string) (Report, error) {
	rep := Report{Window: p.Window, Component: p.Component, Channel: p.Channel, Skipped: append([]Skip(nil), p.Skipped...)}
	del := r.deleterFor(p.Window)
	if del == nil {
		return rep, fmt.Errorf("retention: no %s store to delete from", p.Window)
	}
	for i, t := range p.Targets {
		if err := ctx.Err(); err != nil {
			rep.Failed = append(rep.Failed, fmt.Sprintf("%d rows not reached: %v", len(p.Targets)-i, err))
			break
		}
		r.applyTarget(ctx, del, p.Window, t, actor, &rep)
	}
	r.log().Info("retention", "actor", actor, "summary", rep.Summary())
	if len(rep.Failed) > 0 {
		return rep, fmt.Errorf("%w: %s", ErrIncomplete, rep.Summary())
	}
	return rep, nil
}

func (r *Retainer) applyTarget(ctx context.Context, del Deleter, w Window, t Target, actor string, rep *Report) {
	rv, err := r.Store.Get(t.RowID)
	if err != nil {
		rep.Failed = append(rep.Failed, fmt.Sprintf("row %d: %v", t.RowID, err))
		return
	}
	if reason := notPrunable(*rv, w); reason != "" {
		for _, key := range t.Keys {
			rep.Skipped = append(rep.Skipped, Skip{RowID: rv.ID, Stamp: rv.Stamp, Key: key, Reason: reason})
		}
		return
	}
	clean := t.Complete
	var deleted []string
	for _, key := range t.Keys {
		if err := checkKey(*rv, w, key); err != nil {
			rep.Skipped = append(rep.Skipped, Skip{RowID: rv.ID, Stamp: rv.Stamp, Key: key, Reason: err.Error()})
			clean = false
			continue
		}
		if err := del.Delete(ctx, key); err != nil {
			rep.Failed = append(rep.Failed, key+": "+err.Error())
			clean = false
			continue
		}
		deleted = append(deleted, key)
	}
	rep.Deleted = append(rep.Deleted, deleted...)
	if clean {
		r.record(*rv, w, deleted, actor, rep)
	}
}

func (r *Retainer) record(rv store.ReleaseVersion, w Window, deleted []string, actor string, rep *Report) {
	expired, err := r.Store.RecordPruned(rv.ID, string(w), deleted, actor, r.now())
	if err != nil {
		rep.Failed = append(rep.Failed, fmt.Sprintf("row %d: %v", rv.ID, err))
		return
	}
	rep.Pruned = append(rep.Pruned, rv.Stamp)
	if expired {
		rep.Expired = append(rep.Expired, rv.Stamp)
	}
}

func notPrunable(rv store.ReleaseVersion, w Window) string {
	if w == Gated {
		if !rv.GatedPrunedAt.IsZero() {
			return "its gated copy is already recorded pruned"
		}
		return ""
	}
	switch {
	case rv.IsCurrent:
		return "the row is current"
	case rv.Permanent:
		return "the row is pinned"
	case rv.PromotedAt.IsZero():
		return "the row was never public"
	case !rv.PublicPrunedAt.IsZero():
		return "its public copy is already recorded pruned"
	}
	return ""
}

func (r *Retainer) Retain(ctx context.Context, component, channel, actor string, windows ...Window) ([]Report, error) {
	release, err := r.Locks.Acquire(ctx, component, channel)
	if err != nil {
		return nil, err
	}
	defer release()
	return r.RetainLocked(ctx, component, channel, actor, windows...)
}

func (r *Retainer) RetainLocked(ctx context.Context, component, channel, actor string, windows ...Window) ([]Report, error) {
	var reports []Report
	var errs []error
	for _, w := range windows {
		p, err := r.Plan(ctx, component, channel, w)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s/%s: %w", w, component, channel, err))
			continue
		}
		rep, err := r.apply(ctx, p, actor)
		reports = append(reports, rep)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return reports, errors.Join(errs...)
}

func (r *Retainer) AfterStage(ctx context.Context, component, channel string) (string, error) {
	reports, err := r.Retain(ctx, component, channel, ActorRegistration, Gated)
	return Summaries(reports), err
}

func (r *Retainer) AfterPromote(ctx context.Context, component, channel string) (string, error) {
	reports, err := r.RetainLocked(ctx, component, channel, ActorPromote, Gated, Public)
	return Summaries(reports), err
}

func (r *Retainer) RetainAll(ctx context.Context, actor string) ([]Report, error) {
	var reports []Report
	var errs []error
	for _, comp := range catalog.Components {
		for _, ch := range catalog.Channels {
			reps, err := r.Retain(ctx, comp, ch, actor, Windows...)
			reports = append(reports, reps...)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}
	return reports, errors.Join(errs...)
}

func (r *Retainer) PlanAll(ctx context.Context) ([]Plan, error) {
	var plans []Plan
	for _, comp := range catalog.Components {
		for _, ch := range catalog.Channels {
			for _, w := range Windows {
				p, err := r.Plan(ctx, comp, ch, w)
				if err != nil {
					return plans, fmt.Errorf("%s %s/%s: %w", w, comp, ch, err)
				}
				plans = append(plans, p)
			}
		}
	}
	return plans, nil
}

func (r *Retainer) Confirm(ctx context.Context, component, channel string, w Window, fingerprint, actor string) (Report, error) {
	release, err := r.Locks.Acquire(ctx, component, channel)
	if err != nil {
		return Report{}, err
	}
	defer release()
	p, err := r.Plan(ctx, component, channel, w)
	if err != nil {
		return Report{}, err
	}
	if p.Fingerprint() != fingerprint {
		return Report{Window: w, Component: component, Channel: channel}, ErrPlanChanged
	}
	return r.apply(ctx, p, actor)
}

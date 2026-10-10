package publish

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/umbree-git/release/internal/manage/catalog"
)

var ErrNoStatic = errors.New("publish: no static publisher is configured")

type StaticPublisher interface {
	Publish(ctx context.Context, component string) (string, error)
}

func RepublishStatic(ctx context.Context, d Deps, component, channel, actor string) (string, error) {
	if strings.TrimSpace(actor) == "" {
		return "", ErrNoActor
	}
	if !catalog.ValidComponent(component) || !catalog.ValidChannel(channel) {
		return "", fmt.Errorf("publish: no static surface for %s/%s", component, channel)
	}
	if d.Static == nil {
		return "", ErrNoStatic
	}
	release, err := d.Locks.Acquire(ctx, component, channel)
	if err != nil {
		return "", err
	}
	defer release()
	r := &Run{d: d, actor: actor}
	summary, err := d.Static.Publish(ctx, component)
	r.log().Info("release republish static", "actor", actor, "component", component, "channel", channel,
		"result", summary, "err", err)
	return summary, err
}

func (r *Run) republishStatic(ctx context.Context, st *stream) {
	if r.d.Static == nil {
		return
	}
	summary, err := r.d.Static.Publish(ctx, r.row.Component)
	if err != nil {
		r.log().Warn("static republish", "component", r.row.Component, "err", err)
		st.send(Event{Step: "static", Status: "error", Message: err.Error() +
			"; the manifest is live, so republish the static surface from the overview or with umbree-release-manage publish-static " + r.row.Component})
		return
	}
	st.send(Event{Step: "static", Status: "ok", Message: summary})
}

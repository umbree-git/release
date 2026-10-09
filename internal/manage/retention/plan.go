package retention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"umbree-release-r2-mirror/manifest"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/store"
)

const maxManifestBytes = 64 << 10

type Target struct {
	RowID    int64
	Stamp    string
	State    string
	Keys     []string
	Expires  bool
	Complete bool
}

type Plan struct {
	Window    Window
	Component string
	Channel   string
	Kept      []string
	Targets   []Target
	Skipped   []Skip
}

func (p Plan) Keys() []string {
	var out []string
	for _, t := range p.Targets {
		out = append(out, t.Keys...)
	}
	sort.Strings(out)
	return out
}

func (p Plan) Empty() bool { return len(p.Targets) == 0 }

func (p Plan) Fingerprint() string {
	lines := []string{fmt.Sprintf("window %s %s/%s", p.Window, p.Component, p.Channel)}
	for _, t := range p.Targets {
		lines = append(lines, fmt.Sprintf("row %d %s %s expires=%t complete=%t", t.RowID, t.Stamp, t.State, t.Expires, t.Complete))
		for _, k := range t.Keys {
			lines = append(lines, fmt.Sprintf("delete %d %s", t.RowID, k))
		}
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Retainer) Plan(ctx context.Context, component, channel string, w Window) (Plan, error) {
	p := Plan{Window: w, Component: component, Channel: channel}
	rows, err := r.Store.List(component, channel)
	if err != nil {
		return p, err
	}
	switch w {
	case Gated:
		p.planGated(rows)
	case Public:
		named, err := r.manifestStamp(ctx, component)
		if err != nil {
			return p, err
		}
		p.planPublic(rows, named)
	default:
		return p, fmt.Errorf("%w %q", ErrUnknownWindow, w)
	}
	return p, nil
}

func (p *Plan) planGated(rows []store.ReleaseVersion) {
	for i, rv := range rows {
		switch {
		case i < KeepGated:
			p.Kept = append(p.Kept, rv.Stamp+" (newest "+fmt.Sprint(KeepGated)+")")
		case rv.GatedPrunedAt.IsZero():
			p.addTarget(rv)
		}
	}
}

func publicWindow(rows []store.ReleaseVersion) map[int64]string {
	kept := map[int64]string{}
	for _, rv := range rows {
		if rv.IsCurrent {
			kept[rv.ID] = "current"
		}
	}
	for _, rv := range rows {
		if len(kept) >= KeepPublicProduction {
			break
		}
		if _, ok := kept[rv.ID]; !ok && !rv.PromotedAt.IsZero() {
			kept[rv.ID] = "newest " + fmt.Sprint(KeepPublicProduction)
		}
	}
	return kept
}

func (p *Plan) planPublic(rows []store.ReleaseVersion, named string) {
	kept := publicWindow(rows)
	for _, rv := range rows {
		if rv.PromotedAt.IsZero() {
			continue
		}
		reason, ok := kept[rv.ID]
		switch {
		case ok:
		case rv.Permanent:
			reason = "pinned"
		case rv.Stamp == named:
			reason = "named by " + manifest.FileName
		case rv.PublicPrunedAt.IsZero():
			p.addTarget(rv)
			continue
		default:
			continue
		}
		p.Kept = append(p.Kept, rv.Stamp+" ("+reason+")")
	}
}

func (p *Plan) addTarget(rv store.ReleaseVersion) {
	t := Target{RowID: rv.ID, Stamp: rv.Stamp, State: rv.State, Expires: expiresAfter(rv, p.Window), Complete: true}
	keys, err := storedKeys(rv, p.Window)
	if err != nil {
		p.Skipped = append(p.Skipped, Skip{RowID: rv.ID, Stamp: rv.Stamp, Reason: err.Error()})
		return
	}
	for _, key := range keys {
		if err := checkKey(rv, p.Window, key); err != nil {
			p.Skipped = append(p.Skipped, Skip{RowID: rv.ID, Stamp: rv.Stamp, Key: key, Reason: err.Error()})
			t.Complete = false
			continue
		}
		t.Keys = append(t.Keys, key)
	}
	if !t.Complete {
		t.Expires = false
	}
	if len(t.Keys) > 0 || t.Complete {
		p.Targets = append(p.Targets, t)
	}
}

func expiresAfter(rv store.ReleaseVersion, w Window) bool {
	if rv.IsCurrent || rv.Permanent {
		return false
	}
	if w == Gated {
		return rv.PromotedAt.IsZero() || !rv.PublicPrunedAt.IsZero()
	}
	return !rv.GatedPrunedAt.IsZero()
}

func (r *Retainer) manifestStamp(ctx context.Context, component string) (string, error) {
	if r.Public == nil {
		return "", errors.New("retention: no public store to read the manifest from")
	}
	key := manifest.Key(component + "/")
	body, err := r.Public.Get(ctx, key, maxManifestBytes)
	if errors.Is(err, backend.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(body, &m); err != nil || m.Stamp == "" {
		return "", fmt.Errorf("%s does not parse; the public pass deletes nothing until it does", key)
	}
	return m.Stamp, nil
}

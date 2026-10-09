package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"umbree-release-r2-mirror/manifest"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

const maxManifestBytes = 64 << 10

func (r *Run) promote(ctx context.Context, st *stream) error {
	err := r.runPromote(ctx, st)
	st.finish(err, r.row.ID, fmt.Sprintf("%s %s is live on %s", r.row.Component, r.row.Stamp, r.row.Channel))
	return err
}

func (r *Run) runPromote(ctx context.Context, st *stream) error {
	if err := r.checkPromotable(ctx); err != nil {
		return err
	}
	arts, err := artifactsOf(r.row.ArtifactsJSON)
	if err != nil {
		return fmt.Errorf("row %d: %w", r.row.ID, err)
	}
	st.send(Event{Step: "promote", Status: "start", Row: r.row.ID, Message: r.row.Component + " " + r.row.Stamp})
	if err := r.verifyAll(ctx, st, arts); err != nil {
		return err
	}
	if err := r.copyAll(ctx, st, arts); err != nil {
		return err
	}
	if err := r.writeManifest(ctx, st, r.row.Version, r.row.Stamp, arts); err != nil {
		return err
	}
	if err := r.d.Store.Promote(r.row.ID, r.now()); err != nil {
		return fmt.Errorf("flip row %d: %w; the manifest already names %s, so re-run the promote to complete the flip", r.row.ID, err, r.row.Stamp)
	}
	st.send(Event{Step: "flip", Status: "ok", Row: r.row.ID})
	r.log().Info("promoted", "row", r.row.ID, "component", r.row.Component, "stamp", r.row.Stamp)
	r.afterPromote(ctx, st)
	r.confirm(ctx, st)
	return nil
}

func (r *Run) checkPromotable(ctx context.Context) error {
	if r.row.State != catalog.StateStaged {
		return fmt.Errorf("%w: row %d is %s", ErrNotStaged, r.row.ID, r.row.State)
	}
	if !catalog.ValidChannel(r.row.Channel) {
		return fmt.Errorf("%w: row %d is on channel %q, which the catalog does not publish", ErrNotPromotable, r.row.ID, r.row.Channel)
	}
	ok, err := r.d.Store.IsPromotable(r.row)
	if err != nil {
		return err
	}
	if !ok {
		return r.notPromotable()
	}
	_, cerr := r.d.Store.Current(r.row.Component, r.row.Channel)
	if cerr == nil {
		return nil
	}
	if !errors.Is(cerr, store.ErrNotFound) {
		return cerr
	}
	live, err := r.publicManifestStamp(ctx, r.row.Component)
	if err != nil || live == "" || live == r.row.Stamp {
		return err
	}
	return fmt.Errorf("%w: the public manifest names %s and the catalog has no current %s row", ErrNeedsBackfill, live, r.row.Component)
}

func (r *Run) notPromotable() error {
	mark, err := r.d.Store.HighWaterMark(r.row.Component, r.row.Channel)
	if err != nil {
		return fmt.Errorf("%w: row %d", ErrNotPromotable, r.row.ID)
	}
	return fmt.Errorf("%w: row %d (%s) is not newer than the high-water mark %s (%s); roll back by yank, or cut again and promote the newer stamp",
		ErrNotPromotable, r.row.ID, r.row.Stamp, mark.Stamp, mark.State)
}

func (r *Run) publicManifestStamp(ctx context.Context, component string) (string, error) {
	body, err := r.d.Public.Get(ctx, manifest.Key(component+"/"), maxManifestBytes)
	if errors.Is(err, backend.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read the public manifest: %w", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(body, &m); err != nil || m.Stamp == "" {
		return "", fmt.Errorf("the public manifest %s does not parse", manifest.Key(component+"/"))
	}
	return m.Stamp, nil
}

func artifactsOf(artifactsJSON string) ([]register.Artifact, error) {
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(artifactsJSON), &arts); err != nil {
		return nil, fmt.Errorf("unreadable artifact list: %w", err)
	}
	if len(arts) == 0 {
		return nil, errors.New("the artifact list is empty")
	}
	return arts, nil
}

func PublicKey(component, stamp, key string) string {
	return component + "/" + stamp + "/" + path.Base(key)
}

func zipNames(arts []register.Artifact) []string {
	var out []string
	for _, a := range arts {
		if strings.HasSuffix(a.Key, ".zip") {
			out = append(out, path.Base(a.Key))
		}
	}
	return out
}

func (r *Run) writeManifest(ctx context.Context, st *stream, version, stamp string, arts []register.Artifact) error {
	prefix := r.row.Component + "/"
	body, err := manifest.Build(r.row.Component, prefix, version, stamp, zipNames(arts), r.now()).Encode()
	if err != nil {
		return err
	}
	key := manifest.Key(prefix)
	if err := r.d.Public.Put(ctx, key, body, "application/json"); err != nil {
		return fmt.Errorf("manifest %s: %w", key, err)
	}
	st.send(Event{Step: "manifest", Key: key, Status: "ok", Message: "names " + stamp})
	return nil
}

func (r *Run) afterPromote(ctx context.Context, st *stream) {
	if r.d.AfterPromote == nil {
		return
	}
	if err := r.d.AfterPromote(ctx, r.row.Component, r.row.Channel); err != nil {
		r.log().Warn("after promote", "component", r.row.Component, "err", err)
		st.send(Event{Step: "retention", Status: "error", Message: err.Error()})
	}
}

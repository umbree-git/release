package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"umbree-release-r2-mirror/layout"
	"umbree-release-r2-mirror/manifest"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/sums"
	"github.com/umbree-git/release/internal/register"
)

const maxSumsBytes = 1 << 20

var stampVersion = regexp.MustCompile(`^v([0-9]+\.[0-9]+\.[0-9]+)\.`)

type BackfillReport struct {
	Inserted []string
	Existing []string
	Skipped  []string
	Failed   []string
	Current  string
}

func Backfill(ctx context.Context, d Deps, component, actor string) (BackfillReport, error) {
	var rep BackfillReport
	if !catalog.ValidComponent(component) {
		return rep, fmt.Errorf("unknown component %q", component)
	}
	release, err := d.Locks.Acquire(ctx, component, catalog.ChannelProduction)
	if err != nil {
		return rep, err
	}
	defer release()
	r := &Run{d: d, row: store.ReleaseVersion{Component: component, Channel: catalog.ChannelProduction}}
	live, err := r.publicManifestStamp(ctx, component)
	if err != nil {
		return rep, err
	}
	stamps, err := r.publicStamps(ctx, &rep)
	if err != nil {
		return rep, err
	}
	for _, stamp := range sortedKeys(stamps) {
		r.backfillStamp(ctx, stamp, stamps[stamp], live, &rep)
	}
	if len(rep.Failed) > 0 {
		return rep, fmt.Errorf("backfill %s: %d stamps did not verify and were not inserted", component, len(rep.Failed))
	}
	return rep, nil
}

func (r *Run) publicStamps(ctx context.Context, rep *BackfillReport) (map[string][]string, error) {
	prefix := r.row.Component + "/"
	keys, err := r.d.Public.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	stamps := map[string][]string{}
	skipped := map[string]bool{}
	for _, key := range keys {
		rest := strings.TrimPrefix(key, prefix)
		if rest == manifest.FileName {
			continue
		}
		stamp, file, ok := strings.Cut(rest, "/")
		if !ok || strings.Contains(file, "/") || !layout.StableStampRe.MatchString(stamp) {
			skipped[strings.SplitN(rest, "/", 2)[0]] = true
			continue
		}
		stamps[stamp] = append(stamps[stamp], file)
	}
	for _, name := range sortedKeys(skipped) {
		rep.Skipped = append(rep.Skipped, prefix+name+": not a production stamp; skipped")
	}
	return stamps, nil
}

func (r *Run) backfillStamp(ctx context.Context, stamp string, files []string, live string, rep *BackfillReport) {
	component := r.row.Component
	if _, err := r.d.Store.ByStamp(component, catalog.ChannelProduction, stamp); err == nil {
		rep.Existing = append(rep.Existing, stamp)
		return
	}
	rv, err := r.publicRow(ctx, stamp, files)
	if err != nil {
		rep.Failed = append(rep.Failed, stamp+": "+err.Error())
		return
	}
	isCurrent := stamp == live && r.hasNoCurrent()
	if _, err := r.d.Store.InsertBackfilled(rv, isCurrent, r.now()); err != nil {
		rep.Failed = append(rep.Failed, stamp+": "+err.Error())
		return
	}
	rep.Inserted = append(rep.Inserted, stamp)
	if isCurrent {
		rep.Current = stamp
	}
}

func (r *Run) hasNoCurrent() bool {
	_, err := r.d.Store.Current(r.row.Component, r.row.Channel)
	return errors.Is(err, store.ErrNotFound)
}

func (r *Run) publicRow(ctx context.Context, stamp string, files []string) (store.ReleaseVersion, error) {
	base := r.row.Component + "/" + stamp + "/"
	m := stampVersion.FindStringSubmatch(stamp)
	if m == nil {
		return store.ReleaseVersion{}, errors.New("the stamp carries no version")
	}
	listed, sumsArts, err := r.verifiedSums(ctx, base)
	if err != nil {
		return store.ReleaseVersion{}, err
	}
	arts := sumsArts
	for _, f := range files {
		if !strings.HasSuffix(f, ".zip") {
			continue
		}
		sha, ok := listed[f]
		if !ok {
			return store.ReleaseVersion{}, fmt.Errorf("%s is not in %s", f, register.SumsName)
		}
		size, err := r.d.Public.Head(ctx, base+f)
		if err != nil {
			return store.ReleaseVersion{}, err
		}
		arts = append(arts, register.Artifact{Key: base + f, Size: size, SHA256: sha})
	}
	sort.Slice(arts, func(i, j int) bool { return arts[i].Key < arts[j].Key })
	body, err := json.Marshal(arts)
	if err != nil {
		return store.ReleaseVersion{}, err
	}
	return store.ReleaseVersion{
		Component: r.row.Component, Channel: catalog.ChannelProduction, Version: m[1], Stamp: stamp,
		ArtifactsJSON: string(body), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName,
	}, nil
}

func (r *Run) verifiedSums(ctx context.Context, base string) (map[string]string, []register.Artifact, error) {
	sumsBody, err := r.d.Public.Get(ctx, base+register.SumsName, maxSumsBytes)
	if err != nil {
		return nil, nil, err
	}
	signature, err := r.d.Public.Get(ctx, base+register.MinisigName, maxManifestBytes)
	if err != nil {
		return nil, nil, err
	}
	if err := sums.Verify(r.d.Key, sumsBody, signature); err != nil {
		return nil, nil, err
	}
	listed, err := sums.Parse(sumsBody)
	if err != nil {
		return nil, nil, err
	}
	arts := []register.Artifact{artifactOf(base+register.SumsName, sumsBody), artifactOf(base+register.MinisigName, signature)}
	return listed, arts, nil
}

func artifactOf(key string, body []byte) register.Artifact {
	return register.Artifact{Key: key, Size: int64(len(body)), SHA256: sha256Hex(body)}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

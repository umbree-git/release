package retention

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"umbree-release-r2-mirror/layout"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

type Skip struct {
	RowID  int64
	Stamp  string
	Key    string
	Reason string
}

func storedKeys(rv store.ReleaseVersion, w Window) ([]string, error) {
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil {
		return nil, fmt.Errorf("row %d: unreadable artifact list: %w", rv.ID, err)
	}
	var keys []string
	add := func(k string) {
		if k != "" && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	for _, a := range arts {
		add(a.Key)
	}
	add(rv.SumsKey)
	add(rv.MinisigKey)
	if w == Public {
		for i, k := range keys {
			keys[i] = publish.PublicKey(rv.Component, rv.Stamp, k)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

func stampPrefix(rv store.ReleaseVersion, w Window) (string, error) {
	if !layout.StableStampRe.MatchString(rv.Stamp) {
		return "", fmt.Errorf("stamp %q is not a production stamp", rv.Stamp)
	}
	if w == Public {
		return rv.Component + "/" + rv.Stamp + "/", nil
	}
	prefix, err := layout.GatedPrefix(rv.Component, rv.Channel)
	if err != nil {
		return "", err
	}
	return prefix + rv.Stamp + "/", nil
}

var errOutsidePrefix = errors.New("outside the row's prefix")

func checkKey(rv store.ReleaseVersion, w Window, key string) error {
	keys, err := storedKeys(rv, w)
	if err != nil {
		return err
	}
	if !slices.Contains(keys, key) {
		return errors.New("not one of the row's stored keys")
	}
	prefix, err := stampPrefix(rv, w)
	if err != nil {
		return err
	}
	file, ok := strings.CutPrefix(key, prefix)
	if !ok || file == "" || strings.Contains(file, "/") || file == layout.ManifestName {
		return fmt.Errorf("%w %s", errOutsidePrefix, prefix)
	}
	return nil
}

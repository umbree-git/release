package prune

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"umbree-release-r2-mirror/layout"
)

const (
	DefaultKeepStable = 3
	DefaultKeepBeta   = 1
)

func DefaultKeep(channel string) int {
	switch channel {
	case "stable":
		return DefaultKeepStable
	case "beta":
		return DefaultKeepBeta
	}
	return 0
}

type Store interface {
	List(ctx context.Context, prefix string) ([]string, error)
	Delete(ctx context.Context, key string) error
}

func chOf(segment string) string {
	switch {
	case layout.StableStampRe.MatchString(segment):
		return "stable"
	case layout.BetaStampRe.MatchString(segment):
		return "beta"
	}
	return ""
}

func Prune(ctx context.Context, store Store, comp, channel string, keep int, execute bool, out io.Writer) (int, error) {
	return PruneProtect(ctx, store, comp, channel, keep, execute, out, nil)
}

func PruneProtect(ctx context.Context, store Store, comp, channel string, keep int, execute bool, out io.Writer, protect map[string]struct{}) (int, error) {
	if out == nil {
		out = io.Discard
	}
	if channel != "stable" && channel != "beta" {
		return 0, fmt.Errorf("prune %s: unknown channel %q (want stable | beta)", comp, channel)
	}
	if keep < 1 {
		return 0, fmt.Errorf("prune %s/%s: keep must be >= 1 (got %d)", comp, channel, keep)
	}
	prefix := comp + "/"
	if channel == "beta" {
		prefix = comp + "/beta/"
	}

	keys, err := store.List(ctx, prefix)
	if err != nil {
		return 0, err
	}

	byStamp := map[string][]string{}
	for _, k := range keys {
		rest := strings.TrimPrefix(k, prefix)
		stamp, _, ok := strings.Cut(rest, "/")
		if !ok || chOf(stamp) != channel {
			continue
		}
		byStamp[stamp] = append(byStamp[stamp], k)
	}

	stamps := make([]string, 0, len(byStamp))
	for s := range byStamp {
		stamps = append(stamps, s)
	}
	sort.Sort(byVersionSort(stamps))

	mode := "DRY-RUN"
	if execute {
		mode = "EXECUTE"
	}
	fmt.Fprintf(out, "[%s/%s] %d stamp(s) under %s — keep newest %d (%s)\n", comp, channel, len(stamps), prefix, keep, mode)

	if len(stamps) <= keep {
		fmt.Fprintf(out, "[%s/%s] nothing to prune\n", comp, channel)
		return 0, nil
	}

	drop := stamps[:len(stamps)-keep]
	kept := stamps[len(stamps)-keep:]
	fmt.Fprintf(out, "[%s/%s] keep: %s\n", comp, channel, strings.Join(kept, " "))

	deleted := 0
	for _, stamp := range drop {
		if Protected(protect, comp, stamp) {
			fmt.Fprintf(out, "  keep permanent %s/%s\n", comp, stamp)
			continue
		}
		sort.Strings(byStamp[stamp])
		for _, key := range byStamp[stamp] {
			if execute {
				if err := store.Delete(ctx, key); err != nil {
					return deleted, err
				}
				fmt.Fprintf(out, "  ✓ deleted %s\n", key)
			} else {
				fmt.Fprintf(out, "  - would delete %s\n", key)
			}
			deleted++
		}
	}
	return deleted, nil
}

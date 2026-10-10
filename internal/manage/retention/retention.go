package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

const (
	KeepGated            = 3
	KeepPublicProduction = 5
)

type Window string

const (
	Gated  Window = store.CopyGated
	Public Window = store.CopyPublic
)

var Windows = []Window{Gated, Public}

var (
	ErrPlanChanged   = errors.New("retention: plan changed since the preview; nothing was deleted")
	ErrUnknownWindow = errors.New("retention: unknown window")
	ErrIncomplete    = errors.New("retention: not every planned key was deleted")
)

func ParseWindow(s string) (Window, error) {
	for _, w := range Windows {
		if string(w) == s {
			return w, nil
		}
	}
	return "", fmt.Errorf("%w %q (want gated | public)", ErrUnknownWindow, s)
}

type Deleter interface {
	Delete(ctx context.Context, key string) error
}

type PublicStore interface {
	Deleter
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
}

type Retainer struct {
	Store  *store.Store
	Gated  Deleter
	Public PublicStore
	Locks  *publish.Locks
	Now    func() time.Time
	Log    *slog.Logger
}

type Report struct {
	Window    Window
	Component string
	Channel   string
	Deleted   []string
	Skipped   []Skip
	Failed    []string
	Pruned    []string
	Expired   []string
}

func (r Report) Summary() string {
	s := fmt.Sprintf("%s %s/%s: %d keys deleted, %d rows pruned, %d expired",
		r.Window, r.Component, r.Channel, len(r.Deleted), len(r.Pruned), len(r.Expired))
	if len(r.Skipped) > 0 {
		s += fmt.Sprintf(", %d keys skipped", len(r.Skipped))
	}
	if len(r.Failed) > 0 {
		s += fmt.Sprintf(", %d failed: %s", len(r.Failed), strings.Join(r.Failed, "; "))
	}
	return s
}

func Summaries(reports []Report) string {
	parts := make([]string, 0, len(reports))
	for _, r := range reports {
		parts = append(parts, r.Summary())
	}
	return strings.Join(parts, "; ")
}

func (r *Retainer) now() time.Time {
	if r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func (r *Retainer) log() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}

func (r *Retainer) deleterFor(w Window) Deleter {
	if w == Gated {
		return r.Gated
	}
	return r.Public
}

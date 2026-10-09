package publish

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/sums"
)

type Deps struct {
	Store        *store.Store
	Gated        backend.Gated
	Public       backend.Public
	Key          sums.PublicKey
	Locks        *Locks
	Now          func() time.Time
	Log          *slog.Logger
	AfterPromote func(ctx context.Context, component, channel string) error
}

type Event struct {
	Step    string `json:"step"`
	Key     string `json:"key,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Row     int64  `json:"row,omitempty"`
}

func Promote(ctx context.Context, d Deps, rowID int64, w io.Writer) error {
	return errors.New("publish: not built")
}

package publish

import (
	"context"
	"errors"
	"time"
)

var ErrBusy = errors.New("publish: channel busy")

type Locks struct {
	After  func(time.Duration) <-chan time.Time
	OnWait func(component, channel string)
}

func NewLocks(wait time.Duration) *Locks { return &Locks{} }

func (l *Locks) Acquire(ctx context.Context, component, channel string) (func(), error) {
	return func() {}, nil
}

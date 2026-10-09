package publish

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

const DefaultLockWait = 5 * time.Second

var ErrBusy = errors.New("publish: channel busy")

type Locks struct {
	After  func(time.Duration) <-chan time.Time
	OnWait func(component, channel string)
	wait   time.Duration
	mu     sync.Mutex
	slots  map[string]chan struct{}
}

func NewLocks(wait time.Duration) *Locks {
	return &Locks{After: time.After, wait: wait, slots: map[string]chan struct{}{}}
}

func NewSharedLocks(wait time.Duration, dataDir string) (*Locks, error) {
	if dataDir == "" {
		return nil, errors.New("publish: shared locks need the data dir")
	}
	return NewLocks(wait), nil
}

func (l *Locks) slot(component, channel string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := component + "/" + channel
	ch, ok := l.slots[key]
	if !ok {
		ch = make(chan struct{}, 1)
		l.slots[key] = ch
	}
	return ch
}

func (l *Locks) Acquire(ctx context.Context, component, channel string) (func(), error) {
	ch := l.slot(component, channel)
	release := func() { <-ch }
	select {
	case ch <- struct{}{}:
		return release, nil
	default:
	}
	if l.OnWait != nil {
		l.OnWait(component, channel)
	}
	select {
	case ch <- struct{}{}:
		return release, nil
	case <-l.After(l.wait):
		return nil, fmt.Errorf("%w: %s/%s is held by another promote or yank", ErrBusy, component, channel)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

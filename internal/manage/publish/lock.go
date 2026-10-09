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
	dir    string
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
	l := NewLocks(wait)
	l.dir = dataDir
	return l, nil
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
	release, err := l.acquireSlot(ctx, component, channel)
	if err != nil || l.dir == "" {
		return release, err
	}
	unlock, err := l.acquireFile(ctx, component, channel)
	if err != nil {
		release()
		return nil, err
	}
	return func() { unlock(); release() }, nil
}

func (l *Locks) acquireSlot(ctx context.Context, component, channel string) (func(), error) {
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
		return nil, busy(component, channel)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func busy(component, channel string) error {
	return fmt.Errorf("%w: %s/%s is held by another promote, yank, backfill or retention pass", ErrBusy, component, channel)
}

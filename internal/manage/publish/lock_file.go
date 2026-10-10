package publish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

const filePoll = 20 * time.Millisecond

var lockName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (l *Locks) acquireFile(ctx context.Context, component, channel string) (func(), error) {
	if !lockName.MatchString(component) || !lockName.MatchString(channel) {
		return nil, fmt.Errorf("publish: lock name %q/%q is not a component and channel", component, channel)
	}
	path := filepath.Join(l.dir, "lock."+component+"."+channel)
	if err := l.checkOwner(path); err != nil {
		return nil, err
	}
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	if err := l.checkFileOwner(path); err != nil {
		_ = f.Close()
		return nil, err
	}
	unlock := func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
	if err := l.waitFlock(ctx, f, component, channel); err != nil {
		_ = f.Close()
		return nil, err
	}
	return unlock, nil
}

func openLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("publish: open lock %s: %w", path, err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("publish: lock %s is not a regular file", path)
	}
	return f, nil
}

func (l *Locks) waitFlock(ctx context.Context, f *os.File, component, channel string) error {
	var deadline <-chan time.Time
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("publish: lock %s/%s: %w", component, channel, err)
		}
		if deadline == nil {
			if l.OnWait != nil {
				l.OnWait(component, channel)
			}
			deadline = l.After(l.wait)
		}
		select {
		case <-deadline:
			return busy(component, channel)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(filePoll):
		}
	}
}

func (l *Locks) owner(path string) (int, error) {
	if l.OwnerOf != nil {
		return l.OwnerOf(path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("publish: no owner for %s", path)
	}
	return int(st.Uid), nil
}

func (l *Locks) euid() int {
	if l.Euid != nil {
		return l.Euid()
	}
	return os.Geteuid()
}

func (l *Locks) checkOwner(path string) error {
	dirOwner, err := l.owner(l.dir)
	if err != nil {
		return fmt.Errorf("publish: data dir %s: %w", l.dir, err)
	}
	if euid := l.euid(); euid != dirOwner {
		return fmt.Errorf("publish: running as uid %d, but the data dir %s belongs to uid %d; run this as the service user so its lock files stay usable by serve",
			euid, l.dir, dirOwner)
	}
	if _, err := os.Lstat(path); err == nil {
		return l.checkFileOwner(path)
	}
	return nil
}

func (l *Locks) checkFileOwner(path string) error {
	dirOwner, err := l.owner(l.dir)
	if err != nil {
		return fmt.Errorf("publish: data dir %s: %w", l.dir, err)
	}
	fileOwner, err := l.owner(path)
	if err != nil {
		return fmt.Errorf("publish: lock %s: %w", path, err)
	}
	if fileOwner != dirOwner {
		return fmt.Errorf("publish: lock %s belongs to uid %d, not the data dir's owner uid %d; remove it as the service user's administrator and re-run as the service user",
			path, fileOwner, dirOwner)
	}
	return nil
}

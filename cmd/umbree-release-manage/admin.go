package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

func runMarkYanked(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	if len(o.args) != 1 {
		return usagef(v, "takes exactly one row <id>, got %d arguments", len(o.args))
	}
	id, err := strconv.ParseInt(o.args[0], 10, 64)
	if err != nil || id <= 0 {
		return usagef(v, "row id %q is not a positive integer", o.args[0])
	}
	if strings.TrimSpace(o.dataDir) == "" {
		return usagef(v, "--data-dir is required")
	}
	if strings.TrimSpace(o.reason) == "" {
		return usagef(v, "--reason is required; it is what the audit log records")
	}
	actor := actorOf(e)
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	release, err := lockRow(e, st, o.dataDir, id)
	if err != nil {
		return err
	}
	defer release()
	if err := st.MarkYanked(id, actor, o.reason, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "row %d marked yanked in the catalog by %s; no object was touched\n", id, actor)
	return nil
}

func lockRow(e *env, st *store.Store, dataDir string, id int64) (func(), error) {
	rv, err := st.Get(id)
	if err != nil {
		return nil, err
	}
	return lockChannel(e, dataDir, rv.Component, rv.Channel)
}

func lockChannel(e *env, dataDir, component, channel string) (func(), error) {
	locks, err := publish.NewSharedLocks(publish.DefaultLockWait, dataDir)
	if err != nil {
		return nil, err
	}
	return locks.Acquire(e.ctx, component, channel)
}

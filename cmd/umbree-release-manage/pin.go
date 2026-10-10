package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
)

func runAdminPin(e *env, v *verb, args []string) error { return setPin(e, v, args, true) }

func runAdminUnpin(e *env, v *verb, args []string) error { return setPin(e, v, args, false) }

func setPin(e *env, v *verb, args []string, pinned bool) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	if len(o.args) != 2 {
		return usagef(v, "takes a <component> and a <stamp>, got %d arguments", len(o.args))
	}
	if strings.TrimSpace(o.dataDir) == "" {
		return usagef(v, "--data-dir is required")
	}
	st, err := store.Open(o.dataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	release, err := lockChannel(e, o.dataDir, o.args[0], catalog.ChannelProduction)
	if err != nil {
		return err
	}
	defer release()
	rv, err := st.SetPermanent(o.args[0], catalog.ChannelProduction, o.args[1], pinned, actorOf(e), time.Now())
	if err != nil {
		return err
	}
	what := "unpinned; retention may prune it again"
	if pinned {
		what = "pinned; the public window keeps its bytes in addition to the newest releases"
	}
	fmt.Fprintf(e.stdout, "%s %s (row %d) %s\n", rv.Component, rv.Stamp, rv.ID, what)
	return nil
}

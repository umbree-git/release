package main

import (
	"fmt"
	"strings"

	"github.com/umbree-git/release/internal/manage/store"
)

func runMigrate(e *env, v *verb, args []string) error {
	o, err := parseVerb(e, v, args)
	if err != nil {
		return err
	}
	if !o.check {
		return usagef(v, "migrate takes --check only; migrations apply when serve starts, never from a separate verb")
	}
	if strings.TrimSpace(o.dataDir) == "" {
		return usagef(v, "--data-dir is required")
	}
	report, err := store.CheckLedger(o.dataDir)
	if err != nil {
		return err
	}
	head := store.Migrations()
	fmt.Fprintf(e.stdout, "ledger: %d applied, %d pending, binary head %d\n", len(report.Applied), len(report.Pending), len(head))
	if report.IsCurrent() {
		return nil
	}
	var names []string
	for _, m := range report.Pending {
		names = append(names, fmt.Sprintf("%d %s", m.Version, m.Name))
	}
	return fmt.Errorf("pending migrations: %s (they apply when serve starts)", strings.Join(names, ", "))
}

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/umbree-git/release/internal/register"
)

const statusUsage = "usage: rkit status --manage-url <url> --sign-key <file> --component <comp> --channel production --stamp <stamp>"

type statusOpts struct {
	manageURL, signKey, component, channel, stamp string
}

func parseStatus(args []string) (statusOpts, error) {
	var o statusOpts
	fs := flag.NewFlagSet("rkit status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fields := []struct {
		name string
		dest *string
	}{
		{"manage-url", &o.manageURL}, {"sign-key", &o.signKey},
		{"component", &o.component}, {"channel", &o.channel}, {"stamp", &o.stamp},
	}
	for _, f := range fields {
		fs.StringVar(f.dest, f.name, "", "")
	}
	if err := fs.Parse(args); err != nil {
		return o, &registerUsageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return o, &registerUsageError{msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
	}
	for _, f := range fields {
		if *f.dest == "" {
			return o, &registerUsageError{msg: "--" + f.name + " is required"}
		}
	}
	return o, nil
}

func runStatus(ctx context.Context, args []string, stdout io.Writer, hc *http.Client) error {
	o, err := parseStatus(args)
	if err != nil {
		return err
	}
	client, err := register.NewClient(o.manageURL, hc)
	if err != nil {
		return err
	}
	key, err := register.LoadSigningKey(o.signKey)
	if err != nil {
		return err
	}
	row, err := client.Status(ctx, o.component, o.channel, o.stamp, key)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "row %d %s %s %s\n", row.ID, row.State, row.Version, row.Stamp)
	return nil
}

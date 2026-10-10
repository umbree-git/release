package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"

	"github.com/umbree-git/release/internal/register"
)

const registerUsage = "usage: rkit register --manage-url <url> --sign-key <file> --receipt <file> " +
	"--component <comp> --channel production --version <semver> --stamp <stamp>"

type registerUsageError struct{ msg string }

func (e *registerUsageError) Error() string { return e.msg }

type registerOpts struct {
	manageURL, signKey, receipt, component, channel, version, stamp string
}

func parseRegister(args []string) (registerOpts, error) {
	var o registerOpts
	fs := flag.NewFlagSet("rkit register", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fields := []struct {
		name string
		dest *string
	}{
		{"manage-url", &o.manageURL}, {"sign-key", &o.signKey}, {"receipt", &o.receipt},
		{"component", &o.component}, {"channel", &o.channel}, {"version", &o.version}, {"stamp", &o.stamp},
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

func runRegister(ctx context.Context, args []string, stdout io.Writer, hc *http.Client) error {
	o, err := parseRegister(args)
	if err != nil {
		return err
	}
	client, err := register.NewClient(o.manageURL, hc)
	if err != nil {
		return err
	}
	payload, err := payloadFor(o)
	if err != nil {
		return err
	}
	key, err := register.LoadSigningKey(o.signKey)
	if err != nil {
		return err
	}
	row, err := client.Register(ctx, payload, key)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "✓ registered %s %s on %s: row %d %s\n", row.Version, row.Stamp, o.channel, row.ID, row.State)
	return nil
}

func payloadFor(o registerOpts) (register.Payload, error) {
	r, err := register.ReadReceipt(o.receipt)
	if err != nil {
		return register.Payload{}, err
	}
	want := [][3]string{
		{"component", o.component, r.Component}, {"channel", o.channel, r.Channel},
		{"version", o.version, r.Version}, {"stamp", o.stamp, r.Stamp},
	}
	for _, w := range want {
		if w[1] != w[2] {
			return register.Payload{}, fmt.Errorf("--%s %q disagrees with the receipt's %q; the receipt is what was uploaded", w[0], w[1], w[2])
		}
	}
	return register.PayloadFromReceipt(r)
}

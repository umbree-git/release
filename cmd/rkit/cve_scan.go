package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/umbree-git/release/internal/relconfig"
)

const (
	cveScanUsage   = "usage: rkit cve-scan --component <umbree|umbreed> --src <dir> --report <dir> [--repo <dir>] [--channel stable|beta]"
	scanProduct    = "umbree"
	scanFindings   = 1
	scanCannotRun  = 2
	verdictPrefix  = "cve: "
	cannotRunAhead = "cve: cannot-run · "
)

type cveScanOpts struct {
	build  buildOpts
	report string
}

func parseCVEScan(args []string) (cveScanOpts, error) {
	var o cveScanOpts
	fs := flag.NewFlagSet("rkit cve-scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.build.Component, "component", "", "")
	fs.StringVar(&o.build.SrcDir, "src", "", "")
	fs.StringVar(&o.build.RepoDir, "repo", ".", "")
	fs.StringVar(&o.build.Channel, "channel", relconfig.ChannelStable, "")
	fs.StringVar(&o.report, "report", "", "")
	if err := fs.Parse(args); err != nil {
		return o, &registerUsageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return o, &registerUsageError{msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0))}
	}
	if !slices.Contains(relconfig.Components, o.build.Component) {
		return o, &registerUsageError{msg: fmt.Sprintf("--component must be one of %s (got %q)", strings.Join(relconfig.Components, ", "), o.build.Component)}
	}
	if o.build.SrcDir == "" {
		return o, &registerUsageError{msg: "--src is required"}
	}
	if o.report == "" {
		return o, &registerUsageError{msg: "--report is required"}
	}
	if o.build.Channel != relconfig.ChannelStable && o.build.Channel != relconfig.ChannelBeta {
		return o, &registerUsageError{msg: fmt.Sprintf("--channel must be stable or beta (got %q)", o.build.Channel)}
	}
	return o, nil
}

func runCVEScanVerb(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprintln(stdout, cveScanUsage)
			return 0
		}
	}
	o, err := parseCVEScan(args)
	if err != nil {
		return reportVerb("cve-scan", cveScanUsage, err, stderr)
	}
	verdict, code := cveScan(ctx, o, stderr)
	fmt.Fprintf(stdout, "%s/%s: %s\n", scanProduct, o.build.Component, verdict)
	return code
}

func cveScan(ctx context.Context, o cveScanOpts, stderr io.Writer) (string, int) {
	if o.build.Channel == relconfig.ChannelBeta {
		return "skipped · beta channel (stable cuts only)", 0
	}
	if info, err := os.Stat(o.build.SrcDir); err != nil || !info.IsDir() {
		return cannotRunAhead + "missing source " + o.build.SrcDir, scanCannotRun
	}
	var captured bytes.Buffer
	err := runCVEGate(ctx, o.build, filepath.Join(o.report, o.build.Component), io.MultiWriter(stderr, &captured))
	verdict := lastVerdictLine(captured.String())
	code := scanExitCode(err)
	if verdict == "" {
		verdict = cannotRunAhead + errText(err)
		code = scanCannotRun
	}
	return verdict, code
}

func lastVerdictLine(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], verdictPrefix) {
			return lines[i]
		}
	}
	return ""
}

func scanExitCode(err error) int {
	var ge *gateExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ge) && ge.code == scanFindings:
		return scanFindings
	}
	return scanCannotRun
}

func errText(err error) string {
	if err == nil {
		return "the gate printed no verdict line"
	}
	return err.Error()
}

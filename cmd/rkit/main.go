package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/umbree-git/release/internal/relconfig"
)

func usage() string {
	return "usage: rkit <build --component <umbree> [flags] | register [flags] | status [flags] | components>"
}

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, nil))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, hc *http.Client) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, usage())
		return 2
	}
	switch args[0] {
	case "build":
		if err := runBuild(args[1:]); err != nil {
			fmt.Fprintln(stderr, "✗", err)
			return 1
		}
	case "register":
		return reportVerb("register", registerUsage, runRegister(ctx, args[1:], stdout, hc), stderr)
	case "status":
		return reportVerb("status", statusUsage, runStatus(ctx, args[1:], stdout, hc), stderr)
	case "components":
		for _, c := range relconfig.Components {
			fmt.Fprintln(stdout, c)
		}
	default:
		fmt.Fprintln(stderr, usage())
		return 2
	}
	return 0
}

func reportVerb(name, usage string, err error, stderr io.Writer) int {
	var ue *registerUsageError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ue):
		fmt.Fprintf(stderr, "rkit %s: %s\n%s\n", name, ue.msg, usage)
		return 2
	}
	fmt.Fprintf(stderr, "✗ rkit %s: %v\n", name, err)
	return 1
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const (
	toolName  = "umbree-release-manage"
	exitUsage = 2
)

type env struct {
	ctx    context.Context
	stdout io.Writer
	stderr io.Writer
	getenv func(string) string
}

type usageError struct {
	verb *verb
	msg  string
}

func (e *usageError) Error() string { return e.msg }

func usagef(v *verb, format string, a ...any) error {
	return &usageError{verb: v, msg: fmt.Sprintf(format, a...)}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(&env{ctx: ctx, stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv}, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(e *env, args []string) int {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprint(e.stdout, rootPage())
		return 0
	}
	v := lookupVerb(args[0])
	if v == nil {
		fmt.Fprintf(e.stderr, "%s: unknown command %q\n\n%s", toolName, args[0], rootPage())
		return exitUsage
	}
	err := v.run(e, v, args[1:])
	var ue *usageError
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errHelpShown):
		return 0
	case errors.As(err, &ue):
		fmt.Fprintf(e.stderr, "%s %s: %s\n\n%s", toolName, ue.verb.name, ue.msg, verbPage(ue.verb))
		return exitUsage
	}
	fmt.Fprintf(e.stderr, "✗ %s %s: %v\n", toolName, v.name, err)
	return 1
}

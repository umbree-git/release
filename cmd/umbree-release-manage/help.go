package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

var errHelpShown = errors.New("help shown")

type verb struct {
	name     string
	summary  string
	register func(fs *flag.FlagSet, o *options)
	run      func(e *env, v *verb, args []string) error
}

var verbs []*verb

func init() {
	verbs = []*verb{
		{name: "serve", summary: "apply pending migrations, then serve the release intake", register: registerServe, run: runServe},
		{name: "migrate", summary: "with --check, report the migrations ledger against this binary; writes nothing", register: registerMigrate, run: runMigrate},
	}
}

func lookupVerb(name string) *verb {
	for _, v := range verbs {
		if v.name == name {
			return v
		}
	}
	return nil
}

func isHelp(arg string) bool { return arg == "-h" || arg == "--help" }

func rootPage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — the Umbree release catalog service\n\nUsage:\n  %s <command> [flags]\n\nCommands:\n", toolName, toolName)
	for _, v := range verbs {
		fmt.Fprintf(&b, "  %-10s %s\n", v.name, v.summary)
	}
	fmt.Fprintf(&b, "\n%s <command> --help prints that command's flags.\n", toolName)
	return b.String()
}

func verbPage(v *verb) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage:\n  %s %s [flags]\n\n%s\n\nFlags:\n", toolName, v.name, v.summary)
	fs := newFlagSet(v, &options{})
	fs.VisitAll(func(f *flag.Flag) {
		name, usage := flag.UnquoteUsage(f)
		token := "--" + f.Name
		if name != "" {
			token += " <" + name + ">"
		}
		fmt.Fprintf(&b, "  %-28s %s\n", token, usage)
	})
	return b.String()
}

func newFlagSet(v *verb, o *options) *flag.FlagSet {
	fs := flag.NewFlagSet(toolName+" "+v.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	v.register(fs, o)
	return fs
}

func parseVerb(e *env, v *verb, args []string) (*options, error) {
	for _, a := range args {
		if a == "--" {
			break
		}
		if isHelp(a) {
			fmt.Fprint(e.stdout, verbPage(v))
			return nil, errHelpShown
		}
	}
	o := &options{}
	fs := newFlagSet(v, o)
	if err := fs.Parse(args); err != nil {
		return nil, usagef(v, "%s", err)
	}
	if fs.NArg() > 0 {
		return nil, usagef(v, "unexpected argument %q", fs.Arg(0))
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	o.applyEnv(set, e.getenv)
	return o, nil
}

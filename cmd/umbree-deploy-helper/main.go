//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
)

const (
	toolName  = "umbree-deploy-helper"
	exitUsage = 2
)

type verbFunc func(h *host, cfg *config) error

func verbs() map[string]verbFunc {
	return map[string]verbFunc{
		"preflight":      preflight,
		"install-binary": installBinary,
		"render":         render,
		"activate":       activate,
		"restore":        restore,
		"status":         status,
	}
}

func usage(w io.Writer) {
	fmt.Fprintf(w, "usage: %s <preflight|install-binary|render|activate|restore|status>\n\n", toolName)
	fmt.Fprintf(w, "  Run as root through sudo -n. Takes no other argument and reads only\n")
	fmt.Fprintf(w, "  %s.\n", confPath)
}

func main() {
	os.Exit(run(realHost(), os.Args[1:]))
}

func run(h *host, args []string) int {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		usage(h.stdout)
		return 0
	}
	if len(args) != 1 {
		h.fail("expected exactly one verb, got %d arguments", len(args))
		usage(h.stderr)
		return exitUsage
	}
	verb, ok := verbs()[args[0]]
	if !ok {
		h.fail("unknown verb %q", args[0])
		usage(h.stderr)
		return exitUsage
	}
	if h.euid() != 0 {
		h.fail("%s must run as root (sudo -n %s %s)", toolName, h.path(helperRel), args[0])
		return 1
	}
	cfg, err := h.loadTrusted()
	if err == nil {
		err = verb(h, cfg)
	}
	if err != nil {
		h.fail("%s: %v", args[0], err)
		return 1
	}
	return 0
}

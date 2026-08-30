// Command rkit drives umbree release cuts on release-kit: `build` produces the
// signed (and, with --apple, notarized) artifact set into dist/<stamp>/.
package main

import (
	"fmt"
	"os"

	"github.com/umbree-git/release/internal/relconfig"
)

func usage() string {
	return "usage: rkit <build --component <umbree> [flags] | components>"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(2)
	}
	switch os.Args[1] {
	case "build":
		if err := runBuild(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "✗", err)
			os.Exit(1)
		}
	case "components":
		// One component per line, straight from relconfig.Components — the
		// single list rkit builds from. tools/gen-bootstraps.sh parses this
		// output so the bootstrap loop can't drift from what rkit builds.
		for _, c := range relconfig.Components {
			fmt.Println(c)
		}
	default:
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(2)
	}
}

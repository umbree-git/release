// Command rkit drives umbree release cuts on release-kit: `build` produces the
// signed (and, with --apple, notarized) artifact set into dist/<stamp>/.
package main

import (
	"fmt"
	"os"
)

func usage() string {
	return "usage: rkit <build> --component <umbree> [flags]"
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
	default:
		fmt.Fprintln(os.Stderr, usage())
		os.Exit(2)
	}
}

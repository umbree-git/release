package main

import (
	"context"
	"io"
	"os"
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

func main() {
	os.Exit(run(&env{ctx: context.Background(), stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv}, os.Args[1:]))
}

func run(e *env, args []string) int { return 1 }

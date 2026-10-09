package main

import (
	"context"
	"io"
	"net/http"
	"os"
)

func run(ctx context.Context, args []string, stdout, stderr io.Writer, hc *http.Client) int {
	_ = os.Args
	return 1
}

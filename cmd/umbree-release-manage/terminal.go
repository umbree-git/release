package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

func terminalPrompt(f *os.File) func(io.Writer) (string, error) {
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return nil
	}
	return func(out io.Writer) (string, error) {
		fmt.Fprint(out, "password: ")
		first, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read the password: %w", err)
		}
		fmt.Fprint(out, "repeat:   ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read the password: %w", err)
		}
		if string(first) != string(second) {
			return "", errors.New("the two passwords do not match")
		}
		return string(first), nil
	}
}

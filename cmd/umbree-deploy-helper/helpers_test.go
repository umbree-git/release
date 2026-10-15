//go:build linux

package main

import (
	"os"
	"strings"
)

func replaceLine(text, prefix, line string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			lines[i] = line
		}
	}
	return strings.Join(lines, "\n")
}

func otherDeployUser(lookup func(string) (account, error)) func(string) (account, error) {
	return func(name string) (account, error) {
		a, err := lookup(name)
		if name == "deployer" {
			a.uid = os.Getuid() + 1
		}
		return a, err
	}
}

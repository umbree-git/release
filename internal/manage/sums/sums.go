package sums

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var sumsLine = regexp.MustCompile(`^([0-9a-f]{64}) [ *]([^/\s][^/]*)$`)

func Parse(body []byte) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		m := sumsLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("SHA256SUMS.txt line %d is not \"<sha256>  <name>\"", i+1)
		}
		if _, dup := out[m[2]]; dup {
			return nil, fmt.Errorf("SHA256SUMS.txt lists %s twice", m[2])
		}
		out[m[2]] = m[1]
	}
	return out, nil
}

func Agree(listed, want map[string]string) error {
	if len(want) == 0 {
		return errors.New("no zip to check against SHA256SUMS.txt")
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		got, ok := listed[name]
		if !ok {
			return fmt.Errorf("SHA256SUMS.txt does not list %s", name)
		}
		if got != want[name] {
			return fmt.Errorf("SHA256SUMS.txt says %s is %s, the catalog says %s", name, got, want[name])
		}
	}
	return nil
}

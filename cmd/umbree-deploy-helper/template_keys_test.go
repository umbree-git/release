//go:build linux

package main

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

func TestUnitKeysEqualTheCommittedTemplateKeys(t *testing.T) {
	seen := map[string]bool{}
	for _, u := range unitNames {
		data, err := os.ReadFile("../../ops/systemd/" + u)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		for sc.Scan() {
			if k, _, ok := strings.Cut(sc.Text(), "="); ok && !strings.HasPrefix(k, "[") {
				seen[strings.TrimSpace(k)] = true
			}
		}
	}
	for k := range seen {
		if !unitKeys[k] {
			t.Errorf("template key %s is not in unitKeys", k)
		}
	}
	for k := range unitKeys {
		if !seen[k] {
			t.Errorf("unitKeys has %s, which no template uses", k)
		}
	}
}

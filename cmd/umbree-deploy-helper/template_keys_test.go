//go:build linux

package main

import (
	"os"
	"testing"
)

func TestUnitKeysEqualEachCommittedTemplate(t *testing.T) {
	for _, u := range unitNames {
		data, err := os.ReadFile("../../ops/systemd/" + u)
		if err != nil {
			t.Fatal(err)
		}
		seen := templateKeys(string(data))
		for k := range seen {
			if !unitKeys[u][k] {
				t.Errorf("%s: template key %s is not in its unitKeys", u, k)
			}
		}
		for k := range unitKeys[u] {
			if !seen[k] {
				t.Errorf("%s: unitKeys has %s, which its template does not use", u, k)
			}
		}
	}
}

func TestRenderedKeyOutsideItsTemplateRefuses(t *testing.T) {
	allowed := allowedKeys(serviceUnit, "[Service]\nUser=<SERVICE_USER>\nExecStart=/x\n")
	if err := checkUnit(serviceUnit, "[Service]\nUser=umbree-release\nExecStart=/x\nRestart=always\n", allowed, "umbree-release"); err == nil {
		t.Fatal("a rendered key absent from its template passed")
	}
}

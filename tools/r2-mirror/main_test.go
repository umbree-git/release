package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	stableStamp = "v0.1.8.2026.08.31.46b36734"
	betaStamp   = "v0.2.0.beta.2026.09.05.deadbeef"
)

var arts = []string{"SHA256SUMS.txt", "SHA256SUMS.txt.minisig", "umbree-darwin-arm64.zip", "umbree-linux-amd64.zip"}

// TestPlannedKeysBetaLayout: beta keys sit under <comp>/beta/<stamp>/ and the
// beta manifest <comp>/beta/latest.json is the LAST key; stable keys carry no
// beta/ segment.
func TestPlannedKeysBetaLayout(t *testing.T) {
	beta := plannedKeys(config{comp: "umbree", channel: "beta", stamp: betaStamp}, arts)
	want := []string{
		"umbree/beta/" + betaStamp + "/SHA256SUMS.txt",
		"umbree/beta/" + betaStamp + "/SHA256SUMS.txt.minisig",
		"umbree/beta/" + betaStamp + "/umbree-darwin-arm64.zip",
		"umbree/beta/" + betaStamp + "/umbree-linux-amd64.zip",
		"umbree/beta/latest.json",
	}
	if !reflect.DeepEqual(beta, want) {
		t.Fatalf("beta keys = %v, want %v", beta, want)
	}
	stable := plannedKeys(config{comp: "umbree", channel: "stable", stamp: stableStamp}, arts)
	for _, k := range stable {
		if strings.Contains(k, "/beta/") {
			t.Fatalf("stable key %q carries a beta/ segment", k)
		}
	}
	if last := stable[len(stable)-1]; last != "umbree/latest.json" {
		t.Fatalf("stable manifest key = %q, want umbree/latest.json last", last)
	}
	m := buildManifest(config{comp: "umbree", channel: "beta", stamp: betaStamp, version: "0.2.0"}, arts[2:])
	if m.Path != "umbree/beta/"+betaStamp || m.SHA256Sums != "umbree/beta/"+betaStamp+"/SHA256SUMS.txt" {
		t.Fatalf("beta manifest paths = %q / %q", m.Path, m.SHA256Sums)
	}
}

// TestValidateChannelStampShapes: the two shapes never cross, and an unknown
// channel is refused.
func TestValidateChannelStampShapes(t *testing.T) {
	stage := t.TempDir()
	base := config{comp: "umbree", version: "0.2.0", stageDir: stage, dryRun: true}
	cases := []struct {
		channel, stamp string
		ok             bool
	}{
		{"stable", stableStamp, true},
		{"beta", betaStamp, true},
		{"stable", betaStamp, false},
		{"beta", stableStamp, false},
		{"bogus", stableStamp, false},
		{"beta", "v0.2.0-beta.2026.09.05.deadbeef", false},
	}
	for _, c := range cases {
		cfg := base
		cfg.channel, cfg.stamp = c.channel, c.stamp
		err := cfg.validate()
		if (err == nil) != c.ok {
			t.Errorf("channel=%s stamp=%s: err=%v, want ok=%v", c.channel, c.stamp, err, c.ok)
		}
	}
}

// TestCollectArtifactsAndDryRunOrder: over a real stage dir the artifacts
// are collected sorted and the manifest is planned last.
func TestCollectArtifactsAndDryRunOrder(t *testing.T) {
	stage := t.TempDir()
	for _, n := range []string{"umbree-linux-amd64.zip", "SHA256SUMS.txt", "SHA256SUMS.txt.minisig", "umbree-darwin-arm64.zip", "release-notes.md"} {
		if err := os.WriteFile(filepath.Join(stage, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	artifacts, zips, err := collectArtifacts(stage)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(zips, []string{"umbree-darwin-arm64.zip", "umbree-linux-amd64.zip"}) {
		t.Fatalf("zips = %v", zips)
	}
	keys := plannedKeys(config{comp: "umbreed", channel: "beta", stamp: betaStamp}, artifacts)
	if len(keys) != len(artifacts)+1 || keys[len(keys)-1] != "umbreed/beta/latest.json" {
		t.Fatalf("keys = %v", keys)
	}
}

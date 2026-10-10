package manifest_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"umbree-release-r2-mirror/manifest"
)

const stamp = "v0.1.8.2026.09.20.7162a3f3"

var updated = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func TestEncodeMatchesMirrorBytes(t *testing.T) {
	golden, err := os.ReadFile("testdata/latest.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	m := manifest.Build("umbree", "umbree/", "0.1.8", stamp, []string{"umbree-linux-amd64.zip", "umbree-darwin-arm64.zip"}, updated.In(time.FixedZone("x", 3600)))
	got, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, golden) {
		t.Fatalf("manifest bytes\n got %s\nwant %s", got, golden)
	}
	if manifest.Key("umbree/") != "umbree/latest.json" || manifest.Key("umbree/beta/") != "umbree/beta/latest.json" {
		t.Fatalf("Key = %q / %q", manifest.Key("umbree/"), manifest.Key("umbree/beta/"))
	}
	var back manifest.Manifest
	if err := json.Unmarshal(got, &back); err != nil || back.Stamp != stamp || back.Path != "umbree/"+stamp {
		t.Fatalf("decoded %+v, %v", back, err)
	}
}

func TestEncodeFieldOrder(t *testing.T) {
	m := manifest.Build("umbreed", "umbreed/beta/", "0.2.0", "v0.2.0.beta.2026.09.05.deadbeef", []string{"b.zip", "a.zip"}, updated)
	got, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`"component"`, `"minisig"`, `"path"`, `"sha256sums"`, `"stamp"`, `"updated"`, `"version"`, `"zips"`}
	last := -1
	for _, field := range want {
		i := strings.Index(string(got), field)
		if i <= last {
			t.Fatalf("field %s out of order in\n%s", field, got)
		}
		last = i
	}
	if !strings.HasSuffix(string(got), "}\n") || !strings.Contains(string(got), "\n  \"component\"") {
		t.Fatalf("not two-space indented with a trailing newline:\n%q", got)
	}
	if !strings.Contains(string(got), "\"a.zip\",\n    \"b.zip\"") {
		t.Fatalf("zips are not sorted:\n%s", got)
	}
	if !strings.Contains(string(got), `"path": "umbreed/beta/v0.2.0.beta.2026.09.05.deadbeef"`) {
		t.Fatalf("beta path wrong:\n%s", got)
	}
}

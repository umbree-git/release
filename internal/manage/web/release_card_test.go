package web

import "testing"

func TestPlatformOfLegacyZip(t *testing.T) {
	for name, want := range map[string]string{
		"umbreed-darwin-amd64-legacy.zip": "darwin/amd64-legacy",
		"umbree-darwin-amd64-legacy.zip":  "darwin/amd64-legacy",
		"umbree-darwin-arm64-legacy.zip":  unknownPlatform,
		"umbree-linux-amd64-legacy.zip":   unknownPlatform,
		"umbree-darwin-amd64-legacy.tgz":  unknownPlatform,
	} {
		if got := platformOf(name); got != want {
			t.Errorf("platformOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPlatformOfStockZips(t *testing.T) {
	for _, comp := range []string{"umbree", "umbreed"} {
		for _, p := range []string{"darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64"} {
			name, want := comp+"-"+p+".zip", map[string]string{
				"darwin-arm64": "darwin/arm64", "darwin-amd64": "darwin/amd64",
				"linux-arm64": "linux/arm64", "linux-amd64": "linux/amd64",
			}[p]
			if got := platformOf(name); got != want {
				t.Errorf("platformOf(%q) = %q, want %q", name, got, want)
			}
		}
	}
	for _, name := range []string{"notes.zip", "burrowee-darwin-amd64.zip", "umbree-darwin.zip"} {
		if got := platformOf(name); got != unknownPlatform {
			t.Errorf("platformOf(%q) = %q, want %q", name, got, unknownPlatform)
		}
	}
}

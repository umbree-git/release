package layout_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"umbree-release-r2-mirror/layout"
)

const (
	stableStamp = "v0.1.8.2026.08.31.46b36734"
	betaStamp   = "v0.2.0.beta.2026.09.05.deadbeef"
)

func TestGatedPrefixAcceptsStable(t *testing.T) {
	for _, comp := range []string{"umbree", "umbreed"} {
		got, err := layout.GatedPrefix(comp, "production")
		if err != nil {
			t.Fatalf("GatedPrefix(%q, production): %v", comp, err)
		}
		if want := comp + "/production/"; got != want {
			t.Fatalf("GatedPrefix(%q, production) = %q, want %q", comp, got, want)
		}
	}
}

func TestGatedPrefixRefusesBadChannel(t *testing.T) {
	for _, channel := range []string{"", " ", "Production", "stable", "production/", "../production"} {
		got, err := layout.GatedPrefix("umbree", channel)
		if err == nil {
			t.Errorf("GatedPrefix(umbree, %q) = %q, want a refusal", channel, got)
			continue
		}
		if got != "" {
			t.Errorf("GatedPrefix(umbree, %q) returned %q alongside its error", channel, got)
		}
		want := `unknown channel "` + channel + `"`
		if !strings.Contains(err.Error(), want) {
			t.Errorf("GatedPrefix(umbree, %q) error %q does not contain %q", channel, err, want)
		}
	}
}

func TestGatedPrefixRefusesBetaAsHeld(t *testing.T) {
	_, err := layout.GatedPrefix("umbree", "beta")
	if err == nil {
		t.Fatal("GatedPrefix(umbree, beta) accepted a held channel")
	}
	if !strings.Contains(err.Error(), `channel "beta" is held`) {
		t.Fatalf("error %q does not say the channel is held", err)
	}
	if strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error %q calls a held channel unknown", err)
	}
}

func TestGatedPrefixRefusesBadComponent(t *testing.T) {
	for _, comp := range []string{"", "umbree/x", "burrowee", "../umbree"} {
		_, err := layout.GatedPrefix(comp, "production")
		if err == nil {
			t.Errorf("GatedPrefix(%q, production) accepted it", comp)
			continue
		}
		want := `unknown component "` + comp + `"`
		if !strings.Contains(err.Error(), want) {
			t.Errorf("GatedPrefix(%q, production) error %q does not contain %q", comp, err, want)
		}
	}
}

func TestGatedKeyAcceptsArtifact(t *testing.T) {
	got, err := layout.GatedKey("umbreed", "production", stableStamp, "umbreed-linux-arm64.zip")
	if err != nil {
		t.Fatal(err)
	}
	if want := "umbreed/production/" + stableStamp + "/umbreed-linux-arm64.zip"; got != want {
		t.Fatalf("GatedKey = %q, want %q", got, want)
	}
}

func TestGatedKeyRefusesStampOfWrongShape(t *testing.T) {
	for _, stamp := range []string{betaStamp, "v0.1.8", "", stableStamp + "/x"} {
		got, err := layout.GatedKey("umbree", "production", stamp, "SHA256SUMS.txt")
		if err == nil {
			t.Errorf("GatedKey with stamp %q = %q, want a refusal", stamp, got)
			continue
		}
		if !strings.Contains(err.Error(), `stamp "`+stamp+`"`) {
			t.Errorf("GatedKey with stamp %q: error %q does not name it", stamp, err)
		}
	}
}

func TestGatedKeyRefusesManifestName(t *testing.T) {
	for _, file := range []string{"latest.json", "a/b.zip", "", ".", ".."} {
		got, err := layout.GatedKey("umbree", "production", stableStamp, file)
		if err == nil {
			t.Errorf("GatedKey with file %q = %q, want a refusal", file, got)
			continue
		}
		if !strings.Contains(err.Error(), `file "`+file+`"`) {
			t.Errorf("GatedKey with file %q: error %q does not name it", file, err)
		}
	}
}

func TestGatedKeyRefusesWhatThePrefixRefuses(t *testing.T) {
	if _, err := layout.GatedKey("umbree", "beta", betaStamp, "SHA256SUMS.txt"); err == nil {
		t.Fatal("GatedKey accepted the held beta channel")
	}
	if _, err := layout.GatedKey("umbree", "", stableStamp, "SHA256SUMS.txt"); err == nil {
		t.Fatal("GatedKey accepted an empty channel")
	}
}

func TestStampShapesHaveOneHome(t *testing.T) {
	if !layout.StableStampRe.MatchString(stableStamp) || layout.StableStampRe.MatchString(betaStamp) {
		t.Fatalf("StableStampRe: want %q only", stableStamp)
	}
	if !layout.BetaStampRe.MatchString(betaStamp) || layout.BetaStampRe.MatchString(stableStamp) {
		t.Fatalf("BetaStampRe: want %q only", betaStamp)
	}
	for _, rel := range []string{"../main.go", "../prune/prune.go"} {
		src, err := os.ReadFile(filepath.FromSlash(rel))
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		if strings.Contains(text, `[0-9]{4}\.[0-9]{2}\.[0-9]{2}`) {
			t.Errorf("%s still spells a stamp shape of its own", rel)
		}
		for _, ref := range []string{"layout.StableStampRe", "layout.BetaStampRe"} {
			if !strings.Contains(text, ref) {
				t.Errorf("%s does not use %s", rel, ref)
			}
		}
	}
}

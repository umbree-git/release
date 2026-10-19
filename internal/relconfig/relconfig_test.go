package relconfig

import (
	"reflect"
	"testing"

	"github.com/burrowee-git/release-kit/build"
)

func TestBinsUmbree(t *testing.T) {
	got, err := Bins("umbree", "v0.1.0.2026.07.16.deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"umbree": "./cmd/umbree"}
	if len(got) != len(want) {
		t.Fatalf("got %d bins, want %d", len(got), len(want))
	}
	for _, b := range got {
		if want[b.Name] != b.Package {
			t.Errorf("bin %s: package %q, want %q", b.Name, b.Package, want[b.Name])
		}
		if b.Ldflags != "-X main.version=v0.1.0.2026.07.16.deadbeef" {
			t.Errorf("bin %s: ldflags %q", b.Name, b.Ldflags)
		}
	}
}

func TestBinsUnknown(t *testing.T) {
	if _, err := Bins("bogus", "v0"); err == nil {
		t.Fatal("expected error for unknown component")
	}
}

func TestTargets(t *testing.T) {
	want := []build.Target{
		{OS: "darwin", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"}, {OS: "linux", Arch: "amd64"},
	}
	if got := Targets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Targets() = %v, want the four stock targets %v", got, want)
	}
}

func TestLegacyTargetIsSeparate(t *testing.T) {
	if got := LegacyTarget(); got != (build.Target{OS: "darwin", Arch: "amd64"}) {
		t.Fatalf("LegacyTarget() = %v, want darwin/amd64", got)
	}
	if LegacyArch != "amd64-legacy" {
		t.Fatalf("LegacyArch = %q, want amd64-legacy", LegacyArch)
	}
	for _, tgt := range Targets() {
		if tgt.Arch == LegacyArch {
			t.Fatalf("Targets() carries the legacy target %v; it is built apart from the stock four", tgt)
		}
	}
}

func TestBothComponentsResolve(t *testing.T) {
	for _, comp := range Components {
		bins, err := Bins(comp, "v0.0.0")
		if err != nil {
			t.Fatalf("Bins(%q): %v", comp, err)
		}
		if len(bins) == 0 {
			t.Fatalf("Bins(%q) returned no binaries", comp)
		}
	}
	if len(Components) < 2 {
		t.Fatalf("Components = %v, want umbree and umbreed", Components)
	}
}

func TestUnknownComponentStillErrors(t *testing.T) {
	if _, err := Bins("nope", "v0.0.0"); err == nil {
		t.Fatal("unknown component did not error")
	}
}

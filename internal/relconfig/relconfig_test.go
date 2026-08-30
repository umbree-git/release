package relconfig

import "testing"

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
	if len(Targets()) != 4 {
		t.Fatalf("want 4 targets, got %d", len(Targets()))
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

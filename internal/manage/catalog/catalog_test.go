package catalog_test

import (
	"slices"
	"testing"

	"github.com/umbree-git/release/internal/manage/catalog"
)

const productionStamp = "v0.1.8.2026.09.20.7162a3f3"

func TestValidChannelOnlyProduction(t *testing.T) {
	if !catalog.ValidChannel("production") {
		t.Fatal("production refused")
	}
	if catalog.ValidChannel("beta") {
		t.Error("beta accepted as a catalog channel")
	}
	if !catalog.IsHeldChannel("beta") {
		t.Error("beta is not reported as held")
	}
	for _, ch := range []string{"stable", "", "Production", "production ", "production/"} {
		if catalog.ValidChannel(ch) {
			t.Errorf("ValidChannel(%q) = true", ch)
		}
		if catalog.IsHeldChannel(ch) {
			t.Errorf("IsHeldChannel(%q) = true", ch)
		}
	}
}

func TestValidComponent(t *testing.T) {
	for _, c := range []string{"umbree", "umbreed"} {
		if !catalog.ValidComponent(c) {
			t.Errorf("ValidComponent(%q) = false", c)
		}
	}
	for _, c := range []string{"burrowee", "", "umbree/", "Umbree", "../umbree"} {
		if catalog.ValidComponent(c) {
			t.Errorf("ValidComponent(%q) = true", c)
		}
	}
}

func TestStampMatchesChannel(t *testing.T) {
	if !catalog.StampMatchesChannel(productionStamp, "production") {
		t.Fatalf("production stamp %q refused", productionStamp)
	}
	refused := []struct{ stamp, channel string }{
		{"v0.2.0.beta.2026.09.05.deadbeef", "production"},
		{"v0.1.8", "production"},
		{"0.1.8", "production"},
		{productionStamp + "\n", "production"},
		{productionStamp, "beta"},
		{productionStamp, "stable"},
		{"v0.1.8.2026.09.20.7162A3F3", "production"},
	}
	for _, tc := range refused {
		if catalog.StampMatchesChannel(tc.stamp, tc.channel) {
			t.Errorf("StampMatchesChannel(%q, %q) = true", tc.stamp, tc.channel)
		}
	}
}

func TestStatesClosedSet(t *testing.T) {
	want := []string{"staged", "public", "yanked", "expired"}
	if !slices.Equal(catalog.States, want) {
		t.Fatalf("States = %v, want %v", catalog.States, want)
	}
	for _, s := range want {
		if !catalog.ValidState(s) {
			t.Errorf("ValidState(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Staged", "promoted", "deleted", "current"} {
		if catalog.ValidState(s) {
			t.Errorf("ValidState(%q) = true", s)
		}
	}
}

package sums_test

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/sums"
)

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixtureKey(t *testing.T) sums.PublicKey {
	t.Helper()
	k, err := sums.ParsePublicKey(string(read(t, "test-only.pub")))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestMinisignVerifiesReleaseFixture(t *testing.T) {
	k := fixtureKey(t)
	if err := sums.Verify(k, read(t, "SHA256SUMS.txt"), read(t, "SHA256SUMS.txt.minisig")); err != nil {
		t.Fatalf("prehashed signature from minisign 0.12: %v", err)
	}
	if err := sums.Verify(k, read(t, "legacy.txt"), read(t, "legacy.txt.minisig")); err != nil {
		t.Fatalf("legacy signature from minisign -l: %v", err)
	}
}

func TestMinisignRejectsTamperedBody(t *testing.T) {
	k := fixtureKey(t)
	body := read(t, "SHA256SUMS.txt")
	body[0] ^= 1
	if err := sums.Verify(k, body, read(t, "SHA256SUMS.txt.minisig")); err == nil {
		t.Fatal("a tampered prehashed body verified")
	}
	legacy := append(read(t, "legacy.txt"), '\n')
	if err := sums.Verify(k, legacy, read(t, "legacy.txt.minisig")); err == nil {
		t.Fatal("a tampered legacy body verified")
	}
	sig := strings.Replace(string(read(t, "SHA256SUMS.txt.minisig")), "trusted comment: test-only fixture", "trusted comment: test-only fixturE", 1)
	if err := sums.Verify(k, read(t, "SHA256SUMS.txt"), []byte(sig)); err == nil {
		t.Fatal("a tampered trusted comment verified")
	}
}

func TestMinisignRejectsOtherKey(t *testing.T) {
	other := "untrusted comment: other\nRWQZyK0l3lgdSYfj8VXhoTWlVVVcRqfnuVROJzloNrw9NBFm11IeD3HN\n"
	k, err := sums.ParsePublicKey(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := sums.Verify(k, read(t, "SHA256SUMS.txt"), read(t, "SHA256SUMS.txt.minisig")); err == nil {
		t.Fatal("a signature verified under a key that did not make it")
	}
	fixture := fixtureKey(t)
	sameID := sums.PublicKey{ID: fixture.ID, Key: k.Key}
	if err := sums.Verify(sameID, read(t, "SHA256SUMS.txt"), read(t, "SHA256SUMS.txt.minisig")); err == nil {
		t.Fatal("a matching key id with another key verified")
	}
}

func TestMinisignRejectsMalformedSig(t *testing.T) {
	k := fixtureKey(t)
	body := read(t, "SHA256SUMS.txt")
	lines := strings.Split(string(read(t, "SHA256SUMS.txt.minisig")), "\n")
	raw, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil {
		t.Fatal(err)
	}
	wrongAlg := append([]byte("Xx"), raw[2:]...)
	cases := map[string]string{
		"empty":           "",
		"truncated":       lines[0] + "\n" + lines[1][:40] + "\n" + lines[2] + "\n" + lines[3] + "\n",
		"bad base64":      lines[0] + "\n!!!!\n" + lines[2] + "\n" + lines[3] + "\n",
		"wrong algorithm": lines[0] + "\n" + base64.StdEncoding.EncodeToString(wrongAlg) + "\n" + lines[2] + "\n" + lines[3] + "\n",
		"no global sig":   lines[0] + "\n" + lines[1] + "\n" + lines[2] + "\n",
		"no trusted line": lines[0] + "\n" + lines[1] + "\n",
	}
	for name, sig := range cases {
		if err := sums.Verify(k, body, []byte(sig)); err == nil {
			t.Errorf("%s: a malformed signature verified", name)
		}
	}
}

func TestSumsParseStarPrefix(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	got, err := sums.Parse([]byte(a + "  one.zip\n" + b + " *two.zip\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got["one.zip"] != a || got["two.zip"] != b || len(got) != 2 {
		t.Fatalf("Parse = %v", got)
	}
	bad := []string{
		a + "  one.zip\n" + b + "  one.zip\n",
		"xyz  one.zip\n",
		a + "\n",
		strings.ToUpper(a) + "  one.zip\n",
		a + "  dir/one.zip\n",
	}
	for _, body := range bad {
		if _, err := sums.Parse([]byte(body)); err == nil {
			t.Errorf("Parse(%q) accepted it", body)
		}
	}
}

func TestSumsMustCoverEveryZip(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	listed := map[string]string{"one.zip": a, "two.zip": b}
	if err := sums.Agree(listed, map[string]string{"one.zip": a, "two.zip": b}); err != nil {
		t.Fatalf("keep-control: %v", err)
	}
	if err := sums.Agree(listed, map[string]string{"one.zip": a, "three.zip": b}); err == nil || !strings.Contains(err.Error(), "three.zip") {
		t.Fatalf("a zip the sums do not list: %v", err)
	}
	if err := sums.Agree(listed, map[string]string{"one.zip": b}); err == nil || !strings.Contains(err.Error(), "one.zip") {
		t.Fatalf("a zip the sums disagree with: %v", err)
	}
	if err := sums.Agree(listed, map[string]string{}); err == nil {
		t.Fatal("an empty zip set agreed")
	}
}

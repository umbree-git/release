package retention

import (
	"context"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/store"
)

const testStamp = "v0.1.1.2026.10.08.00000001"

func testRow() store.ReleaseVersion {
	base := "umbree/production/" + testStamp + "/"
	return store.ReleaseVersion{
		ID: 7, Component: "umbree", Channel: "production", Stamp: testStamp, State: "staged",
		ArtifactsJSON: `[{"key":"` + base + `umbree-linux-amd64.zip","size":1,"sha256":"00"},` +
			`{"key":"` + base + `SHA256SUMS.txt","size":1,"sha256":"00"},` +
			`{"key":"` + base + `SHA256SUMS.txt.minisig","size":1,"sha256":"00"}]`,
		SumsKey: base + "SHA256SUMS.txt", MinisigKey: base + "SHA256SUMS.txt.minisig",
	}
}

type recordingDeleter struct{ keys []string }

func (d *recordingDeleter) Delete(_ context.Context, key string) error {
	d.keys = append(d.keys, key)
	return nil
}

func TestKeyNotInRowSkipped(t *testing.T) {
	rv := testRow()
	own := "umbree/production/" + testStamp + "/umbree-linux-amd64.zip"
	stranger := "umbree/production/" + testStamp + "/umbree-darwin-arm64.zip"
	for w, key := range map[Window]string{Gated: own, Public: "umbree/" + testStamp + "/umbree-linux-amd64.zip"} {
		if err := checkKey(rv, w, key); err != nil {
			t.Fatalf("keep-control, %s: the row's own key %s refused: %v", w, key, err)
		}
	}
	if err := checkKey(rv, Gated, stranger); err == nil || !strings.Contains(err.Error(), "stored keys") {
		t.Fatalf("a key under the row's prefix but not in its stored keys: %v, want a refusal", err)
	}
	if err := checkKey(rv, Public, "umbree/"+testStamp+"/umbree-darwin-arm64.zip"); err == nil {
		t.Fatal("a public key the row never stored was accepted")
	}

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.InsertStaged(rv)
	if err != nil {
		t.Fatal(err)
	}
	del := &recordingDeleter{}
	r := &Retainer{Store: st, Gated: del}
	var rep Report
	r.applyTarget(context.Background(), del, Gated, Target{RowID: id, Stamp: testStamp, Keys: []string{own, stranger}}, "test-operator", &rep)
	if len(del.keys) != 0 {
		t.Fatalf("deleted %v; a row with any key that fails the check loses none", del.keys)
	}
	if len(rep.Skipped) != 1 || rep.Skipped[0].Key != stranger {
		t.Fatalf("skipped %+v, want the stranger key reported", rep.Skipped)
	}
	got, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.GatedPrunedAt.IsZero() || got.State != "staged" {
		t.Fatalf("a row with a skipped key is %+v, want not recorded pruned", got)
	}
}

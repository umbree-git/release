package retention_test

import (
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
)

func TestPlanOrderingMatchesSortV(t *testing.T) {
	vectors := map[string]struct{ in, sortV []string }{
		"sort -V": {
			in: []string{
				"v0.1.9.2026.06.13.15646772", "v0.1.12.2026.06.14.3449c8b9", "v0.1.44.2026.06.23.b44ee15d",
				"v0.2.25.2026.08.29.2c953a94", "v0.1.4.2026.06.13.15646772",
			},
			sortV: []string{
				"v0.1.4.2026.06.13.15646772", "v0.1.9.2026.06.13.15646772", "v0.1.12.2026.06.14.3449c8b9",
				"v0.1.44.2026.06.23.b44ee15d", "v0.2.25.2026.08.29.2c953a94",
			},
		},
		"sha tie-break": {
			in: []string{
				"v0.2.5.2026.08.20.0abcdef0", "v0.2.5.2026.08.20.abcdef00",
				"v0.2.5.2026.08.20.f048cdba", "v0.2.5.2026.08.20.5048cdba",
			},
			sortV: []string{
				"v0.2.5.2026.08.20.abcdef00", "v0.2.5.2026.08.20.f048cdba",
				"v0.2.5.2026.08.20.0abcdef0", "v0.2.5.2026.08.20.5048cdba",
			},
		},
	}
	for name, vec := range vectors {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			for i, stamp := range vec.in {
				w.insertStamp(stamp, epoch.Add(time.Duration(len(vec.in)-i)*time.Hour))
			}
			p := w.plan(retention.Gated)
			var kept []string
			for _, k := range p.Kept {
				kept = append(kept, strings.Fields(k)[0])
			}
			newestFirst := slices.Clone(vec.sortV)
			slices.Reverse(newestFirst)
			if !slices.Equal(kept, newestFirst[:retention.KeepGated]) {
				t.Fatalf("the window keeps %v, want the newest %d by sort -V: %v", kept, retention.KeepGated, newestFirst)
			}
			var dropped []string
			for _, tg := range p.Targets {
				dropped = append(dropped, tg.Stamp)
			}
			if !slices.Equal(dropped, newestFirst[retention.KeepGated:]) {
				t.Fatalf("the plan drops %v, want %v", dropped, newestFirst[retention.KeepGated:])
			}
		})
	}
}

func (w *world) insertStamp(stamp string, created time.Time) {
	w.t.Helper()
	version := strings.SplitN(strings.TrimPrefix(stamp, "v"), ".", 4)
	base := "umbree/production/" + stamp + "/"
	if _, err := w.st.InsertStaged(store.ReleaseVersion{Component: "umbree", Channel: "production",
		Version: strings.Join(version[:3], "."), Stamp: stamp,
		ArtifactsJSON: `[{"key":"` + base + `umbree-linux-amd64.zip","size":1,"sha256":"00"}]`,
		SumsKey:       base + "SHA256SUMS.txt", MinisigKey: base + "SHA256SUMS.txt.minisig",
		CreatedAt: created}); err != nil {
		w.t.Fatal(err)
	}
}

func TestManifestNeverInPlan(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	w.addArtifact("0.1.1", "umbree/production/"+stampOf("0.1.1")+"/latest.json")
	w.gated.Seed("umbree/production/"+stampOf("0.1.1")+"/latest.json", []byte("{}"))
	manifest, _ := w.public.Body("umbree/latest.json")
	for _, win := range retention.Windows {
		p := w.plan(win)
		for _, k := range p.Keys() {
			if path.Base(k) == "latest.json" {
				t.Fatalf("%s plan holds %s", win, k)
			}
		}
		if !slices.ContainsFunc(p.Skipped, func(s retention.Skip) bool { return path.Base(s.Key) == "latest.json" }) {
			t.Fatalf("%s plan does not report the manifest-named key it skipped: %+v", win, p.Skipped)
		}
	}
	w.retain(retention.Gated, retention.Public)
	for _, d := range w.deletes() {
		if path.Base(d) == "latest.json" {
			t.Fatalf("retention deleted %s", d)
		}
	}
	if after, ok := w.public.Body("umbree/latest.json"); !ok || string(after) != string(manifest) {
		t.Fatal("the public manifest changed or vanished")
	}
	if rv := w.row("0.1.1"); rv.State == "expired" {
		t.Fatal("a row with a skipped key was expired; its bytes are not all accounted for")
	}
	w.wantPublic(t, nil, []string{"0.1.2"})
}

func TestKeyOutsidePrefixSkipped(t *testing.T) {
	w := newWorld(t)
	for _, v := range []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
		w.stage(v)
	}
	foreign := []string{
		"umbree/production/" + stampOf("0.1.4") + "/umbree-linux-amd64.zip",
		"umbreed/production/" + stampOf("0.1.1") + "/umbreed-linux-amd64.zip",
		"umbree/production/" + stampOf("0.1.1") + "/nested/x.zip",
	}
	w.gated.Seed(foreign[1], []byte("other component"))
	w.gated.Seed(foreign[2], []byte("nested"))
	for _, k := range foreign {
		w.addArtifact("0.1.1", k)
	}
	p := w.plan(retention.Gated)
	if got := skippedKeys(p.Skipped); !slices.Equal(got, sortedCopy(foreign)) {
		t.Fatalf("skipped %v, want exactly %v", got, sortedCopy(foreign))
	}
	reps := w.retain(retention.Gated)
	if got := skippedKeys(reps[0].Skipped); !slices.Equal(got, sortedCopy(foreign)) {
		t.Fatalf("the report skipped %v, want %v", got, sortedCopy(foreign))
	}
	for _, k := range foreign {
		if _, ok := w.gated.Body(k); !ok {
			t.Fatalf("%s outside the row's prefix was deleted", k)
		}
	}
	if !w.holds(w.gated, w.gatedKeys("0.1.1")[:4]) {
		t.Fatal("a row with a key outside its prefix lost its own keys; an incomplete row is never targeted")
	}
	for _, tg := range p.Targets {
		if tg.Stamp == stampOf("0.1.1") {
			t.Fatalf("the incomplete row is a target: %+v", tg)
		}
	}
	if rv := w.row("0.1.1"); !rv.GatedPrunedAt.IsZero() || rv.State != "staged" {
		t.Fatalf("a row with skipped keys is %+v, want staged and not recorded pruned", rv)
	}
}

func TestPlanFingerprintStable(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.1", "0.1.2", "0.1.3", "0.1.4", "0.1.5", "0.1.6", "0.1.7")
	for _, win := range retention.Windows {
		a, b := w.plan(win), w.plan(win)
		if a.Empty() {
			t.Fatalf("%s: setup has nothing to prune", win)
		}
		if a.Fingerprint() != b.Fingerprint() || len(a.Fingerprint()) != 64 {
			t.Fatalf("%s: two previews of one state differ: %s / %s", win, a.Fingerprint(), b.Fingerprint())
		}
		slices.Reverse(b.Targets)
		if a.Fingerprint() != b.Fingerprint() {
			t.Fatalf("%s: the fingerprint depends on target order", win)
		}
	}
	if w.plan(retention.Gated).Fingerprint() == w.plan(retention.Public).Fingerprint() {
		t.Fatal("the two windows share a fingerprint")
	}
	if len(w.deletes()) != 0 {
		t.Fatal("planning deleted something")
	}
}

func TestPlanFingerprintChangesWithKeys(t *testing.T) {
	w := newWorld(t)
	for _, v := range []string{"0.1.1", "0.1.2", "0.1.3", "0.1.4"} {
		w.stage(v)
	}
	before := w.plan(retention.Gated)
	w.addArtifact("0.1.1", "umbree/production/"+stampOf("0.1.1")+"/extra.zip")
	withKey := w.plan(retention.Gated)
	if before.Fingerprint() == withKey.Fingerprint() {
		t.Fatal("adding a key to a dropped row left the fingerprint unchanged")
	}
	w.stage("0.1.5")
	withRow := w.plan(retention.Gated)
	if withRow.Fingerprint() == withKey.Fingerprint() {
		t.Fatal("a newer row pushing another out of the window left the fingerprint unchanged")
	}
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}

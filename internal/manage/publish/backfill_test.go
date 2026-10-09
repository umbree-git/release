package publish_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"umbree-release-r2-mirror/manifest"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/register"
)

func (w *world) seedPublic(version string, n int) string {
	w.t.Helper()
	stamp := stampOf(version, n)
	backendtest.SeedRelease(w.public, "umbree/"+stamp+"/", zipsFor("umbree", stamp))
	return stamp
}

func (w *world) seedManifest(version, stamp string) {
	w.t.Helper()
	body, err := manifest.Build("umbree", "umbree/", version, stamp, []string{"umbree-darwin-arm64.zip", "umbree-linux-amd64.zip"}, epoch).Encode()
	if err != nil {
		w.t.Fatal(err)
	}
	w.public.Seed("umbree/latest.json", body)
}

func (w *world) backfill() publish.BackfillReport {
	w.t.Helper()
	rep, err := publish.Backfill(context.Background(), w.d, "umbree", "test-operator")
	if err != nil {
		w.t.Fatalf("backfill: %v (%+v)", err, rep)
	}
	return rep
}

func TestBackfillCreatesPublicRows(t *testing.T) {
	w := newWorld(t)
	stamps := []string{w.seedPublic("0.1.6", 1), w.seedPublic("0.1.7", 2), w.seedPublic("0.1.8", 3)}
	w.seedManifest("0.1.8", stamps[2])
	rep := w.backfill()
	if len(rep.Inserted) != 3 {
		t.Fatalf("report %+v", rep)
	}
	rows, err := w.st.List("umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("%d rows, want 3", len(rows))
	}
	for _, rv := range rows {
		if rv.State != "public" || rv.GatedPrunedAt.IsZero() || !strings.HasPrefix(rv.Stamp, "v"+rv.Version+".") {
			t.Fatalf("backfilled row %+v", rv)
		}
		var arts []register.Artifact
		if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil || len(arts) != 4 {
			t.Fatalf("row %s artifacts %s", rv.Stamp, rv.ArtifactsJSON)
		}
		for _, a := range arts {
			body, ok := w.public.Body(a.Key)
			if !ok || a.SHA256 != backendtest.SHA256(body) || a.Size != int64(len(body)) {
				t.Fatalf("row %s records %+v, not the public object", rv.Stamp, a)
			}
		}
	}
}

func TestBackfillSetsCurrentFromManifest(t *testing.T) {
	w := newWorld(t)
	w.seedPublic("0.1.6", 1)
	mid := w.seedPublic("0.1.7", 2)
	w.seedPublic("0.1.8", 3)
	w.seedManifest("0.1.7", mid)
	if rep := w.backfill(); rep.Current != mid {
		t.Fatalf("report current %q, want %q", rep.Current, mid)
	}
	cur, err := w.st.Current("umbree", "production")
	if err != nil || cur.Stamp != mid {
		t.Fatalf("current %+v, %v", cur, err)
	}
}

func TestBackfillIdempotent(t *testing.T) {
	w := newWorld(t)
	w.seedPublic("0.1.6", 1)
	top := w.seedPublic("0.1.7", 2)
	w.seedManifest("0.1.7", top)
	w.backfill()
	before, _ := w.st.List("umbree", "production")
	rep := w.backfill()
	after, _ := w.st.List("umbree", "production")
	if len(rep.Inserted) != 0 || len(rep.Existing) != 2 {
		t.Fatalf("second run report %+v", rep)
	}
	if len(before) != len(after) {
		t.Fatalf("rows %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("row changed on a second run:\n%+v\n%+v", before[i], after[i])
		}
	}
}

func TestBackfillSkipsUnknownShape(t *testing.T) {
	w := newWorld(t)
	top := w.seedPublic("0.1.7", 2)
	w.seedManifest("0.1.7", top)
	w.public.Seed("umbree/not-a-stamp/umbree-linux-amd64.zip", []byte("x"))
	w.public.Seed("umbree/beta/v0.2.0.beta.2026.09.05.deadbeef/umbree-linux-amd64.zip", []byte("x"))
	w.public.Seed("umbree/install.sh", []byte("#!/bin/sh"))
	rep := w.backfill()
	if len(rep.Inserted) != 1 || len(rep.Skipped) != 3 {
		t.Fatalf("report %+v, want 1 inserted and 3 skipped", rep)
	}
	for _, want := range []string{"not-a-stamp", "beta", "install.sh"} {
		if !strings.Contains(strings.Join(rep.Skipped, "\n"), want) {
			t.Fatalf("skipped %v does not name %s", rep.Skipped, want)
		}
	}
}

func TestBackfillVerifiesSums(t *testing.T) {
	w := newWorld(t)
	bad := w.seedPublic("0.1.6", 1)
	top := w.seedPublic("0.1.7", 2)
	w.seedManifest("0.1.7", top)
	body, _ := w.public.Body("umbree/" + bad + "/SHA256SUMS.txt")
	w.public.Seed("umbree/"+bad+"/SHA256SUMS.txt.minisig", backendtest.SignWith(otherKey(), body))
	rep, err := publish.Backfill(context.Background(), w.d, "umbree", "test-operator")
	if err == nil || len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0], bad) {
		t.Fatalf("a stamp with a bad signature: %v %+v", err, rep)
	}
	if _, gerr := w.st.ByStamp("umbree", "production", bad); gerr == nil {
		t.Fatal("a stamp whose sums do not verify was inserted")
	}
	if _, gerr := w.st.ByStamp("umbree", "production", top); gerr != nil {
		t.Fatalf("the good stamp was not inserted: %v", gerr)
	}
}

func TestPromoteRefusesWithoutCurrentWhenManifestExists(t *testing.T) {
	w := newWorld(t)
	live := w.seedPublic("0.5.0", 1)
	w.seedManifest("0.5.0", live)
	older, _ := w.stage("0.4.0", 2)
	newer, _ := w.stage("0.6.0", 3)
	err, ev := w.promote(newer)
	wantRefused(t, err, ev, "backfill")
	if !errors.Is(err, publish.ErrNeedsBackfill) || w.public.Count("COPY") != 0 {
		t.Fatalf("refusal %v copied %d", err, w.public.Count("COPY"))
	}
	w.backfill()
	err, ev = w.promote(older)
	wantRefused(t, err, ev, "not newer than")
	if err, ev := w.promote(newer); err != nil {
		t.Fatalf("after backfill: %v %+v", err, ev)
	}
}

func TestBackfillAdoptsCurrentAfterManualReplace(t *testing.T) {
	w := newWorld(t)
	older := w.live("0.1.0", 1)
	yanked := w.live("0.2.0", 2)
	if err := w.st.MarkYanked(yanked, "test-operator", "manifest replaced by hand", epoch); err != nil {
		t.Fatal(err)
	}
	w.seedManifest("0.1.0", w.row(older).Stamp)
	newer, _ := w.stage("0.3.0", 3)
	err, ev := w.promote(newer)
	wantRefused(t, err, ev, "backfill")
	rep := w.backfill()
	if rep.Current != w.row(older).Stamp || len(rep.Inserted) != 0 {
		t.Fatalf("backfill after a manual replace: %+v", rep)
	}
	if !w.row(older).IsCurrent {
		t.Fatal("backfill did not make the manifest's public row current")
	}
	log, err := w.st.AuditLog()
	if err != nil || len(log) != 2 || log[1].Action != "backfill-current" || log[1].RowID != older || log[1].Actor != "test-operator" {
		t.Fatalf("audit %+v, %v", log, err)
	}
	between, _ := w.stage("0.1.5", 4)
	err, ev = w.promote(between)
	wantRefused(t, err, ev, "not newer than")
	if err, ev := w.promote(newer); err != nil {
		t.Fatalf("promote after backfill: %v %+v", err, ev)
	}
}

func TestBackfillManifestNamingYankedOrStagedChangesNothing(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	yanked := w.live("0.2.0", 2)
	if err := w.st.MarkYanked(yanked, "test-operator", "pulled", epoch); err != nil {
		t.Fatal(err)
	}
	w.seedManifest("0.2.0", w.row(yanked).Stamp)
	rep := w.backfill()
	if rep.Current != "" || w.row(yanked).State != "yanked" || w.row(yanked).IsCurrent || !strings.Contains(strings.Join(rep.Skipped, "\n"), "yanked") {
		t.Fatalf("a manifest naming a yanked row: %+v", rep)
	}
	staged, _ := w.stage("0.3.0", 3)
	w.seedManifest("0.3.0", w.row(staged).Stamp)
	rep = w.backfill()
	if rep.Current != "" || w.row(staged).State != "staged" || w.row(staged).IsCurrent {
		t.Fatalf("a manifest naming a staged row: %+v", rep)
	}
	if _, err := w.st.Current("umbree", "production"); err == nil {
		t.Fatal("backfill made a row current")
	}
	if log, _ := w.st.AuditLog(); len(log) != 1 {
		t.Fatalf("audit %+v, want only the mark-yanked entry", log)
	}
}

func TestBackfillLeavesExistingCurrent(t *testing.T) {
	w := newWorld(t)
	older := w.live("0.1.0", 1)
	cur := w.live("0.2.0", 2)
	w.seedManifest("0.1.0", w.row(older).Stamp)
	rep := w.backfill()
	if rep.Current != "" || w.row(older).IsCurrent || !w.row(cur).IsCurrent {
		t.Fatalf("backfill moved the current row: %+v", rep)
	}
	if log, _ := w.st.AuditLog(); len(log) != 0 {
		t.Fatalf("backfill audited a change it did not make: %+v", log)
	}
}

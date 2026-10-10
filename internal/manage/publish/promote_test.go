package publish_test

import (
	"context"
	"errors"
	"path"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
)

func TestPromoteHappyPath(t *testing.T) {
	w := newWorld(t)
	prev := w.live("0.1.0", 1)
	id, arts := w.stage("0.2.0", 2)
	var order []string
	w.gated.OnCall = func(c backendtest.Call) { order = append(order, "gated "+c.Op) }
	w.public.OnCall = func(c backendtest.Call) {
		order = append(order, "public "+c.Op+" "+c.Key)
		if c.Op == "PUT" && w.row(id).State != "staged" {
			t.Errorf("the row flipped before the manifest was written")
		}
	}
	err, ev := w.promote(id)
	if err != nil {
		t.Fatalf("promote: %v %+v", err, ev)
	}
	if terminal(t, ev).Step != "done" {
		t.Fatalf("stream %+v", ev)
	}
	stamp := w.row(id).Stamp
	for _, a := range arts {
		dst := "umbree/" + stamp + "/" + path.Base(a.Key)
		got, ok := w.public.Body(dst)
		want, _ := w.gated.Body(a.Key)
		if !ok || string(got) != string(want) {
			t.Errorf("%s not copied byte for byte", dst)
		}
	}
	if w.manifestStamp() != stamp {
		t.Fatalf("manifest names %q, want %q", w.manifestStamp(), stamp)
	}
	if r := w.row(id); r.State != "public" || !r.IsCurrent || r.PromotedAt.IsZero() {
		t.Fatalf("promoted row %+v", r)
	}
	if w.row(prev).IsCurrent {
		t.Fatal("the previous current row is still current")
	}
	assertOrder(t, order)
}

func assertOrder(t *testing.T, order []string) {
	t.Helper()
	lastGated, firstCopy, lastCopy, manifest := -1, -1, -1, -1
	for i, o := range order {
		switch {
		case strings.HasPrefix(o, "gated "):
			lastGated = i
		case strings.HasPrefix(o, "public COPY"):
			if firstCopy < 0 {
				firstCopy = i
			}
			lastCopy = i
		case o == "public PUT umbree/latest.json":
			manifest = i
		}
	}
	if !(lastGated < firstCopy && lastCopy < manifest && manifest == len(order)-1) {
		t.Fatalf("order is not verify -> copy -> manifest last: %v", order)
	}
}

func TestPromoteRefusesNotStaged(t *testing.T) {
	w := newWorld(t)
	id := w.live("0.1.0", 1)
	err, ev := w.promote(id)
	wantRefused(t, err, ev, "not staged")
}

func TestPromoteRefusesNotPromotable(t *testing.T) {
	w := newWorld(t)
	w.live("0.9.9", 1)
	older, _ := w.stage("0.3.26", 2)
	copies := w.public.Count("COPY")
	err, ev := w.promote(older)
	wantRefused(t, err, ev, "not newer than")
	if w.public.Count("COPY") != copies || w.row(older).State != "staged" {
		t.Fatal("a refused rollback copied or moved")
	}
	newer, _ := w.stage("0.10.0", 3)
	if err, ev := w.promote(newer); err != nil {
		t.Fatalf("keep-control: %v %+v", err, ev)
	}
}

func TestPromoteRefusesUnknownChannel(t *testing.T) {
	w := newWorld(t)
	id, _ := w.stage("0.1.0", 1)
	db := rawDB(t, w.dir)
	if _, err := db.Exec(`UPDATE release_versions SET channel = 'beta' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	err, ev := w.promote(id)
	wantRefused(t, err, ev, "channel")
	if w.public.Count("COPY") != 0 {
		t.Fatal("copied a row on an unknown channel")
	}
}

func noCopyAfter(t *testing.T, w *world, id int64, says string) {
	t.Helper()
	err, ev := w.promote(id)
	wantRefused(t, err, ev, says)
	if n := w.public.Count("COPY"); n != 0 {
		t.Fatalf("%d copies after a failed verify", n)
	}
	if w.public.Count("PUT") != 0 || w.row(id).State != "staged" {
		t.Fatal("a failed verify wrote the manifest or moved the row")
	}
}

func TestPromoteSizeMismatchNoCopy(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	w.gated.ReportSize(arts[2].Key, arts[2].Size+1)
	noCopyAfter(t, w, id, "bytes")
}

func TestPromoteHashMismatchNoCopy(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	body, _ := w.gated.Body(arts[2].Key)
	body[0] ^= 1
	w.gated.Seed(arts[2].Key, body)
	noCopyAfter(t, w, id, "sha256")
}

func TestPromoteSumsDisagreeNoCopy(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	r := w.row(id)
	zips := zipsFor("umbree", r.Stamp)
	zips["umbree-darwin-arm64.zip"] = []byte("another build")
	sumsBody := backendtest.SumsFile(zips)
	w.gated.Seed(r.SumsKey, sumsBody)
	w.gated.Seed(r.MinisigKey, backendtest.Sign(sumsBody))
	fixCatalog(t, w, id, arts, map[string][]byte{r.SumsKey: sumsBody, r.MinisigKey: backendtest.Sign(sumsBody)})
	noCopyAfter(t, w, id, "SHA256SUMS.txt says")
}

func TestPromoteBadMinisigNoCopy(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	r := w.row(id)
	body, _ := w.gated.Body(r.SumsKey)
	other := backendtest.SignWith(otherKey(), body)
	w.gated.Seed(r.MinisigKey, other)
	fixCatalog(t, w, id, arts, map[string][]byte{r.MinisigKey: other})
	noCopyAfter(t, w, id, "minisign")
}

func TestPromoteManifestFailureLeavesPrevious(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	before, _ := w.public.Body("umbree/latest.json")
	id, _ := w.stage("0.2.0", 2)
	w.public.FailOn("PUT", "umbree/latest.json", errors.New("injected manifest fault"))
	err, ev := w.promote(id)
	wantRefused(t, err, ev, "injected manifest fault")
	after, _ := w.public.Body("umbree/latest.json")
	if string(after) != string(before) || w.row(id).State != "staged" {
		t.Fatal("a failed manifest write moved the manifest or the row")
	}
}

func TestPromoteRerunAfterManifestBeforeFlip(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	w.public.OnCall = func(c backendtest.Call) {
		if c.Op == "PUT" && c.Key == "umbree/latest.json" {
			_ = w.st.Close()
		}
	}
	err, ev := w.promote(id)
	if err == nil || terminal(t, ev).Step != "error" || !strings.Contains(terminal(t, ev).Message, "re-run") {
		t.Fatalf("a flip failure after the manifest: %v %+v", err, ev)
	}
	manifestBefore, _ := w.public.Body("umbree/latest.json")
	w.public.OnCall = nil
	w.reopen()
	copies := w.public.Count("COPY")
	if err, ev := w.promote(id); err != nil {
		t.Fatalf("re-run: %v %+v", err, ev)
	}
	if n := w.public.Count("COPY") - copies; n != 0 {
		t.Fatalf("the re-run copied %d objects again, want 0 of %d", n, len(arts))
	}
	manifestAfter, _ := w.public.Body("umbree/latest.json")
	if string(manifestAfter) != string(manifestBefore) || !w.row(id).IsCurrent {
		t.Fatal("the re-run did not rewrite the same manifest and complete the flip")
	}
}

func TestPromoteCopyErrorBody(t *testing.T) {
	w := newWorld(t)
	id, _ := w.stage("0.1.0", 1)
	w.d.Public = errorBodyPublic(t, w.gated)
	err, ev := w.promote(id)
	wantRefused(t, err, ev, "InternalError")
	if w.row(id).State != "staged" {
		t.Fatal("a copy answered with an <Error> body moved the row")
	}
}

func TestPromoteDestHeadSizeChecked(t *testing.T) {
	w := newWorld(t)
	id, arts := w.stage("0.1.0", 1)
	dst := "umbree/" + w.row(id).Stamp + "/" + path.Base(arts[0].Key)
	w.public.OnCall = func(c backendtest.Call) {
		if c.Op == "COPY" && c.Key == dst {
			w.public.ReportSize(dst, arts[0].Size-1)
		}
	}
	err, ev := w.promote(id)
	wantRefused(t, err, ev, "after the copy")
	if w.public.Count("PUT") != 0 || w.row(id).State != "staged" {
		t.Fatal("a short copy reached the manifest")
	}
}

func TestPromoteAfterHookErrorDoesNotFail(t *testing.T) {
	w := newWorld(t)
	id, _ := w.stage("0.1.0", 1)
	w.d.AfterPromote = func(ctx context.Context, component, channel string) (string, error) {
		return "", errors.New("retention hook fault")
	}
	err, ev := w.promote(id)
	if err != nil || terminal(t, ev).Step != "done" {
		t.Fatalf("a hook error failed the promote: %v %+v", err, ev)
	}
	found := false
	for _, e := range ev {
		found = found || strings.Contains(e.Message, "retention hook fault")
	}
	if !found || !w.row(id).IsCurrent {
		t.Fatalf("the hook error was not streamed: %+v", ev)
	}
}

func TestPromoteUsesStoredKeysVerbatim(t *testing.T) {
	w := newWorld(t)
	stamp := stampOf("0.1.0", 1)
	id, arts := w.stageAs("umbree", "0.1.0", 1, "relocated/umbree/"+stamp+"/")
	if err, ev := w.promote(id); err != nil {
		t.Fatalf("promote: %v %+v", err, ev)
	}
	for _, c := range w.public.Calls() {
		if c.Op == "COPY" && !strings.HasPrefix(c.Src, "gated-test/relocated/umbree/") {
			t.Fatalf("copied from %q, not the stored key", c.Src)
		}
	}
	for _, a := range arts {
		if _, ok := w.public.Body("umbree/" + stamp + "/" + path.Base(a.Key)); !ok {
			t.Fatalf("%s did not reach the public layout", a.Key)
		}
	}
}

func TestPromoteFloorAfterYankAndManifestDeleted(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	top := w.live("0.2.0", 2)
	if err := w.st.MarkYanked(top, "test-operator", "manifest deleted by hand", epoch); err != nil {
		t.Fatal(err)
	}
	w.public.Remove("umbree/latest.json")
	between, _ := w.stage("0.1.5", 3)
	err, ev := w.promote(between)
	wantRefused(t, err, ev, "not newer than")
	if !strings.Contains(err.Error(), w.row(top).Stamp) || !strings.Contains(err.Error(), "yanked") {
		t.Fatalf("the refusal %q does not name the mark's stamp and state", err)
	}
	newer, _ := w.stage("0.3.0", 4)
	if err, ev := w.promote(newer); err != nil {
		t.Fatalf("a row above the yanked mark: %v %+v", err, ev)
	}
}

func TestPromoteFloorAfterYankSkippedPrunedRow(t *testing.T) {
	w := newWorld(t)
	w.live("0.1.0", 1)
	mid := w.live("0.2.0", 2)
	top := w.live("0.3.0", 3)
	w.public.Remove("umbree/" + w.row(mid).Stamp + "/umbree-linux-amd64.zip")
	if err, ev := w.yank(top); err != nil {
		t.Fatalf("yank: %v %+v", err, ev)
	}
	below, _ := w.stage("0.1.5", 4)
	err, ev := w.promote(below)
	wantRefused(t, err, ev, "not newer than")
	between, _ := w.stage("0.2.5", 5)
	err, ev = w.promote(between)
	wantRefused(t, err, ev, "not newer than")
	above, _ := w.stage("0.3.1", 6)
	if err, ev := w.promote(above); err != nil {
		t.Fatalf("a row above the mark: %v %+v", err, ev)
	}
}

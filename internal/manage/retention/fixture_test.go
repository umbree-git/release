package retention_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

var epoch = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

type world struct {
	t      *testing.T
	dir    string
	st     *store.Store
	gated  *backendtest.Store
	public *backendtest.Store
	d      publish.Deps
	r      *retention.Retainer
	ids    map[string]int64
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w := &world{t: t, dir: dir, st: st, gated: backendtest.New("gated-retention"), public: backendtest.New("public-retention"), ids: map[string]int64{}}
	w.public.Link(w.gated)
	clock := func() time.Time { return epoch }
	locks := publish.NewLocks(publish.DefaultLockWait)
	w.d = publish.Deps{Store: st, Gated: w.gated, Public: w.public, Key: backendtest.ReleaseKey(), Locks: locks, Now: clock}
	w.r = &retention.Retainer{Store: st, Gated: w.gated, Public: w.public, Locks: locks, Now: clock}
	return w
}

func stampOf(version string) string { return fmt.Sprintf("v%s.2026.10.08.%08x", version, 1) }

func (w *world) stage(version string) int64 {
	w.t.Helper()
	stamp := stampOf(version)
	base := "umbree/production/" + stamp + "/"
	arts := backendtest.SeedRelease(w.gated, base, map[string][]byte{
		"umbree-darwin-arm64.zip": []byte("zip a " + stamp), "umbree-linux-amd64.zip": []byte("zip b " + stamp)})
	body, err := json.Marshal(arts)
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := w.st.InsertStaged(store.ReleaseVersion{
		Component: "umbree", Channel: "production", Version: version, Stamp: stamp,
		ArtifactsJSON: string(body), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName,
		CreatedAt: epoch,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	w.ids[version] = id
	return id
}

func (w *world) promote(id int64) []publish.Event {
	w.t.Helper()
	var buf bytes.Buffer
	if err := publish.Promote(context.Background(), w.d, id, "test-operator", &buf); err != nil {
		w.t.Fatalf("promote row %d: %v\n%s", id, err, buf.String())
	}
	return events(w.t, buf.Bytes())
}

func (w *world) live(versions ...string) {
	w.t.Helper()
	for _, v := range versions {
		w.promote(w.stage(v))
	}
}

func publishYank(w *world, id int64) error {
	return publish.Yank(context.Background(), w.d, id, "test-operator", nil)
}

func (w *world) row(version string) store.ReleaseVersion {
	w.t.Helper()
	rv, err := w.st.Get(w.ids[version])
	if err != nil {
		w.t.Fatal(err)
	}
	return *rv
}

func (w *world) gatedKeys(version string) []string {
	w.t.Helper()
	rv := w.row(version)
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil {
		w.t.Fatal(err)
	}
	var keys []string
	for _, a := range arts {
		keys = append(keys, a.Key)
	}
	return keys
}

func (w *world) publicKeys(version string) []string {
	w.t.Helper()
	var keys []string
	for _, k := range w.gatedKeys(version) {
		keys = append(keys, publish.PublicKey("umbree", stampOf(version), k))
	}
	return keys
}

func (w *world) holds(s *backendtest.Store, keys []string) bool {
	for _, k := range keys {
		if _, ok := s.Body(k); !ok {
			return false
		}
	}
	return len(keys) > 0
}

func (w *world) holdsNone(s *backendtest.Store, keys []string) bool {
	for _, k := range keys {
		if _, ok := s.Body(k); ok {
			return false
		}
	}
	return true
}

func (w *world) wantGated(t *testing.T, kept, gone []string) {
	t.Helper()
	for _, v := range kept {
		if !w.holds(w.gated, w.gatedKeys(v)) {
			t.Errorf("%s: its gated bytes are gone, want kept", v)
		}
	}
	for _, v := range gone {
		if !w.holdsNone(w.gated, w.gatedKeys(v)) {
			t.Errorf("%s: its gated bytes are still there, want deleted", v)
		}
	}
}

func (w *world) wantPublic(t *testing.T, kept, gone []string) {
	t.Helper()
	for _, v := range kept {
		if !w.holds(w.public, w.publicKeys(v)) {
			t.Errorf("%s: its public bytes are gone, want kept", v)
		}
	}
	for _, v := range gone {
		if !w.holdsNone(w.public, w.publicKeys(v)) {
			t.Errorf("%s: its public bytes are still there, want deleted", v)
		}
	}
}

func (w *world) retain(windows ...retention.Window) []retention.Report {
	w.t.Helper()
	reps, err := w.r.Retain(context.Background(), "umbree", "production", "test-operator", windows...)
	if err != nil {
		w.t.Fatalf("retain %v: %v", windows, err)
	}
	return reps
}

func (w *world) plan(win retention.Window) retention.Plan {
	w.t.Helper()
	p, err := w.r.Plan(context.Background(), "umbree", "production", win)
	if err != nil {
		w.t.Fatalf("plan %s: %v", win, err)
	}
	return p
}

func (w *world) rawDB() *sql.DB {
	w.t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(w.dir, store.DBFile))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = db.Close() })
	return db
}

func (w *world) addArtifact(version, key string) {
	w.t.Helper()
	rv := w.row(version)
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil {
		w.t.Fatal(err)
	}
	arts = append(arts, register.Artifact{Key: key, Size: 1, SHA256: "00"})
	body, err := json.Marshal(arts)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := w.rawDB().Exec(`UPDATE release_versions SET artifacts_json = ? WHERE id = ?`, string(body), rv.ID); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) deletes() []string {
	var out []string
	for _, s := range []*backendtest.Store{w.gated, w.public} {
		for _, c := range s.Calls() {
			if c.Op == "DELETE" {
				out = append(out, s.Bucket()+":"+c.Key)
			}
		}
	}
	return out
}

func events(t *testing.T, body []byte) []publish.Event {
	t.Helper()
	var out []publish.Event
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		var e publish.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("stream line %q is not an event: %v", sc.Text(), err)
		}
		out = append(out, e)
	}
	return out
}

func skippedKeys(skips []retention.Skip) []string {
	var out []string
	for _, s := range skips {
		out = append(out, s.Key)
	}
	slices.Sort(out)
	return out
}

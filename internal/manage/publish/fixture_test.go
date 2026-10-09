package publish_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

var epoch = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func TestMain(m *testing.M) {
	code := m.Run()
	for _, s := range backendtest.All() {
		for _, c := range s.Calls() {
			if c.Op == "DELETE" {
				fmt.Fprintf(os.Stderr, "FAIL: %s saw DELETE %s; no publish path deletes an object\n", s.Bucket(), c.Key)
				code = 1
			}
		}
	}
	os.Exit(code)
}

type world struct {
	t      *testing.T
	dir    string
	st     *store.Store
	gated  *backendtest.Store
	public *backendtest.Store
	d      publish.Deps
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	w := &world{t: t, dir: dir, st: st, gated: backendtest.New("gated-test"), public: backendtest.New("public-test")}
	w.public.Link(w.gated)
	w.d = publish.Deps{
		Store: st, Gated: w.gated, Public: w.public, Key: backendtest.ReleaseKey(),
		Locks: publish.NewLocks(publish.DefaultLockWait), Now: func() time.Time { return epoch },
	}
	return w
}

func stampOf(version string, n int) string {
	return fmt.Sprintf("v%s.2026.10.08.%08x", version, n)
}

func zipsFor(component, stamp string) map[string][]byte {
	return map[string][]byte{
		component + "-darwin-arm64.zip": []byte("zip a " + stamp),
		component + "-linux-amd64.zip":  []byte("zip b " + stamp),
	}
}

func (w *world) stageAs(component, version string, n int, base string) (int64, []register.Artifact) {
	w.t.Helper()
	stamp := stampOf(version, n)
	if base == "" {
		base = component + "/production/" + stamp + "/"
	}
	arts := backendtest.SeedRelease(w.gated, base, zipsFor(component, stamp))
	body, err := json.Marshal(arts)
	if err != nil {
		w.t.Fatal(err)
	}
	id, err := w.st.InsertStaged(store.ReleaseVersion{
		Component: component, Channel: "production", Version: version, Stamp: stamp,
		ArtifactsJSON: string(body), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName,
		CreatedAt: epoch,
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return id, arts
}

func (w *world) stage(version string, n int) (int64, []register.Artifact) {
	return w.stageAs("umbree", version, n, "")
}

func (w *world) promote(id int64) (error, []publish.Event) {
	w.t.Helper()
	var buf bytes.Buffer
	err := publish.Promote(context.Background(), w.d, id, "test-operator", &buf)
	return err, events(w.t, buf.Bytes())
}

func (w *world) live(version string, n int) int64 {
	w.t.Helper()
	id, _ := w.stage(version, n)
	if err, ev := w.promote(id); err != nil {
		w.t.Fatalf("set up live %s: %v %+v", version, err, ev)
	}
	return id
}

func (w *world) row(id int64) store.ReleaseVersion {
	w.t.Helper()
	rv, err := w.st.Get(id)
	if err != nil {
		w.t.Fatal(err)
	}
	return *rv
}

func (w *world) manifestStamp() string {
	w.t.Helper()
	body, ok := w.public.Body("umbree/latest.json")
	if !ok {
		return ""
	}
	var m struct {
		Stamp string `json:"stamp"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		w.t.Fatal(err)
	}
	return m.Stamp
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

func terminal(t *testing.T, ev []publish.Event) publish.Event {
	t.Helper()
	n := 0
	for _, e := range ev {
		if e.Step == "done" || e.Step == "error" {
			n++
		}
	}
	if n != 1 || len(ev) == 0 {
		t.Fatalf("stream has %d terminal events, want exactly 1: %+v", n, ev)
	}
	last := ev[len(ev)-1]
	if last.Step != "done" && last.Step != "error" {
		t.Fatalf("the terminal event is not last: %+v", ev)
	}
	return last
}

func wantRefused(t *testing.T, err error, ev []publish.Event, says string) {
	t.Helper()
	if err == nil {
		t.Fatalf("accepted, want a refusal saying %q", says)
	}
	last := terminal(t, ev)
	if last.Step != "error" || !strings.Contains(last.Message, says) || !strings.Contains(err.Error(), says) {
		t.Fatalf("refusal %v / %+v does not say %q", err, last, says)
	}
}

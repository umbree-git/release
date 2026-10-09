package web_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/web"
	"github.com/umbree-git/release/internal/register"
)

func stagedDeps(t *testing.T) (publish.Deps, int64) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	gated, public := backendtest.New("gated-web"), backendtest.New("public-web")
	public.Link(gated)
	stamp := "v0.1.9.2026.10.09.0000abcd"
	base := "umbree/production/" + stamp + "/"
	arts := backendtest.SeedRelease(gated, base, map[string][]byte{"umbree-linux-amd64.zip": []byte("zip")})
	body, _ := json.Marshal(arts)
	id, err := st.InsertStaged(store.ReleaseVersion{Component: "umbree", Channel: "production", Version: "0.1.9", Stamp: stamp,
		ArtifactsJSON: string(body), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName, CreatedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	return publish.Deps{Store: st, Gated: gated, Public: public, Key: backendtest.ReleaseKey(), Locks: publish.NewLocks(publish.DefaultLockWait)}, id
}

func testRouter(d publish.Deps) *http.ServeMux {
	api := web.PublishAPI{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/releases/{id}/promote", func(w http.ResponseWriter, r *http.Request) { api.Promote(w, r, "test-router") })
	mux.HandleFunc("POST /api/v1/releases/{id}/yank", func(w http.ResponseWriter, r *http.Request) { api.Yank(w, r, "test-router") })
	return mux
}

func TestPublishAPIStreamsNDJSON(t *testing.T) {
	d, id := stagedDeps(t)
	srv := httptest.NewServer(testRouter(d))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/v1/releases/"+strconv.FormatInt(id, 10)+"/promote", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("HTTP %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var steps []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		var e publish.Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		steps = append(steps, e.Step)
	}
	if len(steps) < 4 || steps[len(steps)-1] != "done" {
		t.Fatalf("steps %v", steps)
	}
	assertBusy409(t, d, srv, id)
	r, err := http.Post(srv.URL+"/api/v1/releases/999/promote", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown row: HTTP %d", r.StatusCode)
	}
}

func assertBusy409(t *testing.T, d publish.Deps, srv *httptest.Server, id int64) {
	t.Helper()
	fired := make(chan time.Time, 1)
	fired <- time.Now()
	d.Locks.After = func(time.Duration) <-chan time.Time { return fired }
	release, err := d.Locks.Acquire(context.Background(), "umbree", "production")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	resp, err := http.Post(srv.URL+"/api/v1/releases/"+strconv.FormatInt(id, 10)+"/yank", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("a busy channel: HTTP %d, want 409", resp.StatusCode)
	}
}

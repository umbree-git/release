package publish_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"umbree-release-r2-mirror/r2"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.Join(dir, store.DBFile))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (w *world) reopen() {
	w.t.Helper()
	st, err := store.Open(w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() { _ = st.Close() })
	w.st = st
	w.d.Store = st
}

func otherKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("not the release key"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func fixCatalog(t *testing.T, w *world, id int64, arts []register.Artifact, bodies map[string][]byte) {
	t.Helper()
	for i := range arts {
		if b, ok := bodies[arts[i].Key]; ok {
			arts[i].Size = int64(len(b))
			arts[i].SHA256 = backendtest.SHA256(b)
		}
	}
	body, err := json.Marshal(arts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB(t, w.dir).Exec(`UPDATE release_versions SET artifacts_json = ? WHERE id = ?`, string(body), id); err != nil {
		t.Fatal(err)
	}
}

type errorBodyDoer struct{}

func (errorBodyDoer) Do(req *http.Request) (*http.Response, error) {
	status, body := http.StatusNotFound, ""
	if req.Header.Get("X-Amz-Copy-Source") != "" {
		status, body = http.StatusOK, "<Error><Code>InternalError</Code><Message>retry</Message></Error>"
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func errorBodyPublic(t *testing.T, gated *backendtest.Store) *r2.Client {
	t.Helper()
	return r2.New("acct", "public-test", "AKID", "SECRET", errorBodyDoer{})
}

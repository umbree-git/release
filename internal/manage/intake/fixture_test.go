package intake_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

const (
	testStamp = "v0.1.8.2026.09.20.7162a3f3"
	testBase  = "umbree/production/" + testStamp + "/"
)

var epoch = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fixture struct {
	t    *testing.T
	st   *store.Store
	srv  *httptest.Server
	priv ed25519.PrivateKey
	now  time.Time
	mu   sync.Mutex
	log  *lockedBuffer
}

func keyFromSeed(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(label))
	return ed25519.NewKeyFromSeed(seed[:])
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{t: t, st: st, priv: keyFromSeed("intake release key"), now: epoch, log: &lockedBuffer{}}
	logger := slog.New(slog.NewTextHandler(f.log, nil))
	h := intake.New(st, f.priv.Public().(ed25519.PublicKey), f.clock, logger)
	mux := http.NewServeMux()
	h.Routes(mux)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fixture) post(path string, body []byte) (int, string) {
	f.t.Helper()
	resp, err := http.Post(f.srv.URL+path, "application/json", bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	return resp.StatusCode, string(raw)
}

func (f *fixture) issueNonce() string {
	f.t.Helper()
	code, body := f.post("/api/v1/releases/nonce", []byte(`{}`))
	if code != http.StatusOK {
		f.t.Fatalf("nonce: HTTP %d %s", code, body)
	}
	var out struct {
		Nonce     string `json:"nonce"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.Nonce == "" {
		f.t.Fatalf("nonce body %q: %v", body, err)
	}
	return out.Nonce
}

func payload(nonce string) register.Payload {
	sum := strings.Repeat("ab", 32)
	return register.Payload{
		Nonce: nonce, Component: "umbree", Channel: "production", Version: "0.1.8", Stamp: testStamp,
		Artifacts: []register.Artifact{
			{Key: testBase + "umbree-darwin-arm64.zip", Size: 10, SHA256: sum},
			{Key: testBase + "umbree-linux-amd64.zip", Size: 11, SHA256: sum},
			{Key: testBase + register.SumsName, Size: 12, SHA256: sum},
			{Key: testBase + register.MinisigName, Size: 13, SHA256: sum},
		},
		SumsKey:    testBase + register.SumsName,
		MinisigKey: testBase + register.MinisigName,
	}
}

type signer interface {
	SigningBytes() ([]byte, error)
}

func sign(t *testing.T, priv ed25519.PrivateKey, p signer) string {
	t.Helper()
	msg, err := p.SigningBytes()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
}

func envelope[T signer](t *testing.T, priv ed25519.PrivateKey, p T) []byte {
	t.Helper()
	body, err := json.Marshal(register.Envelope[T]{Payload: p, Sig: sign(t, priv, p)})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (f *fixture) register(p register.Payload) (int, string) {
	f.t.Helper()
	return f.post("/api/v1/releases/register", envelope(f.t, f.priv, p))
}

func (f *fixture) rowCount() int {
	f.t.Helper()
	rows, err := f.st.List("umbree", "production")
	if err != nil {
		f.t.Fatal(err)
	}
	return len(rows)
}

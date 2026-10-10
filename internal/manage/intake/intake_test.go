package intake_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/register"
)

const refusedBody = `{"error":"registration refused"}` + "\n"

func TestNonceIssued(t *testing.T) {
	f := newFixture(t)
	a, b := f.issueNonce(), f.issueNonce()
	if a == b {
		t.Fatalf("two nonces are equal: %q", a)
	}
	if len(a) < 40 {
		t.Fatalf("nonce %q is short", a)
	}
	if err := f.st.ConsumeNonce(a, epoch.Add(intake.NonceTTL-time.Second)); err != nil {
		t.Fatalf("an issued nonce is not live for its TTL: %v", err)
	}
	if err := f.st.ConsumeNonce(b, epoch.Add(intake.NonceTTL)); err == nil {
		t.Fatal("an issued nonce outlived its TTL")
	}
	resp, err := http.Get(f.srv.URL + "/api/v1/releases/nonce")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET nonce: HTTP %d, want 405", resp.StatusCode)
	}
}

func TestNonceRateLimited(t *testing.T) {
	f := newFixture(t)
	for range intake.NonceLimit {
		f.issueNonce()
	}
	code, _ := f.post("/api/v1/releases/nonce", []byte(`{}`))
	if code != http.StatusTooManyRequests {
		t.Fatalf("nonce %d in one window: HTTP %d, want 429", intake.NonceLimit+1, code)
	}
	f.advance(intake.NonceWindow)
	f.issueNonce()
}

func TestRegisterCreatesStaged(t *testing.T) {
	f := newFixture(t)
	p := payload(f.issueNonce())
	code, body := f.register(p)
	if code != http.StatusCreated {
		t.Fatalf("register: HTTP %d %s", code, body)
	}
	var created register.RowStatus
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	row, err := f.st.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "staged" || row.Stamp != testStamp || row.Version != "0.1.8" || created.State != "staged" {
		t.Fatalf("row %+v / response %+v, want a staged %s", row, created, testStamp)
	}
	posted, err := json.Marshal(p.Artifacts)
	if err != nil {
		t.Fatal(err)
	}
	if row.ArtifactsJSON != string(posted) {
		t.Fatalf("artifacts_json\n got %s\nwant %s", row.ArtifactsJSON, posted)
	}
	if row.SumsKey != p.SumsKey || row.MinisigKey != p.MinisigKey || !row.CreatedAt.Equal(epoch) {
		t.Fatalf("row %+v does not carry the posted keys and time", row)
	}
	if f.rowCount() != 1 {
		t.Fatalf("row count %d, want 1", f.rowCount())
	}
}

func TestUniformRefusalDepartsFromSource(t *testing.T) {
	f := newFixture(t)
	wrong := payload(f.issueNonce())
	codeSig, bodySig := f.post("/api/v1/releases/register", envelope(t, keyFromSeed("another key"), wrong))
	codeNonce, bodyNonce := f.register(payload("never-issued"))
	if codeSig != http.StatusForbidden || codeNonce != http.StatusForbidden {
		t.Fatalf("bad signature HTTP %d, bad nonce HTTP %d; want 403 both", codeSig, codeNonce)
	}
	if bodySig != bodyNonce || bodySig != refusedBody {
		t.Fatalf("bodies differ or are not the uniform refusal:\n sig   %q\n nonce %q", bodySig, bodyNonce)
	}
	logs := f.log.String()
	if !strings.Contains(logs, "bad signature") || !strings.Contains(logs, "nonce:") {
		t.Fatalf("the log does not say which half failed:\n%s", logs)
	}
	if f.rowCount() != 0 {
		t.Fatal("a refused registration created a row")
	}
}

func assertRefused(t *testing.T, f *fixture, code int, body, logWant string) {
	t.Helper()
	if code != http.StatusForbidden || body != refusedBody {
		t.Fatalf("HTTP %d %q, want 403 %q", code, body, refusedBody)
	}
	if !strings.Contains(f.log.String(), logWant) {
		t.Fatalf("log lacks %q:\n%s", logWant, f.log.String())
	}
}

func TestReplayRefused(t *testing.T) {
	f := newFixture(t)
	body := envelope(t, f.priv, payload(f.issueNonce()))
	if code, resp := f.post("/api/v1/releases/register", body); code != http.StatusCreated {
		t.Fatalf("first: HTTP %d %s", code, resp)
	}
	code, resp := f.post("/api/v1/releases/register", body)
	assertRefused(t, f, code, resp, "already used")
	if f.rowCount() != 1 {
		t.Fatalf("row count %d after a replay, want 1", f.rowCount())
	}
}

func TestUnknownNonceRefused(t *testing.T) {
	f := newFixture(t)
	code, body := f.register(payload("never-issued"))
	assertRefused(t, f, code, body, "not found")
}

func TestExpiredNonceRefused(t *testing.T) {
	f := newFixture(t)
	p := payload(f.issueNonce())
	f.advance(intake.NonceTTL)
	code, body := f.register(p)
	assertRefused(t, f, code, body, "expired")
	if f.rowCount() != 0 {
		t.Fatal("an expired nonce created a row")
	}
}

func TestWrongKeyRefused(t *testing.T) {
	f := newFixture(t)
	p := payload(f.issueNonce())
	code, body := f.post("/api/v1/releases/register", envelope(t, keyFromSeed("another key"), p))
	assertRefused(t, f, code, body, "bad signature")
	if code, body := f.register(p); code != http.StatusCreated {
		t.Fatalf("the nonce a wrong-key body carried was burned: HTTP %d %s", code, body)
	}
}

func TestNonceOutsideSignedBodyRefused(t *testing.T) {
	f := newFixture(t)
	nonce := f.issueNonce()
	p := payload("")
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+"/api/v1/releases/register", bytes.NewReader(envelope(t, f.priv, p)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("Nonce", nonce)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a nonce outside the signed body: HTTP %d, want 403", resp.StatusCode)
	}
	if f.rowCount() != 0 {
		t.Fatal("a nonce outside the signed body created a row")
	}
	withField := []byte(strings.Replace(string(envelope(t, f.priv, p)), `{"payload"`, `{"nonce":"`+nonce+`","payload"`, 1))
	if code, _ := f.post("/api/v1/releases/register", withField); code == http.StatusCreated {
		t.Fatal("a nonce beside the payload was accepted")
	}
}

func TestDuplicateStamp409(t *testing.T) {
	f := newFixture(t)
	if code, body := f.register(payload(f.issueNonce())); code != http.StatusCreated {
		t.Fatalf("first: HTTP %d %s", code, body)
	}
	dup := envelope(t, f.priv, payload(f.issueNonce()))
	code, body := f.post("/api/v1/releases/register", dup)
	if code != http.StatusConflict {
		t.Fatalf("duplicate stamp: HTTP %d %s, want 409", code, body)
	}
	if f.rowCount() != 1 {
		t.Fatalf("row count %d, want 1", f.rowCount())
	}
	code, body = f.post("/api/v1/releases/register", dup)
	if code != http.StatusForbidden || body != refusedBody {
		t.Fatalf("replay of the 409 body: HTTP %d %q; its nonce was not burned before the insert", code, body)
	}
}

func TestBakedKeyIsReleaseKey(t *testing.T) {
	root, err := os.ReadFile("../../../umbree-release.pub")
	if err != nil {
		t.Fatal(err)
	}
	baked, err := os.ReadFile("umbree-release.pub")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(root, baked) {
		t.Fatal("internal/manage/intake/umbree-release.pub differs from the repo root's umbree-release.pub")
	}
	key, err := intake.ReleaseKey()
	if err != nil {
		t.Fatalf("the baked key does not parse: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("baked key is %d bytes", len(key))
	}
}

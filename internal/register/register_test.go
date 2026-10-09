package register_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/umbree-git/release/internal/register"
)

const (
	testStamp = "v0.1.8.2026.09.20.7162a3f3"
	testBase  = "umbree/production/" + testStamp + "/"
)

type testKey struct {
	path   string
	public ed25519.PublicKey
	secret ed25519.PrivateKey
	line   string
}

func writeSigningKey(t *testing.T) testKey {
	t.Helper()
	seed := sha256.Sum256([]byte("umbree register test-only key"))
	priv := ed25519.NewKeyFromSeed(seed[:])
	raw := make([]byte, 158)
	copy(raw[0:], "Ed")
	copy(raw[4:], "B2")
	copy(raw[54:], []byte{1, 2, 3, 4, 5, 6, 7, 8})
	copy(raw[62:], priv)
	line := base64.StdEncoding.EncodeToString(raw)
	path := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(path, []byte("untrusted comment: test-only key\n"+line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return testKey{path: path, public: priv.Public().(ed25519.PublicKey), secret: priv, line: line}
}

func loadKey(t *testing.T, k testKey) register.SigningKey {
	t.Helper()
	key, err := register.LoadSigningKey(k.path)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func samplePayload() register.Payload {
	sum := strings.Repeat("a", 64)
	return register.Payload{
		Component: "umbree", Channel: "production", Version: "0.1.8", Stamp: testStamp,
		Artifacts: []register.Artifact{
			{Key: testBase + "umbree-linux-arm64.zip", Size: 42, SHA256: sum},
			{Key: testBase + register.SumsName, Size: 3, SHA256: sum},
			{Key: testBase + register.MinisigName, Size: 4, SHA256: sum},
		},
		SumsKey:    testBase + register.SumsName,
		MinisigKey: testBase + register.MinisigName,
	}
}

func TestSigningBytesCanonical(t *testing.T) {
	p := register.Payload{
		Nonce: "n1", Component: "umbree", Channel: "production", Version: "0.1.8", Stamp: testStamp,
		Artifacts:  []register.Artifact{{Key: testBase + "a<b>&.zip", Size: 42, SHA256: "ab"}},
		SumsKey:    testBase + "SHA256SUMS.txt",
		MinisigKey: testBase + "SHA256SUMS.txt.minisig",
	}
	got, err := p.SigningBytes()
	if err != nil {
		t.Fatal(err)
	}
	want := "umbree-release register v1\n" +
		`{"nonce":"n1","component":"umbree","channel":"production","version":"0.1.8","stamp":"` + testStamp + `",` +
		`"artifacts":[{"key":"` + testBase + `a<b>&.zip","size":42,"sha256":"ab"}],` +
		`"sums_key":"` + testBase + `SHA256SUMS.txt","minisig_key":"` + testBase + `SHA256SUMS.txt.minisig"}`
	if string(got) != want {
		t.Fatalf("register signing bytes\n got %q\nwant %q", got, want)
	}
	q := register.StatusQuery{Nonce: "n2", Component: "umbreed", Channel: "production", Stamp: testStamp}
	got, err = q.SigningBytes()
	if err != nil {
		t.Fatal(err)
	}
	want = "umbree-release status v1\n" +
		`{"nonce":"n2","component":"umbreed","channel":"production","stamp":"` + testStamp + `"}`
	if string(got) != want {
		t.Fatalf("status signing bytes\n got %q\nwant %q", got, want)
	}
	again, _ := p.SigningBytes()
	p2 := p
	p2.Nonce = "n3"
	other, _ := p2.SigningBytes()
	if string(again) == string(other) {
		t.Fatal("the nonce is not part of the signed bytes")
	}
}

type fakeService struct {
	registerStatus int
	registerBody   string
	statusStatus   int
	statusBody     register.RowStatus
	calls          atomic.Int32
	key            ed25519.PublicKey
	lastRegister   []byte
}

func (f *fakeService) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	nonces := atomic.Int32{}
	mux.HandleFunc("POST /api/v1/releases/nonce", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		fmt.Fprintf(w, `{"nonce":"nonce-%d","expires_in":300}`, nonces.Add(1))
	})
	mux.HandleFunc("POST /api/v1/releases/register", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		f.lastRegister = body
		var env register.Envelope[register.Payload]
		if err := json.Unmarshal(body, &env); err != nil {
			t.Errorf("register body: %v", err)
		}
		msg, _ := env.Payload.SigningBytes()
		sig, _ := base64.StdEncoding.DecodeString(env.Sig)
		if f.key != nil && !ed25519.Verify(f.key, msg, sig) {
			t.Errorf("the client's signature does not verify")
		}
		w.WriteHeader(f.registerStatus)
		if f.registerBody != "" {
			fmt.Fprint(w, f.registerBody)
			return
		}
		fmt.Fprintf(w, `{"id":7,"state":"staged","stamp":%q,"version":"0.1.8"}`, env.Payload.Stamp)
	})
	mux.HandleFunc("POST /api/v1/releases/status", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		w.WriteHeader(f.statusStatus)
		_ = json.NewEncoder(w).Encode(f.statusBody)
	})
	return mux
}

func newFake(t *testing.T, f *fakeService) (*register.Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewTLSServer(f.handler(t))
	t.Cleanup(srv.Close)
	c, err := register.NewClient(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return c, srv
}

func stagedFake(k testKey) *fakeService {
	return &fakeService{
		registerStatus: http.StatusCreated, statusStatus: http.StatusOK, key: k.public,
		statusBody: register.RowStatus{ID: 7, State: "staged", Stamp: testStamp, Version: "0.1.8"},
	}
}

func TestClientRequiresHTTPS(t *testing.T) {
	var dialed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dialed.Store(true) }))
	t.Cleanup(srv.Close)
	for _, raw := range []string{srv.URL, "http://localhost:1", "ftp://example.invalid", "", "manage.example.invalid"} {
		if _, err := register.NewClient(raw, srv.Client()); err == nil {
			t.Errorf("NewClient(%q) accepted a non-https URL", raw)
		}
	}
	if dialed.Load() {
		t.Fatal("a refused URL was dialed")
	}
	if _, err := register.NewClient("https://manage.example.invalid/", nil); err != nil {
		t.Fatalf("https URL refused: %v", err)
	}
}

func TestClientNon2xxIsError(t *testing.T) {
	k := writeSigningKey(t)
	for _, code := range []int{401, 403, 409, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			f := stagedFake(k)
			f.registerStatus = code
			c, _ := newFake(t, f)
			_, err := c.Register(context.Background(), samplePayload(), loadKey(t, k))
			if err == nil {
				t.Fatalf("HTTP %d from register was not an error", code)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("HTTP %d", code)) {
				t.Fatalf("error %q does not name HTTP %d", err, code)
			}
		})
	}
}

func TestClient2xxThenStatusMismatchIsError(t *testing.T) {
	k := writeSigningKey(t)
	cases := map[string]register.RowStatus{
		"public":        {ID: 7, State: "public", Stamp: testStamp, Version: "0.1.8"},
		"another stamp": {ID: 7, State: "staged", Stamp: "v0.1.7.2026.09.01.00000000", Version: "0.1.7"},
		"empty":         {},
	}
	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			f := stagedFake(k)
			f.statusBody = row
			c, _ := newFake(t, f)
			if _, err := c.Register(context.Background(), samplePayload(), loadKey(t, k)); err == nil {
				t.Fatalf("status %+v after a 2xx was accepted", row)
			}
		})
	}
	f := stagedFake(k)
	c, _ := newFake(t, f)
	row, err := c.Register(context.Background(), samplePayload(), loadKey(t, k))
	if err != nil {
		t.Fatalf("keep-control: %v", err)
	}
	if row.ID != 7 || row.State != "staged" || f.calls.Load() != 4 {
		t.Fatalf("keep-control row %+v after %d calls, want row 7 staged after 4", row, f.calls.Load())
	}
	var env register.Envelope[register.Payload]
	if err := json.Unmarshal(f.lastRegister, &env); err != nil {
		t.Fatal(err)
	}
	if env.Payload.Nonce != "nonce-1" {
		t.Fatalf("the signed payload carries nonce %q, want the issued nonce-1", env.Payload.Nonce)
	}
}

func TestClient2xxNoRowIsError(t *testing.T) {
	k := writeSigningKey(t)
	f := stagedFake(k)
	f.statusStatus = http.StatusNotFound
	c, _ := newFake(t, f)
	_, err := c.Register(context.Background(), samplePayload(), loadKey(t, k))
	if !errors.Is(err, register.ErrNoRow) {
		t.Fatalf("status 404 after a 2xx: %v, want ErrNoRow", err)
	}
}

func TestSecretKeyNeverPrinted(t *testing.T) {
	k := writeSigningKey(t)
	key := loadKey(t, k)
	secrets := []string{
		k.line,
		base64.StdEncoding.EncodeToString(k.secret),
		base64.StdEncoding.EncodeToString(k.secret.Seed()),
		hex.EncodeToString(k.secret.Seed()),
		fmt.Sprint([]byte(k.secret.Seed())),
		string(k.secret.Seed()),
	}
	var out []string
	out = append(out, fmt.Sprintf("%v|%+v|%#v|%s", key, key, key, key))
	f := stagedFake(k)
	f.registerStatus = http.StatusForbidden
	c, _ := newFake(t, f)
	if _, err := c.Register(context.Background(), samplePayload(), key); err != nil {
		out = append(out, err.Error())
	}
	bad := filepath.Join(t.TempDir(), "bad.key")
	if err := os.WriteFile(bad, []byte("untrusted comment: x\n"+k.line[:len(k.line)-8]+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := register.LoadSigningKey(bad); err != nil {
		out = append(out, err.Error())
	} else {
		t.Fatal("a truncated key loaded")
	}
	if len(out) != 3 {
		t.Fatalf("collected %d outputs, want 3", len(out))
	}
	for _, o := range out {
		for _, s := range secrets {
			if s != "" && strings.Contains(o, s) {
				t.Fatalf("output %q carries secret key material", o)
			}
		}
		if strings.Contains(o, k.line[:20]) {
			t.Fatalf("output %q carries part of the key line", o)
		}
	}
}

func TestClient2xxUndecodableBodyReadsRowBack(t *testing.T) {
	k := writeSigningKey(t)
	f := stagedFake(k)
	f.registerBody = "<html>created</html>"
	c, _ := newFake(t, f)
	row, err := c.Register(context.Background(), samplePayload(), loadKey(t, k))
	if err != nil {
		t.Fatalf("a 201 with a body that does not decode failed before the read-back: %v", err)
	}
	if row.State != "staged" || row.Stamp != testStamp || f.calls.Load() != 4 {
		t.Fatalf("row %+v after %d calls, want staged %s after 4", row, f.calls.Load(), testStamp)
	}
	twin := stagedFake(k)
	twin.registerBody = "<html>created</html>"
	twin.statusStatus = http.StatusNotFound
	c, _ = newFake(t, twin)
	if _, err := c.Register(context.Background(), samplePayload(), loadKey(t, k)); !errors.Is(err, register.ErrNoRow) {
		t.Fatalf("undecodable 201 then status 404: %v, want ErrNoRow", err)
	}
}

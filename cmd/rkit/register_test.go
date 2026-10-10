package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const registerStamp = "v0.1.8.2026.09.20.7162a3f3"

func writeRegisterKey(t *testing.T) string {
	t.Helper()
	seed := sha256.Sum256([]byte("rkit register test-only key"))
	priv := ed25519.NewKeyFromSeed(seed[:])
	raw := make([]byte, 158)
	copy(raw[0:], "Ed")
	copy(raw[4:], "B2")
	copy(raw[62:], priv)
	path := filepath.Join(t.TempDir(), "release.key")
	body := "untrusted comment: test-only key\n" + base64.StdEncoding.EncodeToString(raw) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeReceipt(t *testing.T) string {
	t.Helper()
	base := "umbree/production/" + registerStamp + "/"
	sum := strings.Repeat("c", 64)
	receipt := map[string]any{
		"component": "umbree", "channel": "production", "stamp": registerStamp, "version": "0.1.8",
		"objects": []map[string]any{
			{"key": base + "umbree-linux-arm64.zip", "size": 9, "sha256": sum},
			{"key": base + "SHA256SUMS.txt", "size": 9, "sha256": sum},
			{"key": base + "SHA256SUMS.txt.minisig", "size": 9, "sha256": sum},
		},
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gated-receipt.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeManage(t *testing.T, registerCode int, statusState string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/releases/nonce", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"nonce":"n","expires_in":300}`)
	})
	mux.HandleFunc("POST /api/v1/releases/register", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(registerCode)
		fmt.Fprintf(w, `{"id":3,"state":"staged","stamp":%q,"version":"0.1.8"}`, registerStamp)
	})
	mux.HandleFunc("POST /api/v1/releases/status", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"id":3,"state":%q,"stamp":%q,"version":"0.1.8"}`, statusState, registerStamp)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRkitRegisterExitCodes(t *testing.T) {
	key, receipt := writeRegisterKey(t), writeReceipt(t)
	argsFor := func(url string, extra ...string) []string {
		args := []string{"register", "--manage-url", url, "--sign-key", key, "--receipt", receipt,
			"--component", "umbree", "--channel", "production", "--version", "0.1.8", "--stamp", registerStamp}
		return append(args, extra...)
	}
	cases := []struct {
		name     string
		srv      *httptest.Server
		args     func(url string) []string
		wantCode int
		wantErr  string
	}{
		{"keep-control", fakeManage(t, 201, "staged"), func(u string) []string { return argsFor(u) }, 0, ""},
		{"register 409", fakeManage(t, 409, "staged"), func(u string) []string { return argsFor(u) }, 1, "HTTP 409"},
		{"register 401", fakeManage(t, 401, "staged"), func(u string) []string { return argsFor(u) }, 1, "HTTP 401"},
		{"status says public", fakeManage(t, 201, "public"), func(u string) []string { return argsFor(u) }, 1, "public"},
		{"http url", fakeManage(t, 201, "staged"), func(u string) []string { return argsFor(strings.Replace(u, "https:", "http:", 1)) }, 1, "not https"},
		{"stamp disagrees with receipt", fakeManage(t, 201, "staged"), func(u string) []string {
			a := argsFor(u)
			a[len(a)-1] = "v0.1.9.2026.09.20.7162a3f3"
			return a
		}, 1, "receipt"},
		{"missing flag", fakeManage(t, 201, "staged"), func(u string) []string { return argsFor(u)[:len(argsFor(u))-2] }, 2, "--stamp"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(context.Background(), tc.args(tc.srv.URL), &out, &errOut, tc.srv.Client())
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stdout %q stderr %q", code, tc.wantCode, out.String(), errOut.String())
			}
			if tc.wantErr != "" && !strings.Contains(errOut.String(), tc.wantErr) {
				t.Fatalf("stderr %q does not say %q", errOut.String(), tc.wantErr)
			}
			if code == 0 && !strings.Contains(out.String(), "row 3 staged") {
				t.Fatalf("keep-control stdout %q", out.String())
			}
		})
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"bogus"}, &out, &errOut, nil); code != 2 || !strings.Contains(errOut.String(), "register") {
		t.Fatalf("unknown verb: exit %d stderr %q; usage must list register", code, errOut.String())
	}
}

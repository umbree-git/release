package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func missingRow(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/releases/nonce", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"nonce":"n","expires_in":300}`))
	})
	mux.HandleFunc("POST /api/v1/releases/status", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such release", http.StatusNotFound)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRkitStatusReadsTheRowBack(t *testing.T) {
	key := writeRegisterKey(t)
	argsFor := func(url string) []string {
		return []string{"status", "--manage-url", url, "--sign-key", key,
			"--component", "umbree", "--channel", "production", "--stamp", registerStamp}
	}
	cases := []struct {
		name     string
		srv      *httptest.Server
		args     func(url string) []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{"staged", fakeManage(t, 201, "staged"), argsFor, 0, "row 3 staged 0.1.8 " + registerStamp + "\n", ""},
		{"public is reported, not judged", fakeManage(t, 201, "public"), argsFor, 0, "row 3 public 0.1.8 " + registerStamp + "\n", ""},
		{"no row", missingRow(t), argsFor, 1, "", "no row"},
		{"http url", fakeManage(t, 201, "staged"), func(u string) []string { return argsFor(strings.Replace(u, "https:", "http:", 1)) }, 1, "", "not https"},
		{"missing flag", fakeManage(t, 201, "staged"), func(u string) []string { return argsFor(u)[:len(argsFor(u))-2] }, 2, "", "--stamp"},
		{"stray argument", fakeManage(t, 201, "staged"), func(u string) []string { return append(argsFor(u), "extra") }, 2, "", "unexpected argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(context.Background(), tc.args(tc.srv.URL), &out, &errOut, tc.srv.Client())
			if code != tc.wantCode || out.String() != tc.wantOut || !strings.Contains(errOut.String(), tc.wantErr) {
				t.Fatalf("exit %d stdout %q stderr %q; want exit %d stdout %q stderr containing %q",
					code, out.String(), errOut.String(), tc.wantCode, tc.wantOut, tc.wantErr)
			}
		})
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"bogus"}, &out, &errOut, nil); code != 2 || !strings.Contains(errOut.String(), "status") {
		t.Fatalf("unknown verb: exit %d stderr %q; usage must list status", code, errOut.String())
	}
}

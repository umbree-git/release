package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

func retainFixture(t *testing.T) (string, *backendtest.Store, *backendtest.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gated, public := backendtest.New("gated-cmd"), backendtest.New("public-cmd")
	for _, comp := range []string{"umbree", "umbreed"} {
		for n := 1; n <= 4; n++ {
			stamp := fmt.Sprintf("v0.1.%d.2026.10.01.%08x", n, n)
			base := comp + "/production/" + stamp + "/"
			arts := backendtest.SeedRelease(gated, base, map[string][]byte{comp + "-linux-amd64.zip": []byte(base)})
			body, _ := json.Marshal(arts)
			if _, err := s.InsertStaged(store.ReleaseVersion{Component: comp, Channel: "production", Version: fmt.Sprintf("0.1.%d", n),
				Stamp: stamp, ArtifactsJSON: string(body), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName,
				CreatedAt: time.Unix(int64(n), 0)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	newRetentionStores = func(o *options) (retention.Deleter, retention.PublicStore, error) { return gated, public, nil }
	t.Cleanup(func() { newRetentionStores = r2RetentionStores })
	return dir, gated, public
}

var retainVars = map[string]string{"UMBREE_R2_ACCOUNT": "acct", "UMBREE_R2_CREDS": "/nonexistent",
	"UMBREE_R2_GATED_BUCKET": "gated-cmd", "UMBREE_R2_BUCKET": "public-cmd"}

func states(t *testing.T, dir string) map[string]string {
	t.Helper()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out := map[string]string{}
	for _, comp := range []string{"umbree", "umbreed"} {
		rows, err := s.List(comp, "production")
		if err != nil {
			t.Fatal(err)
		}
		for _, rv := range rows {
			out[comp+" "+rv.Version] = rv.State
		}
	}
	return out
}

func invokeLive(t *testing.T, vars map[string]string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	e := &env{ctx: context.Background(), stdout: &out, stderr: &errOut, getenv: func(k string) string { return vars[k] }}
	code := run(e, args)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func TestRetainVerbAllComponents(t *testing.T) {
	dir, gated, _ := retainFixture(t)
	if r := invoke(t, map[string]string{"UMBREE_R2_ACCOUNT": "acct"}, "retain", "--data-dir", dir); r.code != exitUsage || !strings.Contains(r.stderr, "--r2-creds") {
		t.Fatalf("missing store flags: exit %d stderr %q", r.code, r.stderr)
	}
	if gated.Count("DELETE") != 0 {
		t.Fatal("a refused retain deleted something")
	}
	r := invokeLive(t, retainVars, "retain", "--data-dir", dir)
	if r.code != 0 {
		t.Fatalf("retain: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	got := states(t, dir)
	for _, comp := range []string{"umbree", "umbreed"} {
		if got[comp+" 0.1.1"] != "expired" || got[comp+" 0.1.2"] != "staged" {
			t.Fatalf("%s after retain: %v", comp, got)
		}
		for _, w := range []string{"gated", "public"} {
			if !strings.Contains(r.stdout, w+" "+comp+"/production") {
				t.Fatalf("stdout %q does not report the %s pass for %s", r.stdout, w, comp)
			}
		}
	}
	if gated.Count("DELETE") != 6 {
		t.Fatalf("retain issued %d gated DELETEs, want 3 per component", gated.Count("DELETE"))
	}
}

func TestRetainVerbDryRunDeletesNothing(t *testing.T) {
	dir, gated, public := retainFixture(t)
	before := states(t, dir)
	r := invokeLive(t, retainVars, "retain", "--data-dir", dir, "--dry-run")
	if r.code != 0 {
		t.Fatalf("retain --dry-run: exit %d stderr %q", r.code, r.stderr)
	}
	if n := gated.Count("DELETE") + public.Count("DELETE"); n != 0 {
		t.Fatalf("a dry run issued %d DELETEs", n)
	}
	got := states(t, dir)
	for k, v := range before {
		if got[k] != v {
			t.Fatalf("a dry run changed %s from %s to %s", k, v, got[k])
		}
	}
	for _, want := range []string{
		"DELETE gated umbree/production/v0.1.1.2026.10.01.00000001/umbree-linux-amd64.zip",
		"EXPIRE gated umbreed/production v0.1.1.2026.10.01.00000001",
		"plan ",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("dry-run stdout %q does not carry %q", r.stdout, want)
		}
	}
}

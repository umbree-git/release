package main

import (
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/backend/backendtest"
)

func TestBackfillVerbWiring(t *testing.T) {
	dir := t.TempDir()
	if r := invoke(t, nil, "backfill", "--data-dir", dir); r.code != exitUsage || !strings.Contains(r.stderr, "--component") {
		t.Fatalf("no component: exit %d stderr %q", r.code, r.stderr)
	}
	if r := invoke(t, nil, "backfill", "--data-dir", dir, "--component", "umbree"); r.code != exitUsage || !strings.Contains(r.stderr, "--public-bucket") {
		t.Fatalf("no public store: exit %d stderr %q", r.code, r.stderr)
	}
	fake := backendtest.New("public-fake")
	var asked options
	newPublicStore = func(o *options) (backend.Public, error) { asked = *o; return fake, nil }
	t.Cleanup(func() { newPublicStore = r2PublicStore })
	vars := map[string]string{"UMBREE_R2_BUCKET": "public-fake", "UMBREE_R2_ACCOUNT": "acct", "UMBREE_R2_CREDS": "/nonexistent"}
	r := invoke(t, vars, "backfill", "--data-dir", dir, "--component", "umbree")
	if r.code != 0 || !strings.Contains(r.stdout, "0 inserted") {
		t.Fatalf("empty public surface: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	if asked.publicBucket != "public-fake" || fake.Count("LIST") != 1 || fake.Count("PUT")+fake.Count("COPY") != 0 {
		t.Fatalf("backfill asked %+v and called %v", asked, fake.Calls())
	}
}

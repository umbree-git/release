package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/backend/backendtest"
)

const gatedControl = "/manage/retention/production/umbree/gated"

var (
	fingerprintRe = regexp.MustCompile(`name="fingerprint" value="([0-9a-f]{64})"`)
	plannedKeyRe  = regexp.MustCompile(`<code class="key">([^<]+)</code>`)
)

func (c *console) fourStaged() {
	c.t.Helper()
	for i, v := range []string{"0.0.1", "0.0.2", "0.0.3", "0.0.4"} {
		c.stage("r"+v, v, i+1)
	}
}

type preview struct {
	token, fingerprint string
	keys               []string
}

func (b *browser) preview(path string) preview {
	b.c.t.Helper()
	r := b.post(path, url.Values{auth.CSRFField: {b.csrf()}})
	tok, fp := confirmRe.FindStringSubmatch(r.body), fingerprintRe.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || tok == nil || fp == nil {
		b.c.t.Fatalf("preview %s: HTTP %d, no token or fingerprint in %q", path, r.status, r.body)
	}
	var keys []string
	for _, m := range plannedKeyRe.FindAllStringSubmatch(r.body, -1) {
		keys = append(keys, m[1])
	}
	slices.Sort(keys)
	return preview{token: tok[1], fingerprint: fp[1], keys: keys}
}

func (b *browser) confirmPlan(path string, p preview) reply {
	return b.post(path, url.Values{auth.CSRFField: {b.csrf()}, "confirm": {p.token}, "fingerprint": {p.fingerprint}})
}

func (c *console) deleted() []string {
	var out []string
	for _, s := range []*backendtest.Store{c.gated, c.public} {
		for _, call := range s.Calls() {
			if call.Op == "DELETE" {
				out = append(out, call.Key)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestRetentionPreviewNoDelete(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	b := c.signedIn("ops")
	if r := b.get("/manage/production/umbree"); !strings.Contains(r.body, `action="`+gatedControl+`"`) ||
		!strings.Contains(r.body, `action="/manage/retention/production/umbree/public"`) {
		t.Fatalf("the overview offers no retention controls: %s", r.body)
	}
	p := b.preview(gatedControl)
	oldest := "umbree/production/" + stampOf("0.0.1", 1) + "/"
	if len(p.keys) != 3 {
		t.Fatalf("the preview names %v, want the oldest row's 3 keys", p.keys)
	}
	for _, k := range p.keys {
		if !strings.HasPrefix(k, oldest) {
			t.Fatalf("the preview names %s, outside the oldest row", k)
		}
	}
	if got := c.deleted(); len(got) != 0 {
		t.Fatalf("the preview deleted %v", got)
	}
	if st, _ := c.state("r0.0.1"); st != "staged" {
		t.Fatalf("the preview changed the oldest row to %s", st)
	}
}

func TestRetentionConfirmDeletesExactlyPreviewed(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	b := c.signedIn("ops")
	p := b.preview(gatedControl)
	r := b.confirmPlan(gatedControl, p)
	if r.status != http.StatusOK {
		t.Fatalf("confirm: HTTP %d %s", r.status, r.body)
	}
	if got := c.deleted(); !slices.Equal(got, p.keys) {
		t.Fatalf("deleted %v, previewed %v", got, p.keys)
	}
	if st, _ := c.state("r0.0.1"); st != "expired" {
		t.Fatalf("the oldest staged row is %s after the confirm, want expired", st)
	}
	if st, _ := c.state("r0.0.2"); st != "staged" {
		t.Fatalf("keep-control: the next row is %s", st)
	}
}

func TestRetentionPlanChanged409(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	b := c.signedIn("ops")
	p := b.preview(gatedControl)
	c.stage("r0.0.5", "0.0.5", 5)
	r := b.confirmPlan(gatedControl, p)
	if r.status != http.StatusConflict || !strings.Contains(r.body, "plan changed") {
		t.Fatalf("confirm after the plan changed: HTTP %d %q, want 409 plan changed", r.status, r.body)
	}
	if got := c.deleted(); len(got) != 0 {
		t.Fatalf("a changed plan deleted %v", got)
	}
	again := b.preview(gatedControl)
	if len(again.keys) != 6 || again.fingerprint == p.fingerprint {
		t.Fatalf("control: a fresh preview names %v (%s)", again.keys, again.fingerprint)
	}
}

func TestRetentionRefusedWithoutSession(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	pending := c.browser()
	pending.passwordStep("ops")
	for who, b := range map[string]*browser{"anonymous": c.browser(), "password-only": pending} {
		for _, form := range []url.Values{
			{auth.CSRFField: {b.csrf()}},
			{auth.CSRFField: {b.csrf()}, "confirm": {"forged"}, "fingerprint": {strings.Repeat("0", 64)}},
		} {
			if r := b.post(gatedControl, form); r.status != http.StatusUnauthorized {
				t.Fatalf("%s: HTTP %d, want 401", who, r.status)
			}
		}
	}
	if got := c.deleted(); len(got) != 0 {
		t.Fatalf("an unauthenticated control deleted %v", got)
	}
	if p := c.signedIn("ops").preview(gatedControl); len(p.keys) == 0 {
		t.Fatal("control: a signed-in admin got an empty preview")
	}
}

func TestRetentionRefusedWithoutCSRF(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	b := c.signedIn("ops")
	p := b.preview(gatedControl)
	for name, form := range map[string]url.Values{
		"preview, no token":    {},
		"preview, wrong token": {auth.CSRFField: {"not-the-token"}},
		"confirm, no token":    {"confirm": {p.token}, "fingerprint": {p.fingerprint}},
		"confirm, wrong token": {"confirm": {p.token}, "fingerprint": {p.fingerprint}, auth.CSRFField: {"not-the-token"}},
	} {
		if r := b.post(gatedControl, form); r.status != http.StatusForbidden {
			t.Fatalf("%s: HTTP %d, want 403", name, r.status)
		}
	}
	if got := c.deleted(); len(got) != 0 {
		t.Fatalf("a control without CSRF deleted %v", got)
	}
	if r := b.confirmPlan(gatedControl, p); r.status != http.StatusOK {
		t.Fatalf("control: the same confirm with the CSRF token: HTTP %d", r.status)
	}
}

func TestRetentionConfirmReplayRefused(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	b := c.signedIn("ops")
	p := b.preview(gatedControl)
	forged := p
	forged.fingerprint = strings.Repeat("a", 64)
	if r := b.confirmPlan(gatedControl, forged); r.status != http.StatusForbidden {
		t.Fatalf("a token with another fingerprint: HTTP %d, want 403", r.status)
	}
	if r := b.confirmPlan("/manage/retention/production/umbree/public", p); r.status != http.StatusForbidden {
		t.Fatalf("a gated token on the public control: HTTP %d, want 403", r.status)
	}
	if r := b.confirmPlan(gatedControl, p); r.status != http.StatusOK {
		t.Fatalf("first confirm: HTTP %d", r.status)
	}
	before := len(c.deleted())
	c.stage("r0.0.5", "0.0.5", 5)
	if r := b.confirmPlan(gatedControl, p); r.status != http.StatusForbidden {
		t.Fatalf("a replayed confirm: HTTP %d, want 403", r.status)
	}
	if len(c.deleted()) != before {
		t.Fatal("a replayed confirm deleted something")
	}
}

func TestRetentionAuditRecordsAdmin(t *testing.T) {
	c := newConsole(t)
	c.fourStaged()
	seeded, _ := c.st.AuditLog()
	b := c.signedIn("ops")
	p := b.preview(gatedControl)
	if r := b.confirmPlan(gatedControl, p); r.status != http.StatusOK {
		t.Fatalf("confirm: HTTP %d", r.status)
	}
	got := consoleAudit(t, c, len(seeded))
	if len(got) != 2 {
		t.Fatalf("audit %+v, want a prune and an expire", got)
	}
	for i, action := range []string{"prune-gated", "expire"} {
		if got[i].Action != action || got[i].Actor != "ops" || got[i].RowID != c.rows["r0.0.1"] {
			t.Fatalf("audit[%d] = %+v, want %s by ops on the oldest row", i, got[i], action)
		}
	}
	for _, k := range p.keys {
		if !strings.Contains(got[0].Detail, k) {
			t.Fatalf("the prune audit %q does not name %s", got[0].Detail, k)
		}
	}
}

func TestHistoryHidesLinksOfPrunedPublicRow(t *testing.T) {
	c := newConsole(t)
	c.promoteDirect(c.stage("old", "0.0.1", 1))
	c.promoteDirect(c.stage("cur", "0.0.2", 1))
	b := c.signedIn("ops")
	oldLink := publicBase + "/umbree/" + stampOf("0.0.1", 1) + "/umbree-linux-amd64.zip"
	if r := b.get("/manage/production/umbree/history"); !strings.Contains(r.body, oldLink) {
		t.Fatalf("keep-control: the public row's link is missing before the prune")
	}
	if _, err := c.st.RecordPruned(c.rows["old"], "public", nil, "test-operator", c.now); err != nil {
		t.Fatal(err)
	}
	r := b.get("/manage/production/umbree/history")
	if strings.Contains(r.body, oldLink) {
		t.Fatal("the history links bytes the public pass deleted")
	}
	if !strings.Contains(r.body, publicBase+"/umbree/"+stampOf("0.0.2", 1)+"/") {
		t.Fatal("keep-control: the current row lost its links")
	}
}

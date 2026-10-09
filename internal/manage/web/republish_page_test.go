package web_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/publish"
)

const republishControl = "/manage/republish/production/umbree"

type countingStatic struct{ calls []string }

func (s *countingStatic) Publish(_ context.Context, component string) (string, error) {
	s.calls = append(s.calls, component)
	return "republished " + component + " v9.9.9.2026.10.09.00000000", nil
}

func TestRepublishControlRefusedWithoutSessionCSRFConfirm(t *testing.T) {
	fake := &countingStatic{}
	c := newConsole(t, func(d *publish.Deps) { d.Static = fake })
	if r := c.browser().post(republishControl, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no session: HTTP %d", r.status)
	}
	b := c.signedIn("ops")
	if r := b.get("/manage/production/umbree"); !strings.Contains(r.body, `action="`+republishControl+`"`) {
		t.Fatalf("the overview offers no republish control: %s", r.body)
	}
	if r := b.post(republishControl, url.Values{}); r.status != http.StatusForbidden {
		t.Fatalf("no CSRF token: HTTP %d", r.status)
	}
	token := b.confirmToken(republishControl)
	if len(fake.calls) != 0 {
		t.Fatalf("the preview republished: %v", fake.calls)
	}
	if r := b.confirmed(republishControl, token+"x"); r.status != http.StatusForbidden {
		t.Fatalf("a forged confirm token: HTTP %d", r.status)
	}
	if r := b.post(republishControl, url.Values{"confirm": {token}}); r.status != http.StatusForbidden {
		t.Fatalf("a confirm without the CSRF token: HTTP %d", r.status)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("a refused request republished: %v", fake.calls)
	}
	r := b.confirmed(republishControl, token)
	if r.status != http.StatusOK || !strings.Contains(r.body, "republished umbree") {
		t.Fatalf("confirm: HTTP %d %s", r.status, r.body)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "umbree" {
		t.Fatalf("republished %v, want umbree once", fake.calls)
	}
	if r := b.confirmed(republishControl, token); r.status != http.StatusForbidden || len(fake.calls) != 1 {
		t.Fatalf("a replayed confirm: HTTP %d, calls %v", r.status, fake.calls)
	}
	other := c.signedIn("ops")
	if r := other.confirmed(republishControl, b.confirmToken(republishControl)); r.status != http.StatusForbidden {
		t.Fatalf("another session's confirm token: HTTP %d", r.status)
	}
	if r := b.post("/manage/republish/beta/umbree", url.Values{auth.CSRFField: {b.csrf()}}); r.status != http.StatusNotFound {
		t.Fatalf("a held channel: HTTP %d", r.status)
	}
}

func TestRepublishControlAbsentWithoutPublisher(t *testing.T) {
	c := newConsole(t)
	b := c.signedIn("ops")
	if r := b.get("/manage/production/umbree"); strings.Contains(r.body, republishControl) {
		t.Fatal("the overview offers a republish control with no publisher configured")
	}
	if r := b.post(republishControl, url.Values{auth.CSRFField: {b.csrf()}}); r.status != http.StatusNotFound {
		t.Fatalf("no publisher: HTTP %d", r.status)
	}
}

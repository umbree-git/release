package intake_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/register"
)

var invalidRegistrations = []struct {
	name   string
	mutate func(*register.Payload)
	want   string
}{
	{"bad stamp shape", func(p *register.Payload) { p.Stamp = "v0.1.8" }, "not a production stamp"},
	{"beta stamp", func(p *register.Payload) { p.Stamp = "v0.2.0.beta.2026.09.05.deadbeef" }, "not a production stamp"},
	{"unknown component", func(p *register.Payload) { p.Component = "burrowee" }, "unknown component"},
	{"beta channel held", func(p *register.Payload) { p.Channel = "beta" }, `channel "beta" is held`},
	{"stable channel", func(p *register.Payload) { p.Channel = "stable" }, "unknown channel"},
	{"key outside the stamp", func(p *register.Payload) {
		p.Artifacts[0].Key = "umbree/production/v0.1.7.2026.09.01.00000000/umbree-darwin-arm64.zip"
	}, "is not under"},
	{"key in another component", func(p *register.Payload) {
		p.Artifacts[0].Key = "umbreed/production/" + testStamp + "/umbreed-linux-arm64.zip"
	}, "is not under"},
	{"nested key", func(p *register.Payload) { p.Artifacts[0].Key = testBase + "x/y.zip" }, "is not under"},
	{"manifest key", func(p *register.Payload) { p.Artifacts[0].Key = testBase + "latest.json" }, "is not under"},
	{"missing sums", func(p *register.Payload) { p.Artifacts = append(p.Artifacts[:2], p.Artifacts[3]) }, "sums key"},
	{"missing minisig", func(p *register.Payload) { p.Artifacts = p.Artifacts[:3] }, "signature key"},
	{"sums key elsewhere", func(p *register.Payload) { p.SumsKey = testBase + "umbree-darwin-arm64.zip" }, "sums key"},
	{"empty artifact list", func(p *register.Payload) { p.Artifacts = nil }, "lists no artifacts"},
	{"zero size", func(p *register.Payload) { p.Artifacts[0].Size = 0 }, "has size 0"},
	{"bad sha256", func(p *register.Payload) { p.Artifacts[0].SHA256 = "abc" }, "sha256"},
	{"duplicate key", func(p *register.Payload) { p.Artifacts[1] = p.Artifacts[0] }, "listed twice"},
	{"version mismatch", func(p *register.Payload) { p.Version = "0.1.9" }, "does not match stamp"},
}

func TestValidation422(t *testing.T) {
	f := newFixture(t)
	for _, tc := range invalidRegistrations {
		t.Run(tc.name, func(t *testing.T) {
			p := payload(f.issueNonce())
			tc.mutate(&p)
			code, body := f.register(p)
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("HTTP %d %s, want 422", code, body)
			}
			if !strings.Contains(body, tc.want) {
				t.Fatalf("body %s does not say %q", body, tc.want)
			}
		})
	}
	t.Run("oversize body", func(t *testing.T) {
		p := payload(f.issueNonce())
		p.Version = strings.Repeat("9", intake.MaxBodyBytes)
		body, err := json.Marshal(register.Envelope[register.Payload]{Payload: p, Sig: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if code, resp := f.post("/api/v1/releases/register", body); code != http.StatusUnprocessableEntity {
			t.Fatalf("oversize body: HTTP %d %s, want 422", code, resp)
		}
	})
	if f.rowCount() != 0 {
		t.Fatalf("invalid registrations created %d rows", f.rowCount())
	}
	if code, body := f.register(payload(f.issueNonce())); code != http.StatusCreated {
		t.Fatalf("keep-control: HTTP %d %s", code, body)
	}
}

func (f *fixture) status(q register.StatusQuery) (int, string) {
	f.t.Helper()
	return f.post("/api/v1/releases/status", envelope(f.t, f.priv, q))
}

func TestStatusReturnsRow(t *testing.T) {
	f := newFixture(t)
	code, body := f.register(payload(f.issueNonce()))
	if code != http.StatusCreated {
		t.Fatalf("register: HTTP %d %s", code, body)
	}
	var created register.RowStatus
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	code, body = f.status(register.StatusQuery{Nonce: f.issueNonce(), Component: "umbree", Channel: "production", Stamp: testStamp})
	if code != http.StatusOK {
		t.Fatalf("status: HTTP %d %s", code, body)
	}
	var got register.RowStatus
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	want := register.RowStatus{ID: created.ID, State: "staged", Stamp: testStamp, Version: "0.1.8"}
	if got != want {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
}

func TestStatusUnknownRow404(t *testing.T) {
	f := newFixture(t)
	code, body := f.status(register.StatusQuery{Nonce: f.issueNonce(), Component: "umbreed", Channel: "production", Stamp: testStamp})
	if code != http.StatusNotFound {
		t.Fatalf("unknown row: HTTP %d %s, want 404", code, body)
	}
}

func TestStatusUnsignedRefused(t *testing.T) {
	f := newFixture(t)
	if code, body := f.register(payload(f.issueNonce())); code != http.StatusCreated {
		t.Fatalf("register: HTTP %d %s", code, body)
	}
	q := register.StatusQuery{Nonce: f.issueNonce(), Component: "umbree", Channel: "production", Stamp: testStamp}
	unsigned, err := json.Marshal(register.Envelope[register.StatusQuery]{Payload: q})
	if err != nil {
		t.Fatal(err)
	}
	code, body := f.post("/api/v1/releases/status", unsigned)
	if code != http.StatusForbidden || body != refusedBody {
		t.Fatalf("unsigned status: HTTP %d %q, want 403 %q", code, body, refusedBody)
	}
	code, body = f.post("/api/v1/releases/status", envelope(t, keyFromSeed("another key"), q))
	if code != http.StatusForbidden || body != refusedBody {
		t.Fatalf("wrong-key status: HTTP %d %q, want 403", code, body)
	}
	regSig := envelope(t, f.priv, payload(q.Nonce))
	var env struct {
		Sig string `json:"sig"`
	}
	if err := json.Unmarshal(regSig, &env); err != nil {
		t.Fatal(err)
	}
	crossed, err := json.Marshal(register.Envelope[register.StatusQuery]{Payload: q, Sig: env.Sig})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := f.post("/api/v1/releases/status", crossed); code != http.StatusForbidden {
		t.Fatalf("a register signature reused on status: HTTP %d, want 403", code)
	}
	if code, body := f.status(q); code != http.StatusOK {
		t.Fatalf("keep-control: HTTP %d %s", code, body)
	}
}

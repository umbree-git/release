package intake_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/retention"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

func olderRow(t *testing.T, st *store.Store, gated *backendtest.Store, n int) string {
	t.Helper()
	stamp := fmt.Sprintf("v0.1.%d.2026.09.01.%08x", n, n)
	base := "umbree/production/" + stamp + "/"
	arts := backendtest.SeedRelease(gated, base, map[string][]byte{"umbree-linux-amd64.zip": []byte("zip " + stamp)})
	body, err := json.Marshal(arts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertStaged(store.ReleaseVersion{Component: "umbree", Channel: "production", Version: fmt.Sprintf("0.1.%d", n),
		Stamp: stamp, ArtifactsJSON: string(body), SumsKey: base + register.SumsName, MinisigKey: base + register.MinisigName,
		CreatedAt: epoch}); err != nil {
		t.Fatal(err)
	}
	return stamp
}

func (f *fixture) registered(p register.Payload) register.RowStatus {
	t := f.t
	t.Helper()
	code, body := f.register(p)
	if code != http.StatusCreated {
		t.Fatalf("register: HTTP %d %s, want 201", code, body)
	}
	var rs register.RowStatus
	if err := json.Unmarshal([]byte(body), &rs); err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestRegisterRunsGatedPass(t *testing.T) {
	f := newFixture(t)
	gated := backendtest.New("gated-intake")
	oldest := olderRow(t, f.st, gated, 1)
	olderRow(t, f.st, gated, 2)
	olderRow(t, f.st, gated, 3)
	r := &retention.Retainer{Store: f.st, Gated: gated, Locks: publish.NewLocks(publish.DefaultLockWait), Now: f.clock}
	f.handler.RetainAfterStage(r.AfterStage, intake.RetentionBudget)
	rs := f.registered(payload(f.issueNonce()))
	if _, err := f.st.Get(rs.ID); err != nil {
		t.Fatalf("the registered row is missing: %v", err)
	}
	row, err := f.st.ByStamp("umbree", "production", oldest)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != "expired" || row.GatedPrunedAt.IsZero() {
		t.Fatalf("the oldest staged row is %+v after a fourth registration, want expired", row)
	}
	keys, _ := gated.List(context.Background(), "umbree/production/"+oldest+"/")
	if len(keys) != 0 {
		t.Fatalf("its gated keys survive: %v", keys)
	}
	if !strings.Contains(rs.Retention, "gated") || !strings.Contains(rs.Retention, "1 expired") {
		t.Fatalf("the 201 body reports retention %q, want the gated pass's summary", rs.Retention)
	}
	if gated.Count("DELETE") != 3 {
		t.Fatalf("the gated pass issued %d DELETEs, want the oldest row's 3", gated.Count("DELETE"))
	}
}

func TestRegisterRetentionFailureStill2xx(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.handler.RetainAfterStage(func(ctx context.Context, component, channel string) (string, error) {
		calls++
		if n := f.rowCount(); n != 1 {
			t.Errorf("the pass ran with %d rows; it runs after the row commits", n)
		}
		return "", errors.New("gated store unreachable")
	}, intake.RetentionBudget)
	rs := f.registered(payload(f.issueNonce()))
	if calls != 1 || rs.State != "staged" || rs.ID == 0 {
		t.Fatalf("calls %d, row %+v; want one pass and the staged row", calls, rs)
	}
	if !strings.Contains(rs.Retention, "gated store unreachable") {
		t.Fatalf("retention field %q does not carry the failure", rs.Retention)
	}
	if !strings.Contains(f.log.String(), "gated store unreachable") {
		t.Fatal("the failure was not logged")
	}
}

func TestRegisterRetentionBudget(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.handler.RetainAfterStage(func(ctx context.Context, component, channel string) (string, error) {
		<-release
		return "finished late", nil
	}, 50*time.Millisecond)
	start := time.Now()
	rs := f.registered(payload(f.issueNonce()))
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the response took %v with a 50ms retention budget", took)
	}
	if !strings.Contains(rs.Retention, "did not finish") {
		t.Fatalf("retention field %q does not report the budget", rs.Retention)
	}
	if _, err := f.st.Get(rs.ID); err != nil {
		t.Fatalf("the row is missing: %v", err)
	}
}

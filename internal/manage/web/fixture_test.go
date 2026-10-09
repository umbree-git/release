package web_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/backend/backendtest"
	"github.com/umbree-git/release/internal/manage/intake"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/manage/totp"
	"github.com/umbree-git/release/internal/manage/web"
	"github.com/umbree-git/release/internal/register"
)

const (
	adminPassword = "correct horse battery"
	publicBase    = "https://downloads.example.test"
)

type console struct {
	t       *testing.T
	st      *store.Store
	svc     *auth.Service
	srv     *httptest.Server
	gated   *backendtest.Store
	public  *backendtest.Store
	deps    publish.Deps
	now     time.Time
	secrets map[string]string
	rows    map[string]int64
}

func newConsole(t *testing.T) *console {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := &console{t: t, st: st, now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC), secrets: map[string]string{}, rows: map[string]int64{}}
	clock := func() time.Time { return c.now }
	c.svc = auth.New(st, sealerIn(t, dir), clock, nil)
	c.gated, c.public = backendtest.New("gated-console"), backendtest.New("public-console")
	c.public.Link(c.gated)
	c.deps = publish.Deps{Store: st, Gated: c.gated, Public: c.public, Key: backendtest.ReleaseKey(),
		Locks: publish.NewLocks(publish.DefaultLockWait), Now: clock}
	key, err := intake.ReleaseKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := web.New(web.Config{Store: st, Auth: c.svc, Intake: intake.New(st, key, clock, nil),
		Publish: c.deps, PublicBaseURL: publicBase, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	c.srv = httptest.NewServer(s.Handler())
	t.Cleanup(c.srv.Close)
	c.addAdmin("ops")
	return c
}

func sealerIn(t *testing.T, dir string) *auth.Sealer {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "totp.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.LoadSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	return sealer
}

func (c *console) addAdmin(name string) {
	c.t.Helper()
	enrol, err := c.svc.AddAdmin(name, adminPassword)
	if err != nil {
		c.t.Fatal(err)
	}
	c.secrets[name] = enrol.Secret
}

func stampOf(version string, n int) string { return fmt.Sprintf("v%s.2026.10.08.%08x", version, n) }

func (c *console) stage(label, version string, n int) int64 {
	c.t.Helper()
	stamp := stampOf(version, n)
	base := "umbree/production/" + stamp + "/"
	arts := backendtest.SeedRelease(c.gated, base, map[string][]byte{
		"umbree-darwin-arm64.zip": []byte("zip a " + stamp), "umbree-linux-amd64.zip": []byte("zip b " + stamp)})
	body, _ := json.Marshal(arts)
	id, err := c.st.InsertStaged(store.ReleaseVersion{Component: "umbree", Channel: "production", Version: version,
		Stamp: stamp, ArtifactsJSON: string(body), SumsKey: base + register.SumsName,
		MinisigKey: base + register.MinisigName, CreatedAt: c.now})
	if err != nil {
		c.t.Fatal(err)
	}
	c.rows[label] = id
	return id
}

func (c *console) promoteDirect(id int64) {
	c.t.Helper()
	if err := publish.Promote(c.t.Context(), c.deps, id, "seed", io.Discard); err != nil {
		c.t.Fatalf("promote row %d: %v", id, err)
	}
}

func (c *console) seedHistory() {
	c.t.Helper()
	c.stage("new", "4.4.4", 1)
	c.promoteDirect(c.stage("expired", "1.1.1", 1))
	c.promoteDirect(c.stage("public", "2.2.2", 1))
	c.stage("below", "2.5.5", 1)
	c.promoteDirect(c.stage("current", "3.3.3", 2))
	c.stage("equal", "3.3.3", 1)
	if err := c.st.Transition(c.rows["expired"], "public", "expired", c.now); err != nil {
		c.t.Fatal(err)
	}
}

func (c *console) row(label string) *store.ReleaseVersion {
	c.t.Helper()
	rv, err := c.st.Get(c.rows[label])
	if err != nil {
		c.t.Fatal(err)
	}
	return rv
}

type browser struct {
	c       *console
	cookies map[string]string
}

func (c *console) browser() *browser { return &browser{c: c, cookies: map[string]string{}} }

type reply struct {
	status int
	header http.Header
	body   string
	set    []*http.Cookie
}

func (b *browser) do(method, path string, form url.Values, header map[string]string) reply {
	b.c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(b.c.t.Context(), method, b.c.srv.URL+path, body)
	if err != nil {
		b.c.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	for k, v := range b.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		b.c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	for _, ck := range resp.Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(b.cookies, ck.Name)
		} else {
			b.cookies[ck.Name] = ck.Value
		}
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: string(raw), set: resp.Cookies()}
}

func (b *browser) get(path string) reply { return b.do(http.MethodGet, path, nil, nil) }

func (b *browser) post(path string, form url.Values) reply {
	if form == nil {
		form = url.Values{}
	}
	return b.do(http.MethodPost, path, form, nil)
}

func (b *browser) csrf() string { return b.cookies[auth.CSRFCookie] }

func (b *browser) passwordStep(name string) reply {
	b.c.t.Helper()
	r := b.post("/manage/login", url.Values{"name": {name}, "password": {adminPassword}})
	if r.status != http.StatusOK {
		b.c.t.Fatalf("password step for %s: HTTP %d %s", name, r.status, r.body)
	}
	return r
}

func (c *console) signedIn(name string) *browser {
	c.t.Helper()
	b := c.browser()
	b.passwordStep(name)
	code, err := totp.Code(c.secrets[name], c.now)
	if err != nil {
		c.t.Fatal(err)
	}
	r := b.post("/manage/login/totp", url.Values{"code": {code}, auth.CSRFField: {b.csrf()}})
	if r.status != http.StatusSeeOther {
		c.t.Fatalf("code step for %s: HTTP %d %s", name, r.status, r.body)
	}
	return b
}

var confirmRe = regexp.MustCompile(`name="confirm" value="([^"]+)"`)

func (b *browser) confirmToken(path string) string {
	b.c.t.Helper()
	r := b.post(path, url.Values{auth.CSRFField: {b.csrf()}})
	m := confirmRe.FindStringSubmatch(r.body)
	if r.status != http.StatusOK || m == nil {
		b.c.t.Fatalf("first POST %s: HTTP %d, no confirm token in %q", path, r.status, r.body)
	}
	return m[1]
}

func (b *browser) confirmed(path, token string) reply {
	return b.post(path, url.Values{auth.CSRFField: {b.csrf()}, "confirm": {token}})
}

func actionPath(id int64, action string) string {
	return fmt.Sprintf("/manage/releases/%d/%s", id, action)
}

func apiPath(id int64, action string) string {
	return fmt.Sprintf("/manage/api/releases/%d/%s", id, action)
}

func (c *console) state(label string) (string, bool) {
	rv := c.row(label)
	return rv.State, rv.IsCurrent
}

func (c *console) writes() int { return c.public.Count("PUT") + c.public.Count("COPY") }

func contains(body string, needles ...string) string {
	for _, n := range needles {
		if bytes.Contains([]byte(body), []byte(n)) {
			return n
		}
	}
	return ""
}

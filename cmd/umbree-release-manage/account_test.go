package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/store"
)

const testPassword = "correct horse battery"

func secretKeyFile(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "totp.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func serveVars(t *testing.T) map[string]string {
	t.Helper()
	creds := filepath.Join(t.TempDir(), "r2.creds")
	if err := os.WriteFile(creds, []byte("access_key_id=AKIDTEST\nsecret_access_key=SECRETTEST\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		"UMBREE_MANAGE_SECRET_KEY": secretKeyFile(t), "UMBREE_R2_ACCOUNT": "acct", "UMBREE_R2_CREDS": creds,
		"UMBREE_R2_GATED_BUCKET": "gated-private", "UMBREE_R2_BUCKET": "downloads",
		"UMBREE_PUBLIC_BASE_URL": "https://downloads.example.test",
	}
}

func invokeIn(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	e := &env{ctx: ctx, stdin: strings.NewReader(stdin), stdout: &out, stderr: &errOut, getenv: func(string) string { return "" }}
	code := run(e, args)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

func addAdmin(t *testing.T, dir, key, name string) result {
	t.Helper()
	return invokeIn(t, testPassword+"\n", "admin", "add", name, "--data-dir", dir, "--secret-key", key, "--password-stdin")
}

func TestAdminAddPrintsEnrolmentOnce(t *testing.T) {
	dir, key := t.TempDir(), secretKeyFile(t)
	r := addAdmin(t, dir, key, "ops")
	if r.code != 0 || strings.Count(r.stdout, "otpauth://totp/") != 1 {
		t.Fatalf("admin add: exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	secret := r.stdout[strings.Index(r.stdout, "secret=")+len("secret="):]
	secret = secret[:strings.IndexAny(secret, "&\n")]
	for _, again := range []result{
		invokeIn(t, "", "admin", "list", "--data-dir", dir),
		addAdmin(t, dir, key, "ops"),
	} {
		if strings.Contains(again.stdout+again.stderr, secret) || strings.Contains(again.stdout, "otpauth://") {
			t.Fatalf("the secret was shown again: %q %q", again.stdout, again.stderr)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, store.DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte(testPassword)) {
		t.Fatal("the catalog holds the TOTP secret or the password in the clear")
	}
	if r := invokeIn(t, testPassword+"\n", "admin", "add", "ops2", "--data-dir", dir, "--password-stdin"); r.code != exitUsage || !strings.Contains(r.stderr, "--secret-key") {
		t.Fatalf("no secret key: exit %d stderr %q", r.code, r.stderr)
	}
}

func TestAdminAddDuplicateRefused(t *testing.T) {
	dir, key := t.TempDir(), secretKeyFile(t)
	if r := addAdmin(t, dir, key, "ops"); r.code != 0 {
		t.Fatalf("first add: exit %d %q", r.code, r.stderr)
	}
	r := addAdmin(t, dir, key, "ops")
	if r.code != 1 || !strings.Contains(r.stderr, "already exists") {
		t.Fatalf("second add: exit %d stderr %q", r.code, r.stderr)
	}
	if r := invokeIn(t, "short\n", "admin", "add", "ops3", "--data-dir", dir, "--secret-key", key, "--password-stdin"); r.code == 0 {
		t.Fatal("a short password was accepted")
	}
	if r := invokeIn(t, testPassword+"\n", "admin", "add", "Ops!", "--data-dir", dir, "--secret-key", key, "--password-stdin"); r.code != exitUsage {
		t.Fatalf("a bad name: exit %d", r.code)
	}
}

func TestAdminRemoveEndsSessions(t *testing.T) {
	dir, key := t.TempDir(), secretKeyFile(t)
	addAdmin(t, dir, key, "ops")
	addAdmin(t, dir, key, "keep")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for id, admin := range map[string]string{"s-ops": "ops", "s-keep": "keep"} {
		if err := st.CreateSession(store.Session{ID: id, Admin: admin, MFAOK: true, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	if r := invokeIn(t, "", "admin", "remove", "ops", "--data-dir", dir); r.code != 0 {
		t.Fatalf("remove: exit %d %q", r.code, r.stderr)
	}
	if _, err := st.Session("s-ops", now); err == nil {
		t.Fatal("the removed admin's session survived")
	}
	if _, err := st.Session("s-keep", now); err != nil {
		t.Fatalf("keep-control, the other admin's session: %v", err)
	}
	if r := invokeIn(t, "", "admin", "remove", "ops", "--data-dir", dir); r.code != 1 || !strings.Contains(r.stderr, "no admin") {
		t.Fatalf("removing twice: exit %d %q", r.code, r.stderr)
	}
	r := invokeIn(t, "", "admin", "reset-totp", "keep", "--data-dir", dir, "--secret-key", key)
	if r.code != 0 || strings.Count(r.stdout, "otpauth://totp/") != 1 {
		t.Fatalf("reset-totp: exit %d %q %q", r.code, r.stdout, r.stderr)
	}
	if _, err := st.Session("s-keep", now); err == nil {
		t.Fatal("reset-totp left the admin's session alive")
	}
}

func TestAdminListNoSecrets(t *testing.T) {
	dir, key := t.TempDir(), secretKeyFile(t)
	added := addAdmin(t, dir, key, "ops")
	r := invokeIn(t, "", "admin", "list", "--data-dir", dir)
	if r.code != 0 || !strings.Contains(r.stdout, "ops") {
		t.Fatalf("list: exit %d %q", r.code, r.stdout)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.Admin("ops")
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"$argon2id", a.PasswordHash, string(a.TOTPSecretEnc), "otpauth", testPassword} {
		if strings.Contains(r.stdout, secret) {
			t.Fatalf("list shows %q:\n%s", secret, r.stdout)
		}
	}
	if !strings.Contains(added.stdout, "otpauth://") {
		t.Fatal("control: add showed no enrolment")
	}
}

func TestNoSignupRoute(t *testing.T) {
	dir := t.TempDir()
	vars := serveVars(t)
	o := &options{dataDir: dir, secretKey: vars["UMBREE_MANAGE_SECRET_KEY"], r2Account: "acct", r2Creds: vars["UMBREE_R2_CREDS"],
		gatedBucket: "gated-private", publicBucket: "downloads", publicBaseURL: vars["UMBREE_PUBLIC_BASE_URL"]}
	h, st, err := buildService(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(h)
	defer srv.Close()
	form := url.Values{"name": {"intruder"}, "password": {testPassword}}
	for _, p := range []string{"/manage/signup", "/manage/register", "/manage/admins", "/manage/admin/add", "/manage/users", "/signup", "/api/v1/admins", "/api/v1/manage/admins"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			req, _ := http.NewRequest(method, srv.URL+p, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode < 300 {
				t.Fatalf("%s %s: HTTP %d", method, p, resp.StatusCode)
			}
		}
	}
	if admins, err := st.ListAdmins(); err != nil || len(admins) != 0 {
		t.Fatalf("admins after the probe: %v %v", admins, err)
	}
	resp, err := http.Get(srv.URL + "/manage/login")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("control, the sign-in page: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestServeRefusesBadSecretKey(t *testing.T) {
	good := serveVars(t)
	loose := filepath.Join(t.TempDir(), "loose.key")
	if err := os.WriteFile(loose, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, key := range map[string]string{
		"missing": "", "absent": filepath.Join(t.TempDir(), "absent.key"), "relative": "totp.key",
		"unclean":     filepath.Dir(good["UMBREE_MANAGE_SECRET_KEY"]) + "/../" + filepath.Base(filepath.Dir(good["UMBREE_MANAGE_SECRET_KEY"])) + "/totp.key",
		"a directory": t.TempDir(), "group-readable": loose,
	} {
		vars := serveVars(t)
		vars["UMBREE_MANAGE_SECRET_KEY"] = key
		dir := t.TempDir()
		r := invoke(t, vars, "serve", "--data-dir", dir, "--listen", "127.0.0.1:0")
		if r.code == 0 || strings.Contains(r.stdout, "listening") || !strings.Contains(r.stderr, "secret") {
			t.Fatalf("%s key %q: exit %d stdout %q stderr %q", name, key, r.code, r.stdout, r.stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, store.DBFile)); !os.IsNotExist(err) {
			t.Fatalf("%s key: the refused serve opened the catalog", name)
		}
	}
	if r := invoke(t, good, "serve", "--data-dir", t.TempDir(), "--listen", "127.0.0.1:0"); r.code != 0 {
		t.Fatalf("control: exit %d %q", r.code, r.stderr)
	}
	if _, err := auth.LoadSealer(good["UMBREE_MANAGE_SECRET_KEY"]); err != nil {
		t.Fatalf("control key: %v", err)
	}
}

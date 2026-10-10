package web_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/umbree-git/release/internal/manage/auth"
	"github.com/umbree-git/release/internal/manage/store"
)

func consoleAudit(t *testing.T, c *console, seeded int) []store.AuditEntry {
	t.Helper()
	log, err := c.st.AuditLog()
	if err != nil {
		t.Fatal(err)
	}
	return log[seeded:]
}

func TestConsoleActionsAuditedWithAdmin(t *testing.T) {
	for _, viaAPI := range []bool{false, true} {
		c := newConsole(t)
		c.seedHistory()
		seeded, _ := c.st.AuditLog()
		b := c.signedIn("ops")
		act := func(id int64, action string) {
			path := actionPath(id, action)
			token := b.confirmToken(path)
			if viaAPI {
				path = apiPath(id, action)
			}
			if r := b.do(http.MethodPost, path, url.Values{"confirm": {token}}, map[string]string{auth.CSRFHeader: b.csrf()}); r.status != http.StatusOK {
				t.Fatalf("%s row %d (api %v): HTTP %d", action, id, viaAPI, r.status)
			}
		}
		act(c.rows["below"], "promote")
		if got := consoleAudit(t, c, len(seeded)); len(got) != 0 {
			t.Fatalf("a refused promote (api %v) was audited: %+v", viaAPI, got)
		}
		act(c.rows["new"], "promote")
		act(c.rows["new"], "yank")
		got := consoleAudit(t, c, len(seeded))
		if len(got) != 2 {
			t.Fatalf("api %v: audit %+v, want a promote and a yank", viaAPI, got)
		}
		for i, action := range []string{"promote", "yank"} {
			if got[i].Action != action || got[i].Actor != "ops" || got[i].RowID != c.rows["new"] {
				t.Fatalf("api %v: audit[%d] = %+v, want %s by ops on row %d", viaAPI, i, got[i], action, c.rows["new"])
			}
		}
	}
}

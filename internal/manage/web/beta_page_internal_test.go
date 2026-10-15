package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/umbree-git/release/internal/manage/store"
)

type betaRecord struct {
	built    []string
	template string
	status   int
	data     any
}

func (b *betaRecord) build(_ http.ResponseWriter, _ *http.Request, _ *store.Session, title, channel, comp string) pageData {
	b.built = []string{title, channel, comp}
	return pageData{Title: title}
}

func (b *betaRecord) render(_ http.ResponseWriter, _ *http.Request, name string, status int, data any) {
	b.template, b.status, b.data = name, status, data
}

func callBeta(path, comp string) (*betaRecord, *httptest.ResponseRecorder) {
	rec := &betaRecord{}
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.SetPathValue("component", comp)
	w := httptest.NewRecorder()
	betaPage(rec.build, rec.render)(w, r, &store.Session{Admin: "ops"})
	return rec, w
}

func TestBetaPageIsStoreFree(t *testing.T) {
	for path, history := range map[string]bool{"/manage/beta/umbree": false, "/manage/beta/umbree/history": true} {
		rec, _ := callBeta(path, "umbree")
		if rec.template != "beta_empty" || rec.status != http.StatusOK {
			t.Fatalf("%s: rendered %q with %d", path, rec.template, rec.status)
		}
		view, ok := rec.data.(betaView)
		if !ok || view.Component != "umbree" || view.Channel != channelBeta || view.History != history || len(view.Channels) != 2 || !view.Channels[1].Active {
			t.Fatalf("%s: view %+v", path, rec.data)
		}
		if len(rec.built) != 3 || rec.built[1] != channelBeta || rec.built[2] != "umbree" {
			t.Fatalf("%s: built %v", path, rec.built)
		}
	}
	rec, w := callBeta("/manage/beta/nope", "nope")
	if w.Code != http.StatusNotFound || rec.template != "" || rec.built != nil {
		t.Fatalf("unknown component: HTTP %d, rendered %q, built %v", w.Code, rec.template, rec.built)
	}
}

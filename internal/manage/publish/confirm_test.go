package publish_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/umbree-git/release/internal/manage/publish"
)

type fakeFetcher struct {
	status int
	body   []byte
	err    error
	urls   []string
}

func (f *fakeFetcher) Get(ctx context.Context, url string) (int, []byte, error) {
	f.urls = append(f.urls, url)
	return f.status, f.body, f.err
}

func TestPromoteConfirmsFromPublicManifest(t *testing.T) {
	cases := []struct {
		name   string
		fetch  func(w *world) *fakeFetcher
		status string
	}{
		{"live", func(w *world) *fakeFetcher {
			b, _ := w.public.Body("umbree/latest.json")
			return &fakeFetcher{status: 200, body: b}
		}, "live"},
		{"cached old manifest", func(w *world) *fakeFetcher {
			return &fakeFetcher{status: 200, body: []byte(`{"stamp":"v0.0.1.2026.01.01.00000000"}`)}
		}, "not yet"},
		{"unreachable", func(w *world) *fakeFetcher { return &fakeFetcher{err: errors.New("dial refused")} }, "cannot tell"},
		{"404", func(w *world) *fakeFetcher { return &fakeFetcher{status: http.StatusNotFound} }, "cannot tell"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t)
			id, _ := w.stage("0.1.0", 1)
			var f *fakeFetcher
			w.d.Confirm = &publish.Confirmer{BaseURL: "https://downloads.example.invalid/", Fetcher: fetcherFunc(func() *fakeFetcher { f = tc.fetch(w); return f })}
			err, ev := w.promote(id)
			if err != nil || terminal(t, ev).Step != "done" {
				t.Fatalf("confirmation changed the promote's outcome: %v %+v", err, ev)
			}
			if f == nil {
				t.Fatal("the promote made no confirmation fetch")
			}
			var got string
			for _, e := range ev {
				if e.Step == "confirm" {
					got = e.Status
				}
			}
			if got != tc.status || len(f.urls) != 1 || f.urls[0] != "https://downloads.example.invalid/umbree/latest.json" {
				t.Fatalf("confirm %q from %v, want %q", got, f.urls, tc.status)
			}
		})
	}
}

type fetcherFunc func() *fakeFetcher

func (fn fetcherFunc) Get(ctx context.Context, url string) (int, []byte, error) {
	return fn().Get(ctx, url)
}

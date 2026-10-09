package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const maxFetch = 1 << 20

type HTTPFetcher struct {
	guard *Guard
	http  *http.Client
}

func NewFetcher(guard *Guard) *HTTPFetcher {
	if guard == nil {
		guard = &Guard{}
	}
	return &HTTPFetcher{guard: guard, http: guard.Client()}
}

func (f *HTTPFetcher) Get(ctx context.Context, rawURL string) (int, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, fmt.Errorf("fetch: %w", err)
	}
	if err := f.guard.CheckURL(u); err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := f.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("fetch %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFetch))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("fetch %s: read body: %w", u.Redacted(), err)
	}
	return resp.StatusCode, body, nil
}

var _ Fetcher = (*HTTPFetcher)(nil)

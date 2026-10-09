package r2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type fakeDoer struct {
	status int
	body   string
	header http.Header
	reqs   []*http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.reqs = append(f.reqs, req)
	h := http.Header{}
	for k, v := range f.header {
		h[k] = v
	}
	return &http.Response{
		StatusCode:    f.status,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(f.body)),
		ContentLength: int64(len(f.body)),
		Request:       req,
	}, nil
}

func newTestClient(d Doer) *Client {
	return New("acct", "gated-test", "AKID", "SECRET", d)
}

func TestHeadReturnsSize(t *testing.T) {
	d := &fakeDoer{status: http.StatusOK, header: http.Header{"Content-Length": {strconv.Itoa(4096)}}}
	size, err := newTestClient(d).Head(context.Background(), "umbree/production/v1/SHA256SUMS.txt")
	if err != nil {
		t.Fatal(err)
	}
	if size != 4096 {
		t.Fatalf("Head size = %d, want 4096", size)
	}
	if len(d.reqs) != 1 || d.reqs[0].Method != http.MethodHead {
		t.Fatalf("requests = %v, want one HEAD", d.reqs)
	}
	if got := d.reqs[0].URL.String(); got != "https://acct.r2.cloudflarestorage.com/gated-test/umbree/production/v1/SHA256SUMS.txt" {
		t.Fatalf("HEAD url = %q", got)
	}
}

func TestHeadMissingIsNotFound(t *testing.T) {
	_, err := newTestClient(&fakeDoer{status: http.StatusNotFound}).Head(context.Background(), "k")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 err = %v, want ErrNotFound", err)
	}
	_, err = newTestClient(&fakeDoer{status: http.StatusForbidden}).Head(context.Background(), "k")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("403 err = %v, want a non-NotFound error", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("403 err %q does not carry the status", err)
	}
}

func TestGetBoundsBody(t *testing.T) {
	d := &fakeDoer{status: http.StatusOK, body: "0123456789"}
	got, err := newTestClient(d).Get(context.Background(), "k", 10)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("0123456789")) {
		t.Fatalf("Get at the limit = %q", got)
	}
	got, err = newTestClient(&fakeDoer{status: http.StatusOK, body: "0123456789A"}).Get(context.Background(), "k", 10)
	if err == nil {
		t.Fatalf("Get over the limit returned %q and no error, want a refusal not a truncation", got)
	}
	if got != nil {
		t.Fatalf("Get over the limit returned %d bytes alongside its error", len(got))
	}
	_, err = newTestClient(&fakeDoer{status: http.StatusNotFound}).Get(context.Background(), "k", 10)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 err = %v, want ErrNotFound", err)
	}
	_, err = newTestClient(&fakeDoer{status: http.StatusInternalServerError, body: "boom"}).Get(context.Background(), "k", 10)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("500 err = %v, want one carrying the status", err)
	}
	if _, err := newTestClient(&fakeDoer{status: http.StatusOK}).Get(context.Background(), "k", 0); err == nil {
		t.Fatal("Get with a zero limit was accepted")
	}
}

func TestRequestsAreSigned(t *testing.T) {
	d := &fakeDoer{status: http.StatusOK, body: "x", header: http.Header{"Content-Length": {"1"}}}
	c := newTestClient(d)
	if _, err := c.Head(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "k", 10); err != nil {
		t.Fatal(err)
	}
	if len(d.reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(d.reqs))
	}
	for _, r := range d.reqs {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKID/") {
			t.Errorf("%s Authorization = %q", r.Method, auth)
		}
		if r.Header.Get("X-Amz-Date") == "" {
			t.Errorf("%s carries no X-Amz-Date", r.Method)
		}
	}
	if d.reqs[0].Method != http.MethodHead || d.reqs[1].Method != http.MethodGet {
		t.Fatalf("methods = %s, %s", d.reqs[0].Method, d.reqs[1].Method)
	}
}

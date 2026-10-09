package register

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	httpTimeout      = 30 * time.Second
	maxResponseBytes = 1 << 20
	stateStaged      = "staged"
)

var ErrNoRow = errors.New("register: the service has no row for this stamp")

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, hc *http.Client) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("manage URL %q is not an absolute URL", baseURL)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("manage URL %q is not https; a release row is never registered over a connection anyone can read or rewrite", baseURL)
	}
	if hc == nil {
		hc = &http.Client{Timeout: httpTimeout}
	}
	return &Client{baseURL: base, http: hc}, nil
}

func (c *Client) Register(ctx context.Context, p Payload, key SigningKey) (RowStatus, error) {
	nonce, err := c.nonce(ctx)
	if err != nil {
		return RowStatus{}, err
	}
	p.Nonce = nonce
	if err := c.signedPost(ctx, "/api/v1/releases/register", p, key, nil); err != nil {
		return RowStatus{}, err
	}
	row, err := c.Status(ctx, p.Component, p.Channel, p.Stamp, key)
	if err != nil {
		return RowStatus{}, fmt.Errorf("registered, but the row could not be read back: %w", err)
	}
	if row.State != stateStaged || row.Stamp != p.Stamp {
		return row, fmt.Errorf("registered, but the service reads row %d back as %s %s, want %s %s",
			row.ID, row.State, row.Stamp, stateStaged, p.Stamp)
	}
	return row, nil
}

func (c *Client) Status(ctx context.Context, component, channel, stamp string, key SigningKey) (RowStatus, error) {
	nonce, err := c.nonce(ctx)
	if err != nil {
		return RowStatus{}, err
	}
	q := StatusQuery{Nonce: nonce, Component: component, Channel: channel, Stamp: stamp}
	var row RowStatus
	if err := c.signedPost(ctx, "/api/v1/releases/status", q, key, &row); err != nil {
		return RowStatus{}, err
	}
	return row, nil
}

type signable interface {
	SigningBytes() ([]byte, error)
}

func (c *Client) signedPost(ctx context.Context, path string, payload signable, key SigningKey, out any) error {
	msg, err := payload.SigningBytes()
	if err != nil {
		return err
	}
	body, err := json.Marshal(Envelope[signable]{Payload: payload, Sig: key.Sign(msg)})
	if err != nil {
		return fmt.Errorf("encode %s request: %w", path, err)
	}
	return c.post(ctx, path, body, out)
}

func (c *Client) nonce(ctx context.Context) (string, error) {
	var out struct {
		Nonce string `json:"nonce"`
	}
	if err := c.post(ctx, "/api/v1/releases/nonce", []byte(`{}`), &out); err != nil {
		return "", err
	}
	if out.Nonce == "" {
		return "", fmt.Errorf("POST %s/api/v1/releases/nonce answered without a nonce", c.baseURL)
	}
	return out.Nonce, nil
}

func (c *Client) post(ctx context.Context, path string, body []byte, out any) error {
	target := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("POST %s: %w", target, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", target, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("POST %s: read response: %w", target, err)
	}
	if resp.StatusCode == http.StatusNotFound && path == "/api/v1/releases/status" {
		return fmt.Errorf("%w: POST %s: HTTP 404 %s", ErrNoRow, target, strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("POST %s: HTTP %d %s", target, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("POST %s: decode response: %w", target, err)
	}
	return nil
}

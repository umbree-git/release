package r2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

var ErrNotFound = errors.New("r2: object not found")

func (c *Client) signedRequest(ctx context.Context, method, key string) (*http.Response, error) {
	reqURL := fmt.Sprintf("%s/%s/%s", c.endpoint, c.bucket, key)
	req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("r2: %s %s: new request: %w", method, key, err)
	}
	signV4(req, c.accessKeyID, c.secret, "auto", "s3", nil, time.Now())
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("r2: %s %s: %w", method, key, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, fmt.Errorf("r2: %s %s: %w", method, key, ErrNotFound)
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		return nil, fmt.Errorf("r2: %s %s: status %d: %s", method, key, resp.StatusCode, b)
	}
	return resp, nil
}

func (c *Client) Head(ctx context.Context, key string) (int64, error) {
	resp, err := c.signedRequest(ctx, http.MethodHead, key)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if v := resp.Header.Get("Content-Length"); v != "" {
		size, err := strconv.ParseInt(v, 10, 64)
		if err != nil || size < 0 {
			return 0, fmt.Errorf("r2: HEAD %s: bad Content-Length %q", key, v)
		}
		return size, nil
	}
	if resp.ContentLength < 0 {
		return 0, fmt.Errorf("r2: HEAD %s: no Content-Length", key)
	}
	return resp.ContentLength, nil
}

func (c *Client) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("r2: GET %s: limit %d must be positive", key, limit)
	}
	resp, err := c.signedRequest(ctx, http.MethodGet, key)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("r2: GET %s: read: %w", key, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("r2: GET %s: body exceeds the %d-byte limit", key, limit)
	}
	return body, nil
}

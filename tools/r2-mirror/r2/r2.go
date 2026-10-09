package r2

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	endpoint    string
	bucket      string
	accessKeyID string
	secret      string
	doer        Doer
}

func New(accountID, bucket, accessKeyID, secret string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{
		endpoint:    "https://" + accountID + ".r2.cloudflarestorage.com",
		bucket:      strings.Trim(bucket, "/"),
		accessKeyID: accessKeyID,
		secret:      secret,
		doer:        doer,
	}
}

func (c *Client) Bucket() string { return c.bucket }

func (c *Client) Put(ctx context.Context, key string, body []byte, contentType string) error {
	url := fmt.Sprintf("%s/%s/%s", c.endpoint, c.bucket, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("r2: put: new request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(body))
	signV4(req, c.accessKeyID, c.secret, "auto", "s3", body, time.Now())
	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("r2: put %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("r2: put %s: status %d: %s", key, resp.StatusCode, b)
	}
	return nil
}

type listResult struct {
	Contents []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
	IsTruncated bool   `xml:"IsTruncated"`
	NextToken   string `xml:"NextContinuationToken"`
}

func (c *Client) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	token := ""
	for {
		q := url.Values{}
		q.Set("list-type", "2")
		q.Set("prefix", prefix)
		if token != "" {
			q.Set("continuation-token", token)
		}
		enc := strings.ReplaceAll(q.Encode(), "+", "%20")
		reqURL := fmt.Sprintf("%s/%s?%s", c.endpoint, c.bucket, enc)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("r2: list %q: new request: %w", prefix, err)
		}
		signV4(req, c.accessKeyID, c.secret, "auto", "s3", nil, time.Now())
		resp, err := c.doer.Do(req)
		if err != nil {
			return nil, fmt.Errorf("r2: list %q: %w", prefix, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("r2: list %q: read response: %w", prefix, err)
		}
		if resp.StatusCode/100 != 2 {
			return nil, fmt.Errorf("r2: list %q: status %d: %s", prefix, resp.StatusCode, body)
		}
		var lr listResult
		if err := xml.Unmarshal(body, &lr); err != nil {
			return nil, fmt.Errorf("r2: list %q: parse response: %w", prefix, err)
		}
		for _, o := range lr.Contents {
			keys = append(keys, o.Key)
		}
		if !lr.IsTruncated || lr.NextToken == "" {
			break
		}
		token = lr.NextToken
	}
	return keys, nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	reqURL := fmt.Sprintf("%s/%s/%s", c.endpoint, c.bucket, key)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("r2: delete %s: new request: %w", key, err)
	}
	signV4(req, c.accessKeyID, c.secret, "auto", "s3", nil, time.Now())
	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("r2: delete %s: %w", key, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("r2: delete %s: status %d: %s", key, resp.StatusCode, b)
	}
	return nil
}

func (c *Client) Copy(ctx context.Context, srcBucket, srcKey, dstKey string) error {
	reqURL := fmt.Sprintf("%s/%s/%s", c.endpoint, c.bucket, dstKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("r2: copy %s: new request: %w", dstKey, err)
	}
	req.ContentLength = 0
	req.Header.Set("X-Amz-Copy-Source", "/"+strings.Trim(srcBucket, "/")+"/"+escapeKey(srcKey))
	signV4(req, c.accessKeyID, c.secret, "auto", "s3", nil, time.Now())
	resp, err := c.doer.Do(req)
	if err != nil {
		return fmt.Errorf("r2: copy %s: %w", dstKey, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("r2: copy %s: read response: %w", dstKey, err)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("r2: copy %s: status %d: %s", dstKey, resp.StatusCode, body)
	}
	var s3err struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
		Message string   `xml:"Message"`
	}
	if xml.Unmarshal(body, &s3err) == nil {
		return fmt.Errorf("r2: copy %s: status %d carried an error: %s %s", dstKey, resp.StatusCode, s3err.Code, s3err.Message)
	}
	return nil
}

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(url.QueryEscape(p), "+", "%20")
	}
	return strings.Join(parts, "/")
}

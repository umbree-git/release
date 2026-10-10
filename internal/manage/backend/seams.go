package backend

import (
	"context"

	"umbree-release-r2-mirror/r2"
)

var ErrNotFound = r2.ErrNotFound

type Gated interface {
	Bucket() string
	Head(ctx context.Context, key string) (int64, error)
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
}

type Public interface {
	Head(ctx context.Context, key string) (int64, error)
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
	Put(ctx context.Context, key string, body []byte, contentType string) error
	Copy(ctx context.Context, srcBucket, srcKey, dstKey string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

type Fetcher interface {
	Get(ctx context.Context, url string) (status int, body []byte, err error)
}

var (
	_ Gated  = (*r2.Client)(nil)
	_ Public = (*r2.Client)(nil)
)

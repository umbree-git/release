package r2

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("r2: object not found")

func (c *Client) Head(ctx context.Context, key string) (int64, error) {
	return 0, nil
}

func (c *Client) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	return nil, nil
}

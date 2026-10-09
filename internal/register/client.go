package register

import (
	"context"
	"errors"
	"net/http"
)

var ErrNoRow = errors.New("register: the service has no row for this stamp")

var errUnbuilt = errors.New("register: not built")

type SigningKey struct{ KeyID string }

func (k SigningKey) Sign(msg []byte) string { return "" }

func LoadSigningKey(path string) (SigningKey, error) { return SigningKey{}, errUnbuilt }

type Client struct{}

func NewClient(baseURL string, hc *http.Client) (*Client, error) { return &Client{}, nil }

func (c *Client) Register(ctx context.Context, p Payload, key SigningKey) (RowStatus, error) {
	return RowStatus{}, errUnbuilt
}

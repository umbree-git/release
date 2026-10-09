package register

import (
	"context"
	"errors"
	"net/http"
)

const (
	SumsName    = "SHA256SUMS.txt"
	MinisigName = "SHA256SUMS.txt.minisig"
)

var errUnbuilt = errors.New("register: not built")

type Artifact struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Payload struct {
	Nonce      string     `json:"nonce"`
	Component  string     `json:"component"`
	Channel    string     `json:"channel"`
	Version    string     `json:"version"`
	Stamp      string     `json:"stamp"`
	Artifacts  []Artifact `json:"artifacts"`
	SumsKey    string     `json:"sums_key"`
	MinisigKey string     `json:"minisig_key"`
}

type StatusQuery struct {
	Nonce     string `json:"nonce"`
	Component string `json:"component"`
	Channel   string `json:"channel"`
	Stamp     string `json:"stamp"`
}

type Envelope[T any] struct {
	Payload T      `json:"payload"`
	Sig     string `json:"sig"`
}

type RowStatus struct {
	ID      int64  `json:"id"`
	State   string `json:"state"`
	Stamp   string `json:"stamp"`
	Version string `json:"version"`
}

type Receipt struct {
	Component string     `json:"component"`
	Channel   string     `json:"channel"`
	Stamp     string     `json:"stamp"`
	Version   string     `json:"version"`
	Objects   []Artifact `json:"objects"`
}

func (p Payload) SigningBytes() ([]byte, error)       { return nil, errUnbuilt }
func (q StatusQuery) SigningBytes() ([]byte, error)   { return nil, errUnbuilt }
func KeyBase(component, channel, stamp string) string { return "" }
func ReadReceipt(path string) (Receipt, error)        { return Receipt{}, errUnbuilt }
func PayloadFromReceipt(r Receipt) (Payload, error)   { return Payload{}, errUnbuilt }

var ErrNoRow = errors.New("register: the service has no row for this stamp")

type SigningKey struct{ KeyID string }

func (k SigningKey) Sign(msg []byte) string          { return "" }
func LoadSigningKey(path string) (SigningKey, error) { return SigningKey{}, errUnbuilt }

type Client struct{}

func NewClient(baseURL string, hc *http.Client) (*Client, error) { return &Client{}, nil }

func (c *Client) Register(ctx context.Context, p Payload, key SigningKey) (RowStatus, error) {
	return RowStatus{}, errUnbuilt
}

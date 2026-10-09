package register

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	SumsName    = "SHA256SUMS.txt"
	MinisigName = "SHA256SUMS.txt.minisig"
)

const (
	registerDomain = "umbree-release register v1\n"
	statusDomain   = "umbree-release status v1\n"
)

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

func (p Payload) SigningBytes() ([]byte, error) { return canonical(registerDomain, p) }

func (q StatusQuery) SigningBytes() ([]byte, error) { return canonical(statusDomain, q) }

func canonical(domain string, v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(domain)
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode canonical payload: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

func KeyBase(component, channel, stamp string) string {
	return component + "/" + channel + "/" + stamp + "/"
}

type Receipt struct {
	Component string     `json:"component"`
	Channel   string     `json:"channel"`
	Stamp     string     `json:"stamp"`
	Version   string     `json:"version"`
	Objects   []Artifact `json:"objects"`
}

func ReadReceipt(path string) (Receipt, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Receipt{}, fmt.Errorf("read receipt: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var r Receipt
	if err := dec.Decode(&r); err != nil {
		return Receipt{}, fmt.Errorf("receipt %s is not a gated-store receipt: %w", path, err)
	}
	return r, nil
}

func PayloadFromReceipt(r Receipt) (Payload, error) {
	base := KeyBase(r.Component, r.Channel, r.Stamp)
	p := Payload{
		Component: r.Component, Channel: r.Channel, Version: r.Version, Stamp: r.Stamp,
		Artifacts: r.Objects,
	}
	for _, a := range r.Objects {
		switch strings.TrimPrefix(a.Key, base) {
		case SumsName:
			p.SumsKey = a.Key
		case MinisigName:
			p.MinisigKey = a.Key
		}
	}
	if p.SumsKey == "" {
		return Payload{}, fmt.Errorf("the receipt lists no %s%s", base, SumsName)
	}
	if p.MinisigKey == "" {
		return Payload{}, fmt.Errorf("the receipt lists no %s%s", base, MinisigName)
	}
	return p, nil
}

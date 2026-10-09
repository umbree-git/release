package sums

import (
	"crypto/ed25519"
	"errors"
)

var errUnbuilt = errors.New("sums: not built")

type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

func ParsePublicKey(contents string) (PublicKey, error) { return PublicKey{}, nil }

func Verify(key PublicKey, message, signature []byte) error { return nil }

func Parse(body []byte) (map[string]string, error) { return nil, errUnbuilt }

func Agree(listed, want map[string]string) error { return nil }

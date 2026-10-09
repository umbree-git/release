package intake

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

//go:embed umbree-release.pub
var releasePublicKey string

const (
	minisignPublicLen = 42
	publicKeyIDOffset = 2
	publicKeyIDLen    = 8
)

func ReleaseKey() (ed25519.PublicKey, error) {
	return parseMinisignPublicKey(releasePublicKey)
}

func parseMinisignPublicKey(contents string) (ed25519.PublicKey, error) {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("minisign public key: not base64: %w", err)
		}
		if len(raw) != minisignPublicLen || string(raw[:2]) != "Ed" {
			return nil, fmt.Errorf("minisign public key: not an Ed25519 key (%d bytes)", len(raw))
		}
		key := make(ed25519.PublicKey, ed25519.PublicKeySize)
		copy(key, raw[publicKeyIDOffset+publicKeyIDLen:])
		return key, nil
	}
	return nil, errors.New("minisign public key: no key line found")
}

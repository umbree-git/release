package register

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	minisignSecretLen = 158
	kdfOffset         = 2
	keyNumberOffset   = 54
	keyIDLen          = 8
)

type SigningKey struct {
	KeyID   string
	private ed25519.PrivateKey
}

func (k SigningKey) String() string { return "minisign signing key " + k.KeyID }

func (k SigningKey) GoString() string { return k.String() }

func (k SigningKey) Sign(msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(k.private, msg))
}

func LoadSigningKey(path string) (SigningKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SigningKey{}, fmt.Errorf("read signing key: %w", err)
	}
	raw, err := decodeSecretLine(string(data))
	if err != nil {
		return SigningKey{}, fmt.Errorf("signing key %s: %w", path, err)
	}
	if raw[kdfOffset] != 0 || raw[kdfOffset+1] != 0 {
		return SigningKey{}, fmt.Errorf("signing key %s is password-protected; the release key is password-less", path)
	}
	keyID := raw[keyNumberOffset : keyNumberOffset+keyIDLen]
	secret := raw[keyNumberOffset+keyIDLen : keyNumberOffset+keyIDLen+ed25519.PrivateKeySize]
	return SigningKey{
		KeyID:   base64.StdEncoding.EncodeToString(keyID),
		private: ed25519.PrivateKey(append([]byte(nil), secret...)),
	}, nil
}

func decodeSecretLine(contents string) ([]byte, error) {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, errors.New("the key line is not base64")
		}
		if len(raw) != minisignSecretLen || string(raw[:2]) != "Ed" {
			return nil, fmt.Errorf("not a minisign Ed25519 secret key (%d bytes)", len(raw))
		}
		return raw, nil
	}
	return nil, errors.New("no key line found")
}

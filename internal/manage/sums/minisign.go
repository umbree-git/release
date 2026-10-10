package sums

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"
)

const (
	publicKeyLen    = 2 + 8 + ed25519.PublicKeySize
	signatureLen    = 2 + 8 + ed25519.SignatureSize
	trustedPrefix   = "trusted comment: "
	untrustedPrefix = "untrusted comment:"
)

type PublicKey struct {
	ID  [8]byte
	Key ed25519.PublicKey
}

func ParsePublicKey(contents string) (PublicKey, error) {
	for _, line := range strings.Split(contents, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, untrustedPrefix) {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return PublicKey{}, fmt.Errorf("minisign public key: not base64: %w", err)
		}
		if len(raw) != publicKeyLen || string(raw[:2]) != "Ed" {
			return PublicKey{}, fmt.Errorf("minisign public key: not an Ed25519 key (%d bytes)", len(raw))
		}
		var k PublicKey
		copy(k.ID[:], raw[2:10])
		k.Key = ed25519.PublicKey(bytes.Clone(raw[10:]))
		return k, nil
	}
	return PublicKey{}, errors.New("minisign public key: no key line found")
}

type signatureFile struct {
	algorithm string
	keyID     [8]byte
	signature []byte
	trusted   string
	global    []byte
}

func parseSignature(file []byte) (signatureFile, error) {
	lines := strings.Split(strings.TrimRight(string(file), "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], untrustedPrefix) || !strings.HasPrefix(lines[2], trustedPrefix) {
		return signatureFile{}, errors.New("minisign signature: not the four-line minisign format")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(raw) != signatureLen {
		return signatureFile{}, errors.New("minisign signature: the signature line is malformed")
	}
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != ed25519.SignatureSize {
		return signatureFile{}, errors.New("minisign signature: the global signature is malformed")
	}
	sf := signatureFile{algorithm: string(raw[:2]), signature: raw[10:], trusted: strings.TrimPrefix(lines[2], trustedPrefix), global: global}
	copy(sf.keyID[:], raw[2:10])
	return sf, nil
}

func Verify(key PublicKey, message, signature []byte) error {
	sf, err := parseSignature(signature)
	if err != nil {
		return err
	}
	if sf.keyID != key.ID {
		return fmt.Errorf("minisign signature: made by key %X, not the release key %X", sf.keyID, key.ID)
	}
	signed := message
	switch sf.algorithm {
	case "Ed":
	case "ED":
		sum := blake2b.Sum512(message)
		signed = sum[:]
	default:
		return fmt.Errorf("minisign signature: unknown algorithm %q", sf.algorithm)
	}
	if !ed25519.Verify(key.Key, signed, sf.signature) {
		return errors.New("minisign signature: does not verify against the release key")
	}
	if !ed25519.Verify(key.Key, append(bytes.Clone(sf.signature), sf.trusted...), sf.global) {
		return errors.New("minisign signature: the trusted comment does not verify")
	}
	return nil
}

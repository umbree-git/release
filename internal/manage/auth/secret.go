package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const secretKeyLen = 32

type Sealer struct {
	aead cipher.AEAD
}

func LoadSealer(path string) (*Sealer, error) {
	if path == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("secret key path %q must be absolute", path)
	}
	if filepath.Clean(path) != path {
		return nil, fmt.Errorf("secret key path %q is not clean; write it as %q", path, filepath.Clean(path))
	}
	key, err := readSecretKey(path)
	if err != nil {
		return nil, err
	}
	aesKey, err := hkdf.Key(sha256.New, key, nil, "umbree-manage/totp-secret/v1", 32)
	if err != nil {
		return nil, fmt.Errorf("derive sealing key: %w", err)
	}
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, fmt.Errorf("sealing cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sealing mode: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

func readSecretKey(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open secret key %q (it is provisioned by the operator and never created here; a symlink is refused): %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat secret key %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret key %q is not a regular file", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("secret key %q is mode %04o; it must not be readable by group or other", path, perm)
	}
	key := make([]byte, secretKeyLen+1)
	n, err := io.ReadFull(f, key)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read secret key %q: %w", path, err)
	}
	if n != secretKeyLen {
		return nil, fmt.Errorf("secret key %q is not %d bytes; it must be exactly %d random bytes", path, secretKeyLen, secretKeyLen)
	}
	return key[:secretKeyLen], nil
}

func (s *Sealer) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("seal: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, fmt.Errorf("open: sealed value is %d bytes, shorter than a nonce", len(sealed))
	}
	out, err := s.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return nil, fmt.Errorf("open: the sealed value does not decrypt; the secret key file does not match this catalog: %w", err)
	}
	return out, nil
}

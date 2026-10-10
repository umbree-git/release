package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const confirmTTL = 10 * time.Minute

var errBadConfirm = errors.New("the confirm token is missing, spent, expired, or not for this action")

type confirmer struct {
	key []byte
	now func() int64

	mu   sync.Mutex
	used map[string]int64
}

func newConfirmer(now func() int64) (*confirmer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("confirm: key: %w", err)
	}
	return &confirmer{key: key, now: now, used: map[string]int64{}}, nil
}

func (c *confirmer) mint(session, action string, row int64) (string, error) {
	body := make([]byte, 24)
	if _, err := rand.Read(body[:16]); err != nil {
		return "", fmt.Errorf("confirm: nonce: %w", err)
	}
	binary.BigEndian.PutUint64(body[16:], uint64(c.now()+int64(confirmTTL.Seconds())))
	payload := base64.RawURLEncoding.EncodeToString(body)
	return payload + "." + base64.RawURLEncoding.EncodeToString(c.sign(session, action, row, payload)), nil
}

func (c *confirmer) sign(session, action string, row int64, payload string) []byte {
	m := hmac.New(sha256.New, c.key)
	fmt.Fprintf(m, "%s\x00%s\x00%d\x00%s", session, action, row, payload)
	return m.Sum(nil)
}

func (c *confirmer) consume(token, session, action string, row int64) error {
	payload, sig, ok := strings.Cut(token, ".")
	if !ok {
		return errBadConfirm
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, c.sign(session, action, row, payload)) {
		return errBadConfirm
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || len(body) != 24 {
		return errBadConfirm
	}
	exp := int64(binary.BigEndian.Uint64(body[16:]))
	now := c.now()
	if now > exp {
		return errBadConfirm
	}
	nonce := string(body[:16])
	c.mu.Lock()
	defer c.mu.Unlock()
	for n, e := range c.used {
		if now > e {
			delete(c.used, n)
		}
	}
	if _, spent := c.used[nonce]; spent {
		return errBadConfirm
	}
	c.used[nonce] = exp
	return nil
}

package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"time"
)

const (
	Period     = 30
	secretSize = 20
)

var encoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func GenerateSecret() (string, error) {
	raw := make([]byte, secretSize)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("totp: generate secret: %w", err)
	}
	return encoding.EncodeToString(raw), nil
}

func Code(secret string, at time.Time) (string, error) {
	key, err := encoding.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("totp: bad secret: %w", err)
	}
	return hotp(key, uint64(at.Unix()/Period)), nil
}

func Verify(secret, code string, now time.Time, lastUsedStep int64) (int64, bool) {
	key, err := encoding.DecodeString(secret)
	if err != nil || len(code) != 6 {
		return 0, false
	}
	current := now.Unix() / Period
	for _, offset := range []int64{0, -1, 1} {
		step := current + offset
		if step <= lastUsedStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hotp(key, uint64(step))), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func OTPAuthURL(issuer, account, secret string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", issuer)
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + v.Encode()
}

func hotp(key []byte, counter uint64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}

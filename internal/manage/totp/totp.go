package totp

import (
	"errors"
	"time"
)

const Period = 30

var errNotBuilt = errors.New("totp: not built")

func GenerateSecret() (string, error) { return "", errNotBuilt }

func Code(secret string, at time.Time) (string, error) { return "", errNotBuilt }

func Verify(secret, code string, now time.Time, lastUsedStep int64) (int64, bool) { return 0, false }

func OTPAuthURL(issuer, account, secret string) string { return "" }

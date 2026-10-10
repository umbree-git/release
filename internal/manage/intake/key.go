package intake

import (
	"crypto/ed25519"
	_ "embed"

	"github.com/umbree-git/release/internal/manage/sums"
)

//go:embed umbree-release.pub
var releasePublicKey string

func ReleaseMinisignKey() (sums.PublicKey, error) {
	return sums.ParsePublicKey(releasePublicKey)
}

func ReleaseKey() (ed25519.PublicKey, error) {
	k, err := ReleaseMinisignKey()
	if err != nil {
		return nil, err
	}
	return k.Key, nil
}

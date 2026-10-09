package backendtest

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"golang.org/x/crypto/blake2b"

	"github.com/umbree-git/release/internal/manage/sums"
	"github.com/umbree-git/release/internal/register"
)

var keyID = [8]byte{0x75, 0x6d, 0x62, 0x72, 0x65, 0x65, 0x74, 0x31}

func signingKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("backendtest release key, test only"))
	return ed25519.NewKeyFromSeed(seed[:])
}

func ReleaseKey() sums.PublicKey {
	return sums.PublicKey{ID: keyID, Key: signingKey().Public().(ed25519.PublicKey)}
}

func SignWith(priv ed25519.PrivateKey, message []byte) []byte {
	hash := blake2b.Sum512(message)
	sig := ed25519.Sign(priv, hash[:])
	line := append(append([]byte("ED"), keyID[:]...), sig...)
	trusted := "timestamp:0\tfile:SHA256SUMS.txt\thashed"
	global := ed25519.Sign(priv, append(append([]byte(nil), sig...), trusted...))
	return []byte("untrusted comment: backendtest signature\n" +
		base64.StdEncoding.EncodeToString(line) + "\n" +
		"trusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n")
}

func Sign(message []byte) []byte { return SignWith(signingKey(), message) }

func SHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func SumsFile(zips map[string][]byte) []byte {
	names := make([]string, 0, len(zips))
	for n := range zips {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "%s  %s\n", SHA256(zips[n]), n)
	}
	return []byte(b.String())
}

func SeedRelease(s *Store, base string, zips map[string][]byte) []register.Artifact {
	sumsBody := SumsFile(zips)
	files := map[string][]byte{register.SumsName: sumsBody, register.MinisigName: Sign(sumsBody)}
	for n, b := range zips {
		files[n] = b
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var arts []register.Artifact
	for _, n := range names {
		s.Seed(base+n, files[n])
		arts = append(arts, register.Artifact{Key: base + n, Size: int64(len(files[n])), SHA256: SHA256(files[n])})
	}
	return arts
}

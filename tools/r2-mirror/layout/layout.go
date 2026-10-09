package layout

import "regexp"

const (
	Production   = "production"
	ManifestName = "latest.json"
)

var Components = []string{"umbree", "umbreed"}

var (
	StableStampRe = regexp.MustCompile(`^$`)
	BetaStampRe   = regexp.MustCompile(`^$`)
)

func GatedPrefix(comp, channel string) (string, error) {
	return "", nil
}

func GatedKey(comp, channel, stamp, file string) (string, error) {
	return "", nil
}

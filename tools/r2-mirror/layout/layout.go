package layout

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	Production   = "production"
	ManifestName = "latest.json"
)

var Components = []string{"umbree", "umbreed"}

var (
	StableStampRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$`)
	BetaStampRe   = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+\.beta\.[0-9]{4}\.[0-9]{2}\.[0-9]{2}\.[0-9a-f]{8}$`)
)

var heldChannels = []string{"beta"}

var gatedStampShape = map[string]*regexp.Regexp{
	Production: StableStampRe,
}

func GatedPrefix(comp, channel string) (string, error) {
	if !slices.Contains(Components, comp) {
		return "", fmt.Errorf("unknown component %q (want %s)", comp, strings.Join(Components, " | "))
	}
	if slices.Contains(heldChannels, channel) {
		return "", fmt.Errorf("channel %q is held: the gated store takes %s only until the hold is lifted", channel, Production)
	}
	if _, ok := gatedStampShape[channel]; !ok {
		return "", fmt.Errorf("unknown channel %q (the gated store takes %s)", channel, Production)
	}
	return comp + "/" + channel + "/", nil
}

func GatedKey(comp, channel, stamp, file string) (string, error) {
	prefix, err := GatedPrefix(comp, channel)
	if err != nil {
		return "", err
	}
	if !gatedStampShape[channel].MatchString(stamp) {
		return "", fmt.Errorf("stamp %q is not a %s stamp (want v<X.Y.Z>.<YYYY>.<MM>.<DD>.<sha8>)", stamp, channel)
	}
	if file == "" || file == "." || file == ".." || strings.Contains(file, "/") || file == ManifestName {
		return "", fmt.Errorf("file %q cannot be a gated object: the gated store holds artifacts only, never a manifest or a nested path", file)
	}
	return prefix + stamp + "/" + file, nil
}

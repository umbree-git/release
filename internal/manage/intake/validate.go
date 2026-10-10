package intake

import (
	"fmt"
	"regexp"
	"strings"

	"umbree-release-r2-mirror/layout"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/register"
)

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validateRow(component, channel, stamp string) (string, bool) {
	if !catalog.ValidComponent(component) {
		return fmt.Sprintf("unknown component %q", component), false
	}
	if catalog.IsHeldChannel(channel) {
		return fmt.Sprintf("channel %q is held: the catalog takes %s only", channel, catalog.ChannelProduction), false
	}
	if !catalog.ValidChannel(channel) {
		return fmt.Sprintf("unknown channel %q", channel), false
	}
	if !catalog.StampMatchesChannel(stamp, channel) {
		return fmt.Sprintf("stamp %q is not a %s stamp", stamp, channel), false
	}
	return "", true
}

func validateStatusQuery(q register.StatusQuery) (string, bool) {
	return validateRow(q.Component, q.Channel, q.Stamp)
}

func validateRegistration(p register.Payload) (string, bool) {
	if msg, ok := validateRow(p.Component, p.Channel, p.Stamp); !ok {
		return msg, false
	}
	if p.Version == "" || !strings.HasPrefix(p.Stamp, "v"+p.Version+".") {
		return fmt.Sprintf("version %q does not match stamp %q", p.Version, p.Stamp), false
	}
	if len(p.Artifacts) == 0 {
		return "the row lists no artifacts", false
	}
	listed, msg, ok := validateArtifacts(p)
	if !ok {
		return msg, false
	}
	base := register.KeyBase(p.Component, p.Channel, p.Stamp)
	if p.SumsKey != base+register.SumsName || !listed[p.SumsKey] {
		return fmt.Sprintf("sums key %q is not the listed %s%s", p.SumsKey, base, register.SumsName), false
	}
	if p.MinisigKey != base+register.MinisigName || !listed[p.MinisigKey] {
		return fmt.Sprintf("signature key %q is not the listed %s%s", p.MinisigKey, base, register.MinisigName), false
	}
	return "", true
}

func validateArtifacts(p register.Payload) (map[string]bool, string, bool) {
	base := register.KeyBase(p.Component, p.Channel, p.Stamp)
	listed := make(map[string]bool, len(p.Artifacts))
	for _, a := range p.Artifacts {
		want, err := layout.GatedKey(p.Component, p.Channel, p.Stamp, strings.TrimPrefix(a.Key, base))
		if err != nil || want != a.Key {
			return nil, fmt.Sprintf("artifact key %q is not under %s", a.Key, base), false
		}
		if listed[a.Key] {
			return nil, fmt.Sprintf("artifact key %q is listed twice", a.Key), false
		}
		if a.Size <= 0 {
			return nil, fmt.Sprintf("artifact %q has size %d", a.Key, a.Size), false
		}
		if !sha256Hex.MatchString(a.SHA256) {
			return nil, fmt.Sprintf("artifact %q has no lowercase hex sha256", a.Key), false
		}
		listed[a.Key] = true
	}
	return listed, "", true
}

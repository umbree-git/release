package catalog

import (
	"slices"

	"umbree-release-r2-mirror/layout"
)

const ChannelProduction = layout.Production

var Channels = []string{ChannelProduction}

var HeldChannels = []string{"beta"}

var Components = slices.Clone(layout.Components)

const (
	StateStaged  = "staged"
	StatePublic  = "public"
	StateYanked  = "yanked"
	StateExpired = "expired"
)

var States = []string{StateStaged, StatePublic, StateYanked, StateExpired}

func ValidChannel(s string) bool { return slices.Contains(Channels, s) }

func IsHeldChannel(s string) bool { return slices.Contains(HeldChannels, s) }

func ValidComponent(s string) bool { return slices.Contains(Components, s) }

func ValidState(s string) bool { return slices.Contains(States, s) }

func StampMatchesChannel(stamp, channel string) bool {
	return channel == ChannelProduction && layout.StableStampRe.MatchString(stamp)
}

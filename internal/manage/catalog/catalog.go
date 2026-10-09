package catalog

const ChannelProduction = "production"

var Channels []string

var HeldChannels []string

var Components []string

const (
	StateStaged  = "staged"
	StatePublic  = "public"
	StateYanked  = "yanked"
	StateExpired = "expired"
)

var States []string

func ValidChannel(s string) bool { return false }

func IsHeldChannel(s string) bool { return false }

func ValidComponent(s string) bool { return false }

func ValidState(s string) bool { return false }

func StampMatchesChannel(stamp, channel string) bool { return false }

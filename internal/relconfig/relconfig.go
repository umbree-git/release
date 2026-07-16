package relconfig

import (
	"fmt"

	"github.com/burrowee-git/release-kit/build"
)

// Components lists every releasable umbree component. The channel ships the
// cli only for now (umbreed etc. are v-next), so this is the whole set.
var Components = []string{"umbree"}

func Targets() []build.Target {
	return []build.Target{
		{OS: "darwin", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"}, {OS: "linux", Arch: "amd64"},
	}
}

// Bins returns the build.BinSpec list for comp. GoWork is left empty
// (release-kit build.Compile defaults it to "off" — module mode, pinned tags).
func Bins(comp, stamp string) ([]build.BinSpec, error) {
	v := "-X main.version=" + stamp
	switch comp {
	case "umbree":
		return []build.BinSpec{
			{Name: "umbree", Package: "./cmd/umbree", Ldflags: v},
		}, nil
	}
	return nil, fmt.Errorf("unknown component %q", comp)
}

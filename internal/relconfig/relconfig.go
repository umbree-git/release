package relconfig

import (
	"fmt"

	"github.com/burrowee-git/release-kit/build"
)

var Components = []string{"umbree", "umbreed"}

func Targets() []build.Target {
	return []build.Target{
		{OS: "darwin", Arch: "arm64"}, {OS: "darwin", Arch: "amd64"},
		{OS: "linux", Arch: "arm64"}, {OS: "linux", Arch: "amd64"},
	}
}

func Bins(comp, stamp string) ([]build.BinSpec, error) {
	v := "-X main.version=" + stamp
	switch comp {
	case "umbree":
		return []build.BinSpec{
			{Name: "umbree", Package: "./cmd/umbree", Ldflags: v},
		}, nil
	case "umbreed":
		return []build.BinSpec{
			{Name: "umbreed", Package: "./cmd/umbreed", Ldflags: v},
		}, nil
	}
	return nil, fmt.Errorf("unknown component %q", comp)
}

package static

import (
	"fmt"
	"regexp"
	"slices"

	"umbree-release-r2-mirror/layout"
)

const Callback = "__umbreeVersion"

var (
	jsVersionRe = regexp.MustCompile(`^[0-9][0-9.]*$`)
	jsStampRe   = regexp.MustCompile(`^v[0-9A-Za-z.]*$`)
)

func VersionJS(component, version, stamp string) ([]byte, error) {
	switch {
	case !slices.Contains(layout.Components, component):
		return nil, fmt.Errorf("%w: unknown component %q", ErrBadInput, component)
	case !jsVersionRe.MatchString(version):
		return nil, fmt.Errorf("%w: version %q is not dotted numbers", ErrBadInput, version)
	case !jsStampRe.MatchString(stamp):
		return nil, fmt.Errorf("%w: stamp %q is not a release stamp", ErrBadInput, stamp)
	}
	return fmt.Appendf(nil, "%s({\"component\":\"%s\",\"version\":\"%s\",\"stamp\":\"%s\"});\n", Callback, component, version, stamp), nil
}

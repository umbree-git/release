package relconfig

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/burrowee-git/release-kit/version"
)

// Stamp reproduces `tools/version.sh <comp> --stamp` for srcDir, wrapping
// release-kit's version.Stamp with the library's DateVersionScheme
// (v<semver>.<dateUTC>.<sha>, dateUTC = YYYY.MM.DD) — byte-identical to
// version.sh's stamp(). No umbree-specific scheme func is needed.
func Stamp(ctx context.Context, semverFile, srcDir string) (string, error) {
	return version.Stamp(ctx, semverFile, srcDir, version.DateVersionScheme)
}

// BetaDateVersionScheme yields "v<semver>.beta.<YYYY.MM.DD>.<sha>" — the beta
// channel's stamp (tools/version.sh --channel beta --stamp). The infix is
// dotted, not "-beta", so a whole-string sort -V still orders by semver first
// and one anchored regex tells the channels apart.
func BetaDateVersionScheme(semver, sha, dateUTC string) string {
	return "v" + semver + ".beta." + dateUTC + "." + sha
}

// Channels a cut can target. Anything else is refused by StampFor and by
// `rkit build --channel`.
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// VersionFile is the semver source file for a channel: versions/<comp> on
// stable, versions/<comp>.beta on beta (its presence is the open-cycle
// marker). Unknown channels return an error rather than a guessed path.
func VersionFile(channel, versionsDir, comp string) (string, error) {
	switch channel {
	case ChannelStable:
		return filepath.Join(versionsDir, comp), nil
	case ChannelBeta:
		return filepath.Join(versionsDir, comp+".beta"), nil
	}
	return "", fmt.Errorf("unknown channel %q (want stable | beta)", channel)
}

// StampFor resolves the stamp for a channel: the semver file is the
// channel's (VersionFile) and the scheme is the channel's. It is
// byte-identical to `tools/version.sh <comp> --channel <channel> --stamp`.
func StampFor(ctx context.Context, channel, versionsDir, comp, srcDir string) (string, error) {
	file, err := VersionFile(channel, versionsDir, comp)
	if err != nil {
		return "", err
	}
	if channel == ChannelBeta {
		return version.Stamp(ctx, file, srcDir, BetaDateVersionScheme)
	}
	return Stamp(ctx, file, srcDir)
}

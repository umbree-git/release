package relconfig

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/burrowee-git/release-kit/version"
)

func Stamp(ctx context.Context, semverFile, srcDir string) (string, error) {
	return version.Stamp(ctx, semverFile, srcDir, version.DateVersionScheme)
}

func BetaDateVersionScheme(semver, sha, dateUTC string) string {
	return "v" + semver + ".beta." + dateUTC + "." + sha
}

const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

func VersionFile(channel, versionsDir, comp string) (string, error) {
	switch channel {
	case ChannelStable:
		return filepath.Join(versionsDir, comp), nil
	case ChannelBeta:
		return filepath.Join(versionsDir, comp+".beta"), nil
	}
	return "", fmt.Errorf("unknown channel %q (want stable | beta)", channel)
}

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

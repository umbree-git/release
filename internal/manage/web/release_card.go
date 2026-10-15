package web

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"umbree-release-r2-mirror/manifest"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

const unknownPlatform = "—"

var archiveName = regexp.MustCompile(`^umbreed?-([a-z0-9]+)-([a-z0-9_]+)\.zip$`)

type fileRow struct {
	Name     string
	URL      string
	Platform string
	Size     string
	SHA      string
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func platformOf(name string) string {
	if m := archiveName.FindStringSubmatch(name); m != nil {
		return m[1] + "/" + m[2]
	}
	return unknownPlatform
}

func fileRows(arts []register.Artifact, linkOf func(register.Artifact) string) []fileRow {
	var archives, signing []fileRow
	for _, a := range arts {
		name := path.Base(a.Key)
		row := fileRow{Name: name, URL: linkOf(a)}
		switch {
		case name == register.SumsName:
			row.Platform = "checksums"
			signing = append([]fileRow{row}, signing...)
		case strings.HasSuffix(name, ".minisig"):
			row.Platform = "signature"
			signing = append(signing, row)
		default:
			row.Platform, row.Size, row.SHA = platformOf(name), humanSize(a.Size), a.SHA256
			archives = append(archives, row)
		}
	}
	return append(archives, signing...)
}

func (s *Server) publicURL(rv store.ReleaseVersion, key string) string {
	if key == "" {
		return ""
	}
	u, err := url.JoinPath(s.cfg.PublicBaseURL, publish.PublicKey(rv.Component, rv.Stamp, key))
	if err != nil {
		return ""
	}
	return u
}

func (s *Server) manifestURL(rv store.ReleaseVersion) string {
	u, err := url.JoinPath(s.cfg.PublicBaseURL, manifest.Key(rv.Component+"/"))
	if err != nil {
		return ""
	}
	return u
}

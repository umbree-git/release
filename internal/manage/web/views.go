package web

import (
	"encoding/json"
	"net/url"
	"path"
	"time"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

type link struct {
	Name string
	URL  string
}

type rowView struct {
	ID        int64
	Version   string
	Stamp     string
	State     string
	IsCurrent bool
	Created   string
	Promoted  string
	Yanked    string
	Expired   string
	Links     []link
}

type navItem struct {
	Name   string
	Path   string
	Active bool
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04Z")
}

func (s *Server) view(rv store.ReleaseVersion) rowView {
	return rowView{
		ID: rv.ID, Version: rv.Version, Stamp: rv.Stamp, State: rv.State, IsCurrent: rv.IsCurrent,
		Created: stamp(rv.CreatedAt), Promoted: stamp(rv.PromotedAt), Yanked: stamp(rv.YankedAt), Expired: stamp(rv.ExpiredAt),
		Links: s.links(rv),
	}
}

func (s *Server) links(rv store.ReleaseVersion) []link {
	if rv.State != catalog.StatePublic {
		return nil
	}
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil {
		s.cfg.Log.Warn("unreadable artifact list", "row", rv.ID, "err", err)
		return nil
	}
	out := make([]link, 0, len(arts))
	for _, a := range arts {
		u, err := url.JoinPath(s.cfg.PublicBaseURL, publish.PublicKey(rv.Component, rv.Stamp, a.Key))
		if err != nil {
			continue
		}
		out = append(out, link{Name: path.Base(a.Key), URL: u})
	}
	return out
}

func componentNav(channel, current string, at func(channel, component string) string) []navItem {
	out := make([]navItem, 0, len(catalog.Components))
	for _, comp := range catalog.Components {
		out = append(out, navItem{Name: comp, Path: at(channel, comp), Active: comp == current})
	}
	return out
}

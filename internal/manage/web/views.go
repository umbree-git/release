package web

import (
	"encoding/json"
	"time"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

type link struct {
	Name string
	URL  string
}

type rowView struct {
	ID         int64
	Version    string
	Stamp      string
	State      string
	IsCurrent  bool
	Created    string
	Promoted   string
	Yanked     string
	Expired    string
	CutAt      *when
	PromotedAt *when
	YankedAt   *when
	ExpiredAt  *when
	Artifacts  int
	CanPromote bool
	CanYank    bool
	Links      []link
	Files      []fileRow
	SumsURL    string
	SigURL     string
	Manifest   string
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
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

type when struct {
	ISO  string
	Full string
	Day  string
}

type actionForm struct {
	ID      int64
	Version string
	CSRF    string
}

func (v rowView) Form(csrf string) actionForm {
	return actionForm{ID: v.ID, Version: v.Version, CSRF: csrf}
}

func whenOf(t time.Time) *when {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &when{ISO: u.Format(time.RFC3339), Full: stamp(u), Day: u.Format("2006-01-02")}
}

func (s *Server) view(rv store.ReleaseVersion) rowView {
	v := rowView{
		ID: rv.ID, Version: rv.Version, Stamp: rv.Stamp, State: rv.State, IsCurrent: rv.IsCurrent,
		Created: stamp(rv.CreatedAt), Promoted: stamp(rv.PromotedAt), Yanked: stamp(rv.YankedAt), Expired: stamp(rv.ExpiredAt),
		CutAt: whenOf(rv.CreatedAt), PromotedAt: whenOf(rv.PromotedAt), YankedAt: whenOf(rv.YankedAt), ExpiredAt: whenOf(rv.ExpiredAt),
	}
	var arts []register.Artifact
	if err := json.Unmarshal([]byte(rv.ArtifactsJSON), &arts); err != nil {
		s.cfg.Log.Warn("unreadable artifact list", "row", rv.ID, "err", err)
		return v
	}
	v.Artifacts = len(arts)
	linkable := isLinkable(rv)
	v.Files = fileRows(arts, func(a register.Artifact) string {
		if !linkable {
			return ""
		}
		return s.publicURL(rv, a.Key)
	})
	if !linkable {
		return v
	}
	for _, f := range v.Files {
		if f.URL != "" {
			v.Links = append(v.Links, link{Name: f.Name, URL: f.URL})
		}
	}
	v.SumsURL, v.SigURL = s.publicURL(rv, rv.SumsKey), s.publicURL(rv, rv.MinisigKey)
	if rv.IsCurrent {
		v.Manifest = s.manifestURL(rv)
	}
	return v
}

func isLinkable(rv store.ReleaseVersion) bool {
	return rv.State == catalog.StatePublic && rv.PublicPrunedAt.IsZero() && rv.PublicPruningAt.IsZero()
}

func componentNav(channel, current string, at func(channel, component string) string) []navItem {
	out := make([]navItem, 0, len(catalog.Components))
	for _, comp := range catalog.Components {
		out = append(out, navItem{Name: comp, Path: at(channel, comp), Active: comp == current})
	}
	return out
}

func pageTabs(channel, comp string, history bool) []navItem {
	return []navItem{
		{Name: "Overview", Path: pagePath(channel, comp), Active: !history},
		{Name: "History", Path: historyPath(channel, comp), Active: history},
	}
}

func channelSwitch(channel, comp string, history bool) []navItem {
	at := pagePath
	if history {
		at = historyPath
	}
	return []navItem{
		{Name: "Production", Path: at(catalog.ChannelProduction, comp), Active: channel == catalog.ChannelProduction},
		{Name: "Beta", Path: at(channelBeta, comp), Active: channel == channelBeta},
	}
}

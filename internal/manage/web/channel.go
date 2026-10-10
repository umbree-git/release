package web

import (
	"net/http"

	"github.com/umbree-git/release/internal/manage/catalog"
)

func pagePath(channel, component string) string { return "/manage/" + channel + "/" + component }

func historyPath(channel, component string) string { return pagePath(channel, component) + "/history" }

func pageTarget(r *http.Request) (channel, component string, ok bool) {
	channel, component = r.PathValue("channel"), r.PathValue("component")
	return channel, component, catalog.ValidChannel(channel) && catalog.ValidComponent(component)
}

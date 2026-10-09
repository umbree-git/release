package publish

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"umbree-release-r2-mirror/manifest"
)

const (
	ConfirmLive       = "live"
	ConfirmNotYet     = "not yet"
	ConfirmCannotTell = "cannot tell"
)

func (r *Run) confirm(ctx context.Context, st *stream) {
	if r.d.Confirm == nil || r.d.Confirm.Fetcher == nil {
		return
	}
	url := strings.TrimRight(r.d.Confirm.BaseURL, "/") + "/" + manifest.Key(r.row.Component+"/")
	status, body, err := r.d.Confirm.Fetcher.Get(ctx, url)
	var m manifest.Manifest
	switch {
	case err != nil:
		st.send(Event{Step: "confirm", Key: url, Status: ConfirmCannotTell, Message: err.Error()})
	case status != http.StatusOK || json.Unmarshal(body, &m) != nil:
		st.send(Event{Step: "confirm", Key: url, Status: ConfirmCannotTell, Message: http.StatusText(status)})
	case m.Stamp == r.row.Stamp:
		st.send(Event{Step: "confirm", Key: url, Status: ConfirmLive, Message: "the public manifest names " + m.Stamp})
	default:
		st.send(Event{Step: "confirm", Key: url, Status: ConfirmNotYet, Message: "the public manifest still names " + m.Stamp})
	}
}

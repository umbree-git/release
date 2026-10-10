package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"

	"github.com/umbree-git/release/internal/manage/publish"
	"github.com/umbree-git/release/internal/manage/store"
)

const logMarker = `<ol id="log" class="log"></ol>`

type progressPage struct {
	pageData
	Row       rowView
	Component string
	Channel   string
	Verb      string
	Back      string
}

type progressLog struct {
	w        io.Writer
	flusher  http.Flusher
	pending  []byte
	terminal *publish.Event
}

func newProgressLog(w io.Writer) *progressLog {
	l := &progressLog{w: w}
	if f, ok := w.(http.Flusher); ok {
		l.flusher = f
	}
	return l
}

func (l *progressLog) Write(p []byte) (int, error) {
	l.pending = append(l.pending, p...)
	for {
		i := bytes.IndexByte(l.pending, '\n')
		if i < 0 {
			break
		}
		l.line(l.pending[:i])
		l.pending = l.pending[i+1:]
	}
	if l.flusher != nil {
		l.flusher.Flush()
	}
	return len(p), nil
}

func (l *progressLog) line(raw []byte) {
	var e publish.Event
	if err := json.Unmarshal(raw, &e); err != nil || e.Step == "" {
		fmt.Fprintf(l.w, `<li class="ev ev-text">%s</li>`, html.EscapeString(strings.TrimSpace(string(raw))))
		return
	}
	if (e.Step == "done" || e.Step == "error") && l.terminal == nil {
		l.terminal = &e
	}
	text := e.Step
	for _, part := range []string{e.Key, e.Status, e.Message} {
		if part != "" {
			text += " · " + part
		}
	}
	fmt.Fprintf(l.w, `<li class="ev ev-%s" data-step="%s" data-status="%s">%s</li>`,
		html.EscapeString(e.Step), html.EscapeString(e.Step), html.EscapeString(e.Status), html.EscapeString(text))
}

func (l *progressLog) result(action string) string {
	switch {
	case l.terminal == nil:
		return fmt.Sprintf(`<p class="result error" data-result="failed">✗ The %s stream ended without a terminal event. Treat it as failed and read the history before acting again.</p>`, html.EscapeString(action))
	case l.terminal.Step == "error":
		return fmt.Sprintf(`<p class="result error" data-result="failed">✗ %s failed: %s</p>`, html.EscapeString(action), html.EscapeString(l.terminal.Message))
	}
	return fmt.Sprintf(`<p class="result ok" data-result="done">✓ %s</p>`, html.EscapeString(l.terminal.Message))
}

func (s *Server) progressHalves(w http.ResponseWriter, r *http.Request, sess *store.Session, action string, row *store.ReleaseVersion) (string, string, error) {
	page := progressPage{Row: s.view(*row), Component: row.Component, Channel: row.Channel, Verb: action,
		Back: pagePath(row.Channel, row.Component)}
	page.pageData = s.consolePage(w, r, sess, action+" "+row.Component)
	var buf bytes.Buffer
	if err := s.pages["progress"].ExecuteTemplate(&buf, "layout", page); err != nil {
		return "", "", fmt.Errorf("render progress: %w", err)
	}
	head, tail, found := strings.Cut(buf.String(), logMarker)
	if !found {
		return "", "", fmt.Errorf("the progress template lost its log marker")
	}
	return head, tail, nil
}

func (s *Server) runPage(w http.ResponseWriter, r *http.Request, sess *store.Session, action string, row *store.ReleaseVersion) {
	run, status, msg := begin(r.Context(), s.cfg.Publish, row.ID, sess.Admin)
	if run == nil {
		http.Error(w, msg, status)
		return
	}
	defer run.Close()
	head, tail, err := s.progressHalves(w, r, sess, action, row)
	if err != nil {
		s.pageError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, head, `<ol id="log" class="log">`)
	lw := newProgressLog(w)
	do := (*publish.Run).Promote
	if action == actionYank {
		do = (*publish.Run).Yank
	}
	_ = do(run, r.Context(), lw)
	fmt.Fprint(w, "</ol>", lw.result(action), tail)
}

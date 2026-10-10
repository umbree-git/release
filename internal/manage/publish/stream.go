package publish

import (
	"encoding/json"
	"io"
	"net/http"
)

type Event struct {
	Step    string `json:"step"`
	Key     string `json:"key,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
	Row     int64  `json:"row,omitempty"`
}

type stream struct {
	enc      *json.Encoder
	flusher  http.Flusher
	finished bool
}

func newStream(w io.Writer) *stream {
	if w == nil {
		w = io.Discard
	}
	s := &stream{enc: json.NewEncoder(w)}
	if f, ok := w.(http.Flusher); ok {
		s.flusher = f
	}
	return s
}

func (s *stream) send(e Event) {
	if s.finished {
		return
	}
	_ = s.enc.Encode(e)
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

func (s *stream) finish(err error, row int64, done string) {
	if err != nil {
		s.send(Event{Step: "error", Row: row, Message: err.Error()})
	} else {
		s.send(Event{Step: "done", Row: row, Message: done})
	}
	s.finished = true
}

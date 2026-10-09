package web

import "io"

type progressLog struct{}

func newProgressLog(w io.Writer) *progressLog { return &progressLog{} }

func (l *progressLog) Write(p []byte) (int, error) { return len(p), nil }

func (l *progressLog) result(action string) string { return "" }

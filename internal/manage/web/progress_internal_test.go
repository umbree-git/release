package web

import (
	"strings"
	"testing"
)

const progressEvents = `{"step":"promote","status":"start","row":7,"message":"umbree v1"}
{"step":"verify","key":"umbree/production/v1/a.zip","bytes":3,"status":"ok"}
{"step":"copy","key":"umbree/v1/a.zip","bytes":3,"status":"ok"}
{"step":"manifest","key":"umbree/latest.json","status":"ok","message":"names v1"}
`

func feed(t *testing.T, stream string, chunk int) (string, string) {
	t.Helper()
	var out strings.Builder
	l := newProgressLog(&out)
	for i := 0; i < len(stream); i += chunk {
		end := min(i+chunk, len(stream))
		if _, err := l.Write([]byte(stream[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	return out.String(), l.result("promote")
}

func TestProgressRendersTerminalEvent(t *testing.T) {
	for _, chunk := range []int{1, 7, 1 << 20} {
		log, result := feed(t, progressEvents+`{"step":"done","row":7,"message":"umbree v1 is live on production"}`+"\n", chunk)
		if strings.Count(log, "<li ") != 5 || !strings.Contains(log, `data-step="copy"`) || !strings.Contains(log, "umbree/v1/a.zip") {
			t.Fatalf("chunk %d: log %s", chunk, log)
		}
		if !strings.Contains(result, `data-result="done"`) || !strings.Contains(result, "is live on production") {
			t.Fatalf("chunk %d: result %s", chunk, result)
		}
		_, failed := feed(t, progressEvents+`{"step":"error","row":7,"message":"copy <x>: boom"}`+"\n", chunk)
		if !strings.Contains(failed, `data-result="failed"`) || !strings.Contains(failed, "copy &lt;x&gt;: boom") {
			t.Fatalf("chunk %d: error result %s", chunk, failed)
		}
	}
}

func TestProgressMissingTerminalShowsFailed(t *testing.T) {
	log, result := feed(t, progressEvents+`{"step":"flip","status":"ok","row":7}`+"\n", 5)
	if !strings.Contains(log, `data-step="flip"`) {
		t.Fatalf("log %s", log)
	}
	if !strings.Contains(result, `data-result="failed"`) || strings.Contains(result, `data-result="done"`) {
		t.Fatalf("a stream with no terminal event rendered %s", result)
	}
	_, truncated := feed(t, progressEvents+`{"step":"do`, 3)
	if !strings.Contains(truncated, `data-result="failed"`) {
		t.Fatalf("a stream cut mid-line rendered %s", truncated)
	}
}

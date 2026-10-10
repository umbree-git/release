package publish_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/umbree-git/release/internal/manage/publish"
)

func TestBeginRequiresActor(t *testing.T) {
	w := newWorld(t)
	id, _ := w.stage("0.1.0", 1)
	for _, actor := range []string{"", "   "} {
		if r, err := publish.Begin(context.Background(), w.d, id, actor); !errors.Is(err, publish.ErrNoActor) {
			if r != nil {
				r.Close()
			}
			t.Fatalf("Begin with actor %q: %v, want ErrNoActor", actor, err)
		}
		var buf bytes.Buffer
		if err := publish.Promote(context.Background(), w.d, id, actor, &buf); !errors.Is(err, publish.ErrNoActor) {
			t.Fatalf("Promote with actor %q: %v", actor, err)
		}
		if terminal(t, events(t, buf.Bytes())).Step != "error" {
			t.Fatalf("no terminal error event: %s", buf.String())
		}
	}
	if w.public.Count("PUT")+w.public.Count("COPY") != 0 || w.row(id).State != "staged" {
		t.Fatal("an actorless promote reached the public store or the row")
	}
	if err, ev := w.promote(id); err != nil {
		t.Fatalf("control, with an actor: %v %+v", err, ev)
	}
	log, _ := w.st.AuditLog()
	if len(log) != 1 || log[0].Action != "promote" || log[0].Actor != "test-operator" || log[0].RowID != id {
		t.Fatalf("audit %+v", log)
	}
}

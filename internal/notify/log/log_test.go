package log

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
)

func TestNotify(t *testing.T) {
	var buf bytes.Buffer
	n := New(slog.New(slog.NewTextHandler(&buf, nil)))
	if err := n.Notify(context.Background(), ports.Notification{Title: "ready to merge", Body: "PR 7", Priority: ports.PriorityHigh, Group: "ws_1"}); err != nil {
		t.Fatal(err)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, `msg="ready to merge"`) || !strings.Contains(out, "body=\"PR 7\"") || !strings.Contains(out, "group=ws_1") {
		t.Errorf("line = %s", out)
	}
	buf.Reset()
	_ = n.Notify(context.Background(), ports.Notification{Title: "x"})
	if !strings.Contains(buf.String(), "level=INFO") {
		t.Errorf("default priority = %s", buf.String())
	}
}

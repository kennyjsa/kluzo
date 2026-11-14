package gates

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kennyjsa/kluzo/internal/platform/redact"
)

// sink receives the output of a gate command as it arrives: each line,
// redacted, goes to the log (up to logMax bytes), to the head and end kept for the
// summary, and to the reader of go test -json events.
type sink struct {
	log     io.Writer
	logMax  int
	logged  int
	rd      *redact.Redactor
	pending []byte
	text    headTail
	tests   goTests
}

func newSink(log io.Writer, rd *redact.Redactor, logMax int) *sink {
	return &sink{log: log, logMax: logMax, rd: rd, tests: goTests{outputs: map[testKey]*bytes.Buffer{}}}
}

// Write implements io.Writer; it never fails, so the command never blocks
// on its output.
func (s *sink) Write(b []byte) (int, error) {
	s.pending = append(s.pending, b...)
	for {
		i := bytes.IndexByte(s.pending, '\n')
		if i < 0 {
			break
		}
		s.line(s.pending[:i+1])
		s.pending = s.pending[i+1:]
	}
	if len(s.pending) > lineMax {
		s.line(s.pending)
		s.pending = nil
	}
	return len(b), nil
}

// flush takes the last line, without a newline.
func (s *sink) flush() {
	if len(s.pending) > 0 {
		s.line(append(s.pending, '\n'))
		s.pending = nil
	}
}

func (s *sink) line(raw []byte) {
	l := s.rd.String(string(raw))
	switch {
	case s.logged+len(l) <= s.logMax:
		_, _ = io.WriteString(s.log, l)
		s.logged += len(l)
	case s.logged <= s.logMax:
		_, _ = fmt.Fprintf(s.log, "\n[kluzo] the log stops at %d bytes\n", s.logMax)
		s.logged = s.logMax + 1
	}
	var ev testEvent
	if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &ev) == nil && ev.Action != "" {
		s.text.add(ev.Output)
		s.tests.add(ev)
		return
	}
	s.text.add(l)
}

// headTail keeps the start and the end of a long text.
type headTail struct {
	head    strings.Builder
	tail    []byte
	dropped int
}

func (h *headTail) add(s string) {
	if n := summaryHead - h.head.Len(); n > 0 {
		take := min(n, len(s))
		h.head.WriteString(s[:take])
		s = s[take:]
	}
	h.tail = append(h.tail, s...)
	if over := len(h.tail) - summaryEnd; over > summaryEnd {
		h.dropped += over
		h.tail = append(h.tail[:0], h.tail[over:]...)
	}
}

// String is the text, with a mark where the middle was left out. The cuts are
// by bytes; a character cut in two is dropped.
func (h *headTail) String() string {
	tail := h.tail
	if over := len(tail) - summaryEnd; over > 0 {
		h.dropped += over
		tail = tail[over:]
	}
	if h.dropped == 0 {
		return strings.ToValidUTF8(h.head.String()+string(tail), "")
	}
	return strings.ToValidUTF8(h.head.String(), "") + fmt.Sprintf("\n…(%d bytes omitted)…\n", h.dropped) + strings.ToValidUTF8(string(tail), "")
}

// clip cuts s at max characters.
func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…(truncated)"
}

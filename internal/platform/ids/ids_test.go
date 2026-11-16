package ids

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestEncodeKnownValues(t *testing.T) {
	var zero, max [16]byte
	for i := range max {
		max[i] = 0xFF
	}
	if got := encode(zero); got != strings.Repeat("0", 26) {
		t.Errorf("encode(zero) = %q", got)
	}
	if got, want := encode(max), "7"+strings.Repeat("Z", 25); got != want {
		t.Errorf("encode(max) = %q, want %q", got, want)
	}
}

func TestTimestampPart(t *testing.T) {
	// Example from the ULID specification: 1469918176385 ms → "01ARYZ6S41".
	g := NewGenerator(bytes.NewReader(make([]byte, 10)))
	id, err := g.New(Event, time.UnixMilli(1469918176385))
	if err != nil {
		t.Fatal(err)
	}
	if want := "ev_01ARYZ6S41"; !strings.HasPrefix(id, want) {
		t.Fatalf("id = %q, want prefix %q", id, want)
	}
	if len(id) != len("ev_")+26 {
		t.Fatalf("id length = %d, want %d", len(id), len("ev_")+26)
	}
}

func TestMonotonicWithinMillisecond(t *testing.T) {
	g := NewGenerator(nil)
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	prev := ""
	for i := 0; i < 1000; i++ {
		id, err := g.New(Node, now)
		if err != nil {
			t.Fatal(err)
		}
		if id <= prev {
			t.Fatalf("id %q is not greater than previous %q", id, prev)
		}
		prev = id
	}
}

func TestSortableAcrossTime(t *testing.T) {
	g := NewGenerator(nil)
	t0 := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	a, _ := g.New(Workspace, t0)
	b, _ := g.New(Workspace, t0.Add(time.Millisecond))
	if a >= b {
		t.Fatalf("expected %q < %q", a, b)
	}
}

func TestOverflowWithinMillisecond(t *testing.T) {
	g := NewGenerator(bytes.NewReader(bytes.Repeat([]byte{0xFF}, 10)))
	now := time.UnixMilli(1)
	if _, err := g.New(Node, now); err != nil {
		t.Fatal(err)
	}
	if _, err := g.New(Node, now); err == nil {
		t.Fatal("expected overflow error on second ID in the same millisecond")
	}
}

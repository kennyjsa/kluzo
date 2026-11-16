package clock

import (
	"testing"
	"time"
)

func TestSystemIsUTC(t *testing.T) {
	if loc := (System{}).Now().Location(); loc != time.UTC {
		t.Fatalf("System.Now location = %v, want UTC", loc)
	}
}

func TestManualAdvance(t *testing.T) {
	start := time.Date(2026, 11, 7, 12, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	c := NewManual(start)
	if got := c.Now(); !got.Equal(start) || got.Location() != time.UTC {
		t.Fatalf("Now = %v, want %v in UTC", got, start)
	}
	c.Advance(90 * time.Second)
	if got, want := c.Now(), start.Add(90*time.Second); !got.Equal(want) {
		t.Fatalf("after Advance, Now = %v, want %v", got, want)
	}
}

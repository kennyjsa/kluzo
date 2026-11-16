// Package clock provides the time source used outside the deterministic core.
//
// The core never reads the clock: the supervisor reads it here and passes the
// timestamp inside each input (docs/architecture/patterns.md, pattern 2).
package clock

import (
	"sync"
	"time"
)

// Clock returns the current time.
type Clock interface {
	Now() time.Time
}

// System is the real wall clock, always in UTC.
type System struct{}

// Now returns the current UTC time.
func (System) Now() time.Time { return time.Now().UTC() }

// Manual is a controllable clock for tests.
type Manual struct {
	mu  sync.Mutex
	now time.Time
}

// NewManual returns a Manual clock set to t (converted to UTC).
func NewManual(t time.Time) *Manual { return &Manual{now: t.UTC()} }

// Now returns the clock's current time.
func (m *Manual) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// Advance moves the clock forward by d.
func (m *Manual) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}

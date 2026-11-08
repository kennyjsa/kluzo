package fakes

import (
	"context"
	"fmt"
	"sync"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// CI is an in-memory ports.CI.
type CI struct {
	mu     sync.Mutex
	checks map[string][]ports.CheckRun
}

var _ ports.CI = (*CI)(nil)

// NewCI returns a CI with no reports.
func NewCI() *CI { return &CI{checks: map[string][]ports.CheckRun{}} }

// Report records the latest state of a check on a commit, replacing an earlier
// report of the same name.
func (c *CI) Report(sha string, run ports.CheckRun) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, r := range c.checks[sha] {
		if r.Name == run.Name {
			c.checks[sha][i] = run
			return
		}
	}
	c.checks[sha] = append(c.checks[sha], run)
}

// Checks implements ports.CI.
func (c *CI) Checks(_ context.Context, sha string, required []string) ([]ports.CheckRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return ports.SelectChecks(c.checks[sha], required), nil
}

// LogURL implements ports.CI.
func (c *CI) LogURL(_ context.Context, check ports.CheckRun) (string, error) {
	if check.URL == "" {
		return "", fmt.Errorf("check %s has no log: %w", check.Name, ports.ErrNotFound)
	}
	return check.URL, nil
}

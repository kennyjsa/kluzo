// Package log implements ports.Notifier on the log of Kluzo: the notifier of
// v0.1, before ntfy (v0.3). Each notification is one structured line a person
// reads in the output of kluzo run.
package log

import (
	"context"
	"log/slog"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// Notifier writes notifications to a logger.
type Notifier struct{ log *slog.Logger }

var _ ports.Notifier = (*Notifier)(nil)

// New returns the notifier on l.
func New(l *slog.Logger) *Notifier { return &Notifier{log: l} }

// Notify implements ports.Notifier: high priority goes out as a warning.
func (n *Notifier) Notify(ctx context.Context, m ports.Notification) error {
	level := slog.LevelInfo
	if m.Priority == ports.PriorityHigh {
		level = slog.LevelWarn
	}
	n.log.Log(ctx, level, m.Title, "body", m.Body, "url", m.URL, "group", m.Group, "kind", "notification")
	return nil
}

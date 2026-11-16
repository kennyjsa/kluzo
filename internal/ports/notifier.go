package ports

import "context"

// Notifier tells a person that something needs them (docs/contracts/interfaces.md).
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// Notification is one message to a person.
type Notification struct {
	Title, Body, URL string
	Priority         string // "low" | "default" | "high" (it blocks work)
	Group            string // groups bursts
}

// Notification priorities.
const (
	PriorityLow     = "low"
	PriorityDefault = "default"
	PriorityHigh    = "high"
)

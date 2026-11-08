package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// tsLayout is the journal timestamp format: UTC, millisecond precision
// (docs/contracts/events.md).
const tsLayout = "2006-01-02T15:04:05.000Z"

func formatTS(t time.Time) string { return t.UTC().Format(tsLayout) }

// Header identifies a journal entry. The id and the time come from the caller
// (platform/ids and platform/clock), so the store never reads the clock.
type Header struct {
	ID string // ev_<ulid>
	TS time.Time
}

// Event is one journal entry.
type Event struct {
	Header
	Seq       int64 // assigned by the journal; zero when appending
	Kind      string
	NodeID    string // empty for workspace events
	SessionID string
	Payload   json.RawMessage
}

// EffectSpec asks for an external effect to be queued with the event that
// causes it (ADR 0013).
type EffectSpec struct {
	ID      string // ef_<ulid>
	Kind    string // 'tracker.set_label', 'tracker.comment', 'session.start', …
	Key     string // identifies the effect within its event
	Payload json.RawMessage
}

// IdemKey is the idempotency key adapters use to detect a repeated execution:
// it derives from the event that caused the effect.
func IdemKey(eventID, key string) string { return eventID + "/" + key }

// Append writes the event to the journal, applies it to the projections and
// queues the effects, all in this transaction. It returns the event's seq.
// An event id already in the journal is rejected with ErrDuplicateEvent, which
// makes a replayed step harmless.
func (t *Tx) Append(e Event, effects ...EffectSpec) (int64, error) {
	var exists int
	err := t.tx.QueryRowContext(t.ctx, `SELECT COUNT(1) FROM events WHERE id = ?`, e.ID).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("check event %s: %w", e.ID, err)
	}
	if exists > 0 {
		return 0, fmt.Errorf("%w: %s", ErrDuplicateEvent, e.ID)
	}
	res, err := t.tx.ExecContext(t.ctx,
		`INSERT INTO events (id, ts, kind, node_id, session_id, payload) VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?)`,
		e.ID, formatTS(e.TS), e.Kind, e.NodeID, e.SessionID, string(e.Payload))
	if err != nil {
		return 0, fmt.Errorf("append %s: %w", e.Kind, err)
	}
	e.Seq, err = res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := t.project(e); err != nil {
		return 0, fmt.Errorf("project %s (%s): %w", e.Kind, e.ID, err)
	}
	for _, f := range effects {
		_, err := t.tx.ExecContext(t.ctx,
			`INSERT INTO effects (id, idem_key, event_seq, kind, payload, state, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?)`,
			f.ID, IdemKey(e.ID, f.Key), e.Seq, f.Kind, string(f.Payload), formatTS(e.TS))
		if err != nil {
			return 0, fmt.Errorf("queue effect %s of %s: %w", f.Key, e.ID, err)
		}
	}
	return e.Seq, nil
}

// queryEvents reads up to limit entries with seq greater than after, in order.
// A limit of zero or less means no limit.
func queryEvents(ctx context.Context, q querier, after int64, limit int) ([]Event, error) {
	query := `SELECT seq, id, ts, kind, COALESCE(node_id, ''), COALESCE(session_id, ''), payload FROM events WHERE seq > ? ORDER BY seq`
	args := []any{after}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	var out []Event
	err := each(ctx, q, query, args, func(rows *sql.Rows) error {
		var e Event
		var ts, payload string
		if err := rows.Scan(&e.Seq, &e.ID, &ts, &e.Kind, &e.NodeID, &e.SessionID, &payload); err != nil {
			return err
		}
		var err error
		if e.TS, err = time.Parse(tsLayout, ts); err != nil {
			return fmt.Errorf("event %s: bad timestamp %q: %w", e.ID, ts, err)
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	return out, nil
}

// Events returns up to limit journal entries with seq greater than after.
func (d *DB) Events(ctx context.Context, after int64, limit int) ([]Event, error) {
	return queryEvents(ctx, d.sql, after, limit)
}

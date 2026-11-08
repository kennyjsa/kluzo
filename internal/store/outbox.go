package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// EffectState is the state of a queued effect.
type EffectState string

// Effect states (docs/contracts/data-model.md).
const (
	EffectPending EffectState = "pending"
	EffectDone    EffectState = "done"
	EffectFailed  EffectState = "failed"
)

// Effect is a row of the outbox.
type Effect struct {
	ID        string
	IdemKey   string
	EventSeq  int64
	Kind      string
	Payload   json.RawMessage
	State     EffectState
	Attempts  int
	NextAt    time.Time // zero when it can run now
	LastError string
}

// PendingEffects returns the effects that can run at now, oldest event first.
// An effect that was started but not completed (the process died in between)
// is still pending: running it again is safe because adapters deduplicate on
// IdemKey (ADR 0013).
func (d *DB) PendingEffects(ctx context.Context, now time.Time, limit int) ([]Effect, error) {
	var out []Effect
	err := each(ctx, d.sql,
		`SELECT id, idem_key, event_seq, kind, payload, state, attempts, next_at, COALESCE(last_error, '')
		   FROM effects WHERE state = 'pending' AND (next_at IS NULL OR next_at <= ?)
		  ORDER BY event_seq, id LIMIT ?`, []any{formatTS(now), limit},
		func(rows *sql.Rows) error {
			var f Effect
			var payload, state string
			var nextAt sql.NullString
			if err := rows.Scan(&f.ID, &f.IdemKey, &f.EventSeq, &f.Kind, &payload, &state, &f.Attempts, &nextAt, &f.LastError); err != nil {
				return err
			}
			f.Payload, f.State = json.RawMessage(payload), EffectState(state)
			if nextAt.Valid {
				var err error
				if f.NextAt, err = time.Parse(tsLayout, nextAt.String); err != nil {
					return fmt.Errorf("effect %s: bad next_at: %w", f.ID, err)
				}
			}
			out = append(out, f)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read outbox: %w", err)
	}
	return out, nil
}

// CompleteEffect marks an effect as done.
func (d *DB) CompleteEffect(ctx context.Context, id string, at time.Time) error {
	return d.updateEffect(ctx, id, `UPDATE effects SET state = 'done', attempts = attempts + 1, done_at = ?, next_at = NULL WHERE id = ? AND state = 'pending'`, formatTS(at))
}

// RetryEffect records a failed attempt and schedules the next one.
func (d *DB) RetryEffect(ctx context.Context, id, lastError string, nextAt time.Time) error {
	return d.updateEffect(ctx, id, `UPDATE effects SET attempts = attempts + 1, last_error = ?, next_at = ? WHERE id = ? AND state = 'pending'`, lastError, formatTS(nextAt))
}

// FailEffect gives up on an effect after its last attempt.
func (d *DB) FailEffect(ctx context.Context, id, lastError string) error {
	return d.updateEffect(ctx, id, `UPDATE effects SET state = 'failed', attempts = attempts + 1, last_error = ?, next_at = NULL WHERE id = ? AND state = 'pending'`, lastError)
}

// updateEffect runs an update whose last argument placeholder is the effect id.
func (d *DB) updateEffect(ctx context.Context, id, query string, args ...any) error {
	res, err := d.sql.ExecContext(ctx, query, append(args, id)...)
	if err != nil {
		return fmt.Errorf("update effect %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: pending effect %s", ErrNotFound, id)
	}
	return nil
}

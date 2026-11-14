package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	return d.effects(ctx, `WHERE state = 'pending' AND (next_at IS NULL OR next_at <= ?) ORDER BY event_seq, id LIMIT ?`, formatTS(now), limit)
}

// FailedEffects returns the effects that gave up, oldest event first.
func (d *DB) FailedEffects(ctx context.Context) ([]Effect, error) {
	return d.effects(ctx, `WHERE state = 'failed' ORDER BY event_seq, id`)
}

// effects reads the rows of the outbox that where selects.
func (d *DB) effects(ctx context.Context, where string, args ...any) ([]Effect, error) {
	var out []Effect
	err := each(ctx, d.sql,
		`SELECT id, idem_key, event_seq, kind, payload, state, attempts, next_at, COALESCE(last_error, '') FROM effects `+where, args,
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

// BeginEffect records that a run of the effect starts and returns its number
// (1 for the first). It is written before the run, so a run the process did
// not finish still counts: the policy's limit and a session's harness counter
// never repeat a number.
func (d *DB) BeginEffect(ctx context.Context, id string) (int, error) {
	var n int
	err := d.sql.QueryRowContext(ctx, `UPDATE effects SET attempts = attempts + 1 WHERE id = ? AND state = 'pending' RETURNING attempts`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: pending effect %s", ErrNotFound, id)
	}
	if err != nil {
		return 0, fmt.Errorf("begin effect %s: %w", id, err)
	}
	return n, nil
}

// RetryEffect records why the run failed and schedules the next one, in the
// transaction that stores what the failed run cost.
func (t *Tx) RetryEffect(id, lastError string, nextAt time.Time) error {
	return updateEffect(t.ctx, t.tx, id, `UPDATE effects SET last_error = ?, next_at = ? WHERE id = ? AND state = 'pending'`, lastError, formatTS(nextAt))
}

// CompleteEffect marks an effect as done, in the transaction that stores what
// its run produced: a restart never runs it again after that.
func (t *Tx) CompleteEffect(id string, at time.Time) error {
	return updateEffect(t.ctx, t.tx, id, `UPDATE effects SET state = 'done', done_at = ?, next_at = NULL WHERE id = ? AND state = 'pending'`, formatTS(at))
}

// FollowEffect queues effects that the run of the effect id caused, in the
// transaction that completes it: they hang on the event of that effect, and
// their idempotency keys derive from its own.
func (t *Tx) FollowEffect(id string, at time.Time, effects ...EffectSpec) error {
	var seq int64
	var key string
	err := t.tx.QueryRowContext(t.ctx, `SELECT event_seq, idem_key FROM effects WHERE id = ?`, id).Scan(&seq, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: effect %s", ErrNotFound, id)
	}
	if err != nil {
		return fmt.Errorf("read effect %s: %w", id, err)
	}
	for _, f := range effects {
		if err := t.queue(f, IdemKey(key, f.Key), seq, at); err != nil {
			return fmt.Errorf("queue effect %s after %s: %w", f.Key, id, err)
		}
	}
	return nil
}

// FailEffect gives up on an effect, in the transaction that journals
// effect.failed.
func (t *Tx) FailEffect(id, lastError string) error {
	return updateEffect(t.ctx, t.tx, id, `UPDATE effects SET state = 'failed', last_error = ?, next_at = NULL WHERE id = ? AND state = 'pending'`, lastError)
}

// RequeueEffect returns a failed effect to pending, with its runs zeroed and
// the same idempotency key (kluzo effect retry), in the transaction that
// journals effect.retried. It returns the kind and the payload of the effect.
func (t *Tx) RequeueEffect(id string) (kind string, payload json.RawMessage, err error) {
	var raw string
	err = t.tx.QueryRowContext(t.ctx,
		`UPDATE effects SET state = 'pending', attempts = 0, next_at = NULL, last_error = NULL WHERE id = ? AND state = 'failed' RETURNING kind, payload`,
		id).Scan(&kind, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, fmt.Errorf("%w: failed effect %s", ErrNotFound, id)
	}
	if err != nil {
		return "", nil, fmt.Errorf("requeue effect %s: %w", id, err)
	}
	return kind, json.RawMessage(raw), nil
}

// execer is what updateEffect needs of a *sql.DB or a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// updateEffect runs an update whose last argument placeholder is the effect id.
func updateEffect(ctx context.Context, db execer, id, query string, args ...any) error {
	res, err := db.ExecContext(ctx, query, append(args, id)...)
	if err != nil {
		return fmt.Errorf("update effect %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: pending effect %s", ErrNotFound, id)
	}
	return nil
}

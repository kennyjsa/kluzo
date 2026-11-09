// Package store is the workspace database: an append-only journal (the source
// of truth), projections kept in the same transaction, and the outbox of
// idempotent effects (docs/contracts/data-model.md, ADRs 0013 and 0014).
//
// It is the only package that imports the SQLite driver. It holds no domain
// rules: state changes go through core's state machine (see transition.go).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // the driver; only this package may import it
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Errors returned by the store. Callers use errors.Is.
var (
	// ErrDuplicateEvent means an event with that id is already in the journal.
	ErrDuplicateEvent = errors.New("event already in the journal")
	// ErrStateMismatch means a transition's from-state is not the current state.
	ErrStateMismatch = errors.New("state does not match the transition")
	// ErrNotFound means the node or workspace does not exist.
	ErrNotFound = errors.New("not found")
	// ErrFutureSchema means the database was written by a newer kluzo.
	ErrFutureSchema = errors.New("database schema is newer than this binary")
)

// DB is one workspace database (state.db).
type DB struct{ sql *sql.DB }

// Open opens (creating it if needed) the database at path, with WAL, and
// applies the pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	q := url.Values{}
	q.Set("_txlock", "immediate") // take the write lock at BEGIN: no upgrade deadlocks
	for _, p := range []string{"journal_mode(WAL)", "synchronous(NORMAL)", "foreign_keys(1)", "busy_timeout(5000)"} {
		q.Add("_pragma", p)
	}
	u := url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}
	conn, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	conn.SetMaxOpenConns(1) // one writer per workspace (ADR 0014)
	d := &DB{sql: conn}
	if err := d.migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return d, nil
}

// Close releases the database.
func (d *DB) Close() error { return d.sql.Close() }

// migrate applies, in order, the embedded migrations newer than the database.
func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var current int
	if err := d.sql.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	files, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name())
	}
	sort.Strings(names)
	latest := 0
	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with its number", name)
		}
		latest = max(latest, version)
		if version <= current {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		err = d.Tx(ctx, func(t *Tx) error {
			if _, err := t.tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}
			_, err := t.tx.ExecContext(ctx, `INSERT INTO schema_migrations (version) VALUES (?)`, version)
			return err
		})
		if err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	if current > latest {
		return fmt.Errorf("%w: database at version %d, binary at %d", ErrFutureSchema, current, latest)
	}
	if current > 0 && current < latest {
		// A migration may add a projection: recompute them from the journal
		// the older binary wrote.
		return d.Rebuild(ctx)
	}
	return nil
}

// Tx is a write transaction. Everything done through it commits or rolls back
// together: the journal entry, the projection update and the queued effects.
type Tx struct {
	ctx context.Context
	tx  *sql.Tx
}

// Tx runs fn in a transaction, committing if it returns nil and rolling back
// otherwise (also on panic).
func (d *DB) Tx(ctx context.Context, fn func(*Tx) error) (err error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(&Tx{ctx: ctx, tx: tx}); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// querier is what *sql.DB and *sql.Tx share.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// each runs a query and calls scan for every row, closing the rows.
func each(ctx context.Context, q querier, query string, args []any, scan func(*sql.Rows) error) (err error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

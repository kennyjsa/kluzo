-- Journal: append-only. seq is the total order within the workspace.
CREATE TABLE events (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  id         TEXT NOT NULL UNIQUE,
  ts         TEXT NOT NULL,
  kind       TEXT NOT NULL,
  node_id    TEXT,
  session_id TEXT,
  payload    TEXT NOT NULL
);
CREATE INDEX events_node ON events(node_id, seq);

-- The journal is never rewritten (docs/contracts/data-model.md).
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events is append-only'); END;

-- Outbox (ADR 0013): written in the same transaction as the events.
CREATE TABLE effects (
  id         TEXT PRIMARY KEY,
  idem_key   TEXT NOT NULL UNIQUE,
  event_seq  INTEGER NOT NULL REFERENCES events(seq),
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  state      TEXT NOT NULL,
  attempts   INTEGER NOT NULL DEFAULT 0,
  next_at    TEXT,
  last_error TEXT,
  created_at TEXT NOT NULL,
  done_at    TEXT
);
CREATE INDEX effects_pending ON effects(state, next_at);

-- Projections: rebuilt from the journal by store.Rebuild.
CREATE TABLE workspace (
  id                TEXT PRIMARY KEY,
  state             TEXT NOT NULL,
  profile           TEXT,
  branch            TEXT NOT NULL,
  base_sha          TEXT NOT NULL,
  head_sha          TEXT,
  plan_version      INTEGER,
  flow_cursor       TEXT,
  budget_usd_micros INTEGER,
  cost_usd_micros   INTEGER NOT NULL DEFAULT 0,
  updated_at        TEXT NOT NULL
);

CREATE TABLE nodes (
  id              TEXT PRIMARY KEY,
  issue_ref       TEXT NOT NULL UNIQUE,
  title           TEXT NOT NULL,
  state           TEXT NOT NULL,
  prev_state      TEXT,
  blocked_reason  TEXT,
  areas           TEXT NOT NULL,
  branch          TEXT,
  worktree_path   TEXT,
  attempt         INTEGER NOT NULL DEFAULT 0,
  round           INTEGER NOT NULL DEFAULT 0,
  step_index      INTEGER,
  overrides       TEXT,
  budget          TEXT NOT NULL,
  cost_usd_micros INTEGER NOT NULL DEFAULT 0,
  critical        INTEGER NOT NULL DEFAULT 0,
  merged_sha      TEXT,
  updated_at      TEXT NOT NULL
);

CREATE TABLE edges (
  from_node TEXT NOT NULL REFERENCES nodes(id),
  to_node   TEXT NOT NULL REFERENCES nodes(id),
  kind      TEXT NOT NULL,
  source    TEXT NOT NULL,
  PRIMARY KEY (from_node, to_node, kind)
);

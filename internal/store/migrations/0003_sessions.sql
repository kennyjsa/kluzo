-- The sessions of the workspace, projected from session.started and
-- session.finished (docs/contracts/data-model.md). The cost of a session also
-- adds up in nodes.cost_usd_micros and workspace.cost_usd_micros.
CREATE TABLE sessions (
  id                  TEXT PRIMARY KEY,
  node_id             TEXT REFERENCES nodes(id),
  role                TEXT NOT NULL,
  harness             TEXT NOT NULL,
  model               TEXT NOT NULL,
  harness_session_ref TEXT,
  attempt             INTEGER,
  round               INTEGER,
  state               TEXT NOT NULL,
  result              TEXT,
  cost_usd_micros     INTEGER NOT NULL DEFAULT 0,
  started_at          TEXT NOT NULL,
  finished_at         TEXT
);
CREATE INDEX sessions_node ON sessions(node_id);

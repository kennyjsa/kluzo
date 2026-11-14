-- The runs of the gates, projected from gate.started (a row, running) and
-- gate.finished (its state and log), docs/contracts/data-model.md. The id is
-- the id of the gate.started event.
CREATE TABLE gate_runs (
  id          TEXT PRIMARY KEY,
  node_id     TEXT REFERENCES nodes(id),
  gate        TEXT NOT NULL,
  sha         TEXT NOT NULL,
  state       TEXT NOT NULL,
  log_ref     TEXT,
  started_at  TEXT NOT NULL,
  finished_at TEXT
);
CREATE INDEX gate_runs_node ON gate_runs(node_id, gate);

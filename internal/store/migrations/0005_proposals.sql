-- The proposals of the workspace, projected from proposal.created (a row,
-- pending) and proposal.decided (approved or rejected, by whom and when),
-- docs/contracts/data-model.md. An approved change is the exception the check
-- after the next implementer rounds honors (ADR 0020).
CREATE TABLE proposals (
  id          TEXT PRIMARY KEY,
  origin_node TEXT REFERENCES nodes(id),
  session_id  TEXT REFERENCES sessions(id),
  kind        TEXT NOT NULL,
  payload     TEXT NOT NULL,
  blocking    INTEGER NOT NULL,
  state       TEXT NOT NULL,
  decided_by  TEXT,
  created_at  TEXT NOT NULL,
  decided_at  TEXT
);
CREATE INDEX proposals_node ON proposals(origin_node, state);

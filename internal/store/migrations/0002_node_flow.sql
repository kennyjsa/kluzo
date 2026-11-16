-- The progress of a node through its flow (node.flow events), so a restart
-- continues from the last role that finished.
ALTER TABLE nodes ADD COLUMN flow TEXT;

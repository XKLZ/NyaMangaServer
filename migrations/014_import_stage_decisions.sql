-- Explicitly skipping a failed recommendation is an audited human decision,
-- separate from both worker outcomes and per-member organization confirmation.
ALTER TABLE import_batches ADD COLUMN stage_decisions_json TEXT NOT NULL DEFAULT '{}'
 CHECK(json_valid(stage_decisions_json) AND json_type(stage_decisions_json)='object');

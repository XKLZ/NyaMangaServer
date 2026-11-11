-- Keep legacy storage keys and decisions intact; matching identity is not a
-- globally unique row identity across import batches and standalone discovery.
ALTER TABLE series_proposals ADD COLUMN match_key TEXT NOT NULL DEFAULT '';
ALTER TABLE series_proposals ADD COLUMN scope_key TEXT NOT NULL DEFAULT 'global';
UPDATE series_proposals SET match_key=proposal_key,
 scope_key=COALESCE((SELECT 'import:'||batch_id FROM import_proposals
 WHERE proposal_id=series_proposals.id AND stage='series'),'global');
CREATE UNIQUE INDEX series_proposals_scope_match ON series_proposals(scope_key,match_key)
 WHERE match_key<>'';
CREATE TABLE series_match_feedback(
 library_id TEXT NOT NULL,semantic_key TEXT NOT NULL,
 decision TEXT NOT NULL CHECK(decision IN('dismissed','restored')),
 proposal_id TEXT NOT NULL REFERENCES series_proposals(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),
 legacy INTEGER NOT NULL DEFAULT 0 CHECK(legacy IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1,updated_at INTEGER NOT NULL,
 PRIMARY KEY(library_id,semantic_key)
);

-- Existing tasks keep their type, identity and checkpoints. Explicit mode prevents
-- the legacy per-Edition executor from claiming whole-library discovery work.
ALTER TABLE jobs ADD COLUMN mode TEXT NOT NULL DEFAULT 'edition' CHECK(mode IN('edition','library'));
CREATE UNIQUE INDEX discovery_one_active_library ON jobs(library_id) WHERE type='work_recommendation' AND mode='library' AND state IN('queued','running','paused');
CREATE TABLE work_discovery_runs(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 library_id TEXT NOT NULL,
 next_row INTEGER NOT NULL DEFAULT 0 CHECK(next_row>=0),
 edge_count INTEGER NOT NULL DEFAULT 0 CHECK(edge_count>=0),
 evidence_bytes INTEGER NOT NULL DEFAULT 0 CHECK(evidence_bytes>=0),
 refresh_required INTEGER NOT NULL DEFAULT 0 CHECK(refresh_required IN(0,1)),
 phase TEXT NOT NULL DEFAULT 'features' CHECK(phase IN('features','pairs','proposals','finished')),
 evidence_version TEXT NOT NULL,created_at INTEGER NOT NULL
);
CREATE TABLE work_pair_blocks(
 job_id TEXT NOT NULL REFERENCES work_discovery_runs(job_id) ON DELETE CASCADE,
 row_index INTEGER NOT NULL,hard_conflicts BLOB NOT NULL,
 PRIMARY KEY(job_id,row_index)
);
CREATE TABLE work_pair_evidence(
 job_id TEXT NOT NULL REFERENCES work_discovery_runs(job_id) ON DELETE CASCADE,
 a INTEGER NOT NULL,b INTEGER NOT NULL CHECK(b>a),tier TEXT NOT NULL CHECK(tier IN('strong','supplementary')),
 score REAL,signal_json TEXT NOT NULL,PRIMARY KEY(job_id,a,b)
);
CREATE TABLE work_proposals(
 id TEXT PRIMARY KEY,job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,library_id TEXT NOT NULL,
 proposal_key TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN('create_work','add_to_work')),
 target_work_id TEXT NOT NULL,tier TEXT NOT NULL CHECK(tier IN('strong','supplementary')),
 score REAL,primary_plan INTEGER NOT NULL CHECK(primary_plan IN(0,1)),
 selection_plan INTEGER NOT NULL DEFAULT 0 CHECK(selection_plan IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1,state TEXT NOT NULL CHECK(state IN('pending','dismissed','accepted','superseded','undone')),
 plan_json TEXT NOT NULL,created_at INTEGER NOT NULL,UNIQUE(job_id,proposal_key)
);
CREATE INDEX proposals_library ON work_proposals(library_id,state,primary_plan,kind,id);
CREATE INDEX proposals_pending ON work_proposals(job_id,state,kind,primary_plan,id);
CREATE TABLE work_proposal_members(
 proposal_id TEXT NOT NULL REFERENCES work_proposals(id) ON DELETE CASCADE,
 edition_id TEXT NOT NULL,position INTEGER NOT NULL,role TEXT NOT NULL CHECK(role IN('new','existing')),
 PRIMARY KEY(proposal_id,edition_id),UNIQUE(proposal_id,position)
);
CREATE INDEX proposals_member ON work_proposal_members(edition_id,proposal_id);
CREATE TABLE work_proposal_decisions(
 proposal_key TEXT PRIMARY KEY,proposal_id TEXT NOT NULL REFERENCES work_proposals(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),decision TEXT NOT NULL CHECK(decision IN('dismissed','accepted','undone')),
 before_json TEXT,after_json TEXT,accepted_members_json TEXT,result_json TEXT,revision INTEGER NOT NULL,updated_at INTEGER NOT NULL
);
CREATE TRIGGER proposal_insert_event AFTER INSERT ON work_proposals BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_proposal',NEW.id,NEW.revision,unixepoch());
END;
CREATE TRIGGER proposal_update_event AFTER UPDATE ON work_proposals
WHEN OLD.revision<>NEW.revision OR OLD.state<>NEW.state OR OLD.primary_plan<>NEW.primary_plan BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_proposal',NEW.id,NEW.revision,unixepoch());
END;

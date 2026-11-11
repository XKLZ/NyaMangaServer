-- Preserve all task references while adding Series discovery.

PRAGMA defer_foreign_keys=ON;

DROP INDEX "jobs_one_active";

DROP INDEX "translation_one_active";

DROP TRIGGER "notify_job_insert";

DROP TRIGGER "notify_job_update";

DROP INDEX "recommendations_query";

DROP TRIGGER "recommendation_change";

DROP TRIGGER "recommendation_feedback_change";

DROP TRIGGER "recommendation_feedback_update";

DROP INDEX "discovery_one_active_library";

DROP INDEX "proposals_library";

DROP INDEX "proposals_pending";

DROP INDEX "proposals_member";

DROP TRIGGER "proposal_insert_event";

DROP TRIGGER "proposal_update_event";

ALTER TABLE "job_items" RENAME TO "v9_job_items";

ALTER TABLE "job_resources" RENAME TO "v9_job_resources";

ALTER TABLE "jobs" RENAME TO "v9_jobs";

ALTER TABLE "maintenance_blocks" RENAME TO "v9_maintenance_blocks";

ALTER TABLE "maintenance_plans" RENAME TO "v9_maintenance_plans";

ALTER TABLE "recommendation_batches" RENAME TO "v9_recommendation_batches";

ALTER TABLE "recommendation_feedback" RENAME TO "v9_recommendation_feedback";

ALTER TABLE "recommendation_misses" RENAME TO "v9_recommendation_misses";

ALTER TABLE "scan_entries" RENAME TO "v9_scan_entries";

ALTER TABLE "source_previews" RENAME TO "v9_source_previews";

ALTER TABLE "work_discovery_runs" RENAME TO "v9_work_discovery_runs";

ALTER TABLE "work_pair_blocks" RENAME TO "v9_work_pair_blocks";

ALTER TABLE "work_pair_evidence" RENAME TO "v9_work_pair_evidence";

ALTER TABLE "work_proposal_decisions" RENAME TO "v9_work_proposal_decisions";

ALTER TABLE "work_proposal_members" RENAME TO "v9_work_proposal_members";

ALTER TABLE "work_proposals" RENAME TO "v9_work_proposals";

ALTER TABLE "work_recommendations" RENAME TO "v9_work_recommendations";

CREATE TABLE job_items (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL,
 relative_path TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, mtime_ns INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('pending','completed','failed','deferred')),
 error_code TEXT NOT NULL DEFAULT '', edition_id TEXT REFERENCES editions(id),
 content_revision INTEGER NOT NULL DEFAULT 0, selection_revision INTEGER NOT NULL DEFAULT 0,
 input_json TEXT NOT NULL DEFAULT '{}', candidates_json TEXT NOT NULL DEFAULT '[]',
 attempt INTEGER NOT NULL DEFAULT 0, preview_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(job_id,ordinal)
);

CREATE TABLE job_resources (
 resource_key TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), generation INTEGER NOT NULL
);

CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 library_id TEXT REFERENCES libraries(id),
 type TEXT NOT NULL DEFAULT 'scan' CHECK(type IN ('scan','metadata','tag_translation','maintenance','cover_rebuild','metadata_preview','work_recommendation','series_recommendation')),
 state TEXT NOT NULL CHECK(state IN ('queued','running','paused','completed','failed','cancelled')),
 stage TEXT NOT NULL,
 generation INTEGER NOT NULL CHECK(generation>0),
 total INTEGER CHECK(total IS NULL OR total>=0),
 done INTEGER NOT NULL DEFAULT 0 CHECK(done>=0 AND (total IS NULL OR done<=total)),
 failed INTEGER NOT NULL DEFAULT 0 CHECK(failed>=0 AND failed<=done),
 error_code TEXT NOT NULL DEFAULT '',
 parent_job_id TEXT REFERENCES jobs(id),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
, mode TEXT NOT NULL DEFAULT 'edition' CHECK(mode IN('edition','library')));

CREATE TABLE maintenance_blocks(library_id TEXT NOT NULL REFERENCES libraries(id),job_id TEXT NOT NULL REFERENCES jobs(id),PRIMARY KEY(library_id,job_id));

CREATE TABLE maintenance_plans(id TEXT PRIMARY KEY,operation TEXT NOT NULL,input_json TEXT NOT NULL,summary_json TEXT NOT NULL,expires_at INTEGER NOT NULL,job_id TEXT REFERENCES jobs(id),created_at INTEGER NOT NULL);

CREATE TABLE recommendation_batches(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 pool_json TEXT NOT NULL,algorithm_version TEXT NOT NULL,prepared_count INTEGER NOT NULL DEFAULT 0,
 resources_json TEXT NOT NULL DEFAULT '{}',summary_json TEXT NOT NULL DEFAULT '{}',input_errors_json TEXT NOT NULL DEFAULT '{}',created_at INTEGER NOT NULL
);

CREATE TABLE recommendation_feedback(
 recommendation_id TEXT PRIMARY KEY REFERENCES work_recommendations(id) ON DELETE CASCADE,
 account_id TEXT NOT NULL REFERENCES accounts(id),relation TEXT NOT NULL CHECK(relation IN('same_work','same_series','unrelated','uncertain')),
 note TEXT NOT NULL,labels_json TEXT NOT NULL DEFAULT '{}',revision INTEGER NOT NULL,
 accepted_result_json TEXT,updated_at INTEGER NOT NULL
);

CREATE TABLE recommendation_misses(
 id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES recommendation_batches(job_id) ON DELETE CASCADE,
 query_edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL,target_work_id TEXT REFERENCES works(id) ON DELETE SET NULL,
 account_id TEXT NOT NULL REFERENCES accounts(id),note TEXT NOT NULL,input_snapshot_json TEXT NOT NULL,created_at INTEGER NOT NULL,
 UNIQUE(job_id,query_edition_id,target_work_id,account_id)
);

CREATE TABLE scan_entries (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL, relative_path TEXT NOT NULL,
 size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
 PRIMARY KEY(job_id,ordinal), UNIQUE(job_id,relative_path)
);

CREATE TABLE source_previews(id TEXT PRIMARY KEY,edition_id TEXT NOT NULL REFERENCES editions(id),job_id TEXT NOT NULL REFERENCES jobs(id),result_json TEXT,expected_revision INTEGER NOT NULL,selection_revision INTEGER NOT NULL,content_revision INTEGER NOT NULL,expires_at INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'pending');

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

CREATE TABLE work_proposal_decisions(
 proposal_key TEXT PRIMARY KEY,proposal_id TEXT NOT NULL REFERENCES work_proposals(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),decision TEXT NOT NULL CHECK(decision IN('dismissed','accepted','undone')),
 before_json TEXT,after_json TEXT,accepted_members_json TEXT,result_json TEXT,revision INTEGER NOT NULL,updated_at INTEGER NOT NULL
);

CREATE TABLE work_proposal_members(
 proposal_id TEXT NOT NULL REFERENCES work_proposals(id) ON DELETE CASCADE,
 edition_id TEXT NOT NULL,position INTEGER NOT NULL,role TEXT NOT NULL CHECK(role IN('new','existing')),
 PRIMARY KEY(proposal_id,edition_id),UNIQUE(proposal_id,position)
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

CREATE TABLE work_recommendations(
 id TEXT PRIMARY KEY,job_id TEXT NOT NULL REFERENCES recommendation_batches(job_id) ON DELETE CASCADE,
 query_edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL,
 target_work_id TEXT REFERENCES works(id) ON DELETE SET NULL,
 query_fingerprint TEXT NOT NULL,target_fingerprint TEXT NOT NULL,
 tier TEXT NOT NULL CHECK(tier IN('strong','supplementary')),
 rank INTEGER NOT NULL CHECK(rank BETWEEN 1 AND 20),result_json TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,created_at INTEGER NOT NULL,
 UNIQUE(job_id,query_edition_id,rank)
);

INSERT INTO "job_items" SELECT * FROM "v9_job_items";

INSERT INTO "job_resources" SELECT * FROM "v9_job_resources";

INSERT INTO "jobs" SELECT * FROM "v9_jobs";

INSERT INTO "maintenance_blocks" SELECT * FROM "v9_maintenance_blocks";

INSERT INTO "maintenance_plans" SELECT * FROM "v9_maintenance_plans";

INSERT INTO "recommendation_batches" SELECT * FROM "v9_recommendation_batches";

INSERT INTO "recommendation_feedback" SELECT * FROM "v9_recommendation_feedback";

INSERT INTO "recommendation_misses" SELECT * FROM "v9_recommendation_misses";

INSERT INTO "scan_entries" SELECT * FROM "v9_scan_entries";

INSERT INTO "source_previews" SELECT * FROM "v9_source_previews";

INSERT INTO "work_discovery_runs" SELECT * FROM "v9_work_discovery_runs";

INSERT INTO "work_pair_blocks" SELECT * FROM "v9_work_pair_blocks";

INSERT INTO "work_pair_evidence" SELECT * FROM "v9_work_pair_evidence";

INSERT INTO "work_proposal_decisions" SELECT * FROM "v9_work_proposal_decisions";

INSERT INTO "work_proposal_members" SELECT * FROM "v9_work_proposal_members";

INSERT INTO "work_proposals" SELECT * FROM "v9_work_proposals";

INSERT INTO "work_recommendations" SELECT * FROM "v9_work_recommendations";

DROP TABLE "v9_job_items";

DROP TABLE "v9_job_resources";

DROP TABLE "v9_maintenance_blocks";

DROP TABLE "v9_maintenance_plans";

DROP TABLE "v9_recommendation_feedback";

DROP TABLE "v9_recommendation_misses";

DROP TABLE "v9_scan_entries";

DROP TABLE "v9_source_previews";

DROP TABLE "v9_work_pair_blocks";

DROP TABLE "v9_work_pair_evidence";

DROP TABLE "v9_work_proposal_decisions";

DROP TABLE "v9_work_proposal_members";

DROP TABLE "v9_work_discovery_runs";

DROP TABLE "v9_work_proposals";

DROP TABLE "v9_work_recommendations";

DROP TABLE "v9_recommendation_batches";

DROP TABLE "v9_jobs";

CREATE UNIQUE INDEX jobs_one_active ON jobs(library_id) WHERE type='scan' AND state IN ('queued','running','paused');

CREATE UNIQUE INDEX translation_one_active ON jobs(type) WHERE type='tag_translation' AND state IN ('queued','running','paused');

CREATE TRIGGER notify_job_insert AFTER INSERT ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;

CREATE TRIGGER notify_job_update AFTER UPDATE ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;

CREATE INDEX recommendations_query ON work_recommendations(query_edition_id,created_at DESC,id);

CREATE TRIGGER recommendation_change AFTER INSERT ON work_recommendations BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_recommendation',NEW.query_edition_id,NEW.revision,unixepoch());
END;

CREATE TRIGGER recommendation_feedback_change AFTER INSERT ON recommendation_feedback BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_recommendation',NEW.recommendation_id,NEW.revision,unixepoch());
END;

CREATE TRIGGER recommendation_feedback_update AFTER UPDATE ON recommendation_feedback BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_recommendation',NEW.recommendation_id,NEW.revision,unixepoch());
END;

CREATE UNIQUE INDEX discovery_one_active_library ON jobs(library_id) WHERE type='work_recommendation' AND mode='library' AND state IN('queued','running','paused');

CREATE INDEX proposals_library ON work_proposals(library_id,state,primary_plan,kind,id);

CREATE INDEX proposals_pending ON work_proposals(job_id,state,kind,primary_plan,id);

CREATE INDEX proposals_member ON work_proposal_members(edition_id,proposal_id);

CREATE TRIGGER proposal_insert_event AFTER INSERT ON work_proposals BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_proposal',NEW.id,NEW.revision,unixepoch());
END;

CREATE TRIGGER proposal_update_event AFTER UPDATE ON work_proposals
WHEN OLD.revision<>NEW.revision OR OLD.state<>NEW.state OR OLD.primary_plan<>NEW.primary_plan BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_proposal',NEW.id,NEW.revision,unixepoch());
END;

CREATE UNIQUE INDEX series_one_active_library ON jobs(library_id) WHERE type='series_recommendation' AND state IN('queued','running','paused');
CREATE TABLE series_discovery_runs(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 library_id TEXT NOT NULL,
 pool_json TEXT NOT NULL,context_json TEXT NOT NULL,input_token TEXT NOT NULL,
 features_done INTEGER NOT NULL DEFAULT 0,
 next_row INTEGER NOT NULL DEFAULT 0,edge_count INTEGER NOT NULL DEFAULT 0,
 phase TEXT NOT NULL DEFAULT 'features' CHECK(phase IN('features','pairs','proposals','finished')),
 evidence_version TEXT NOT NULL,refresh_required INTEGER NOT NULL DEFAULT 0,diagnostics_json TEXT NOT NULL DEFAULT '{}',created_at INTEGER NOT NULL
);
CREATE TABLE series_pair_rows(
 job_id TEXT NOT NULL REFERENCES series_discovery_runs(job_id) ON DELETE CASCADE,
 row_index INTEGER NOT NULL,edges_json TEXT NOT NULL,PRIMARY KEY(job_id,row_index)
);
CREATE TABLE series_proposals(
 id TEXT PRIMARY KEY,job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,
 library_id TEXT NOT NULL,proposal_key TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN('create_series','add_to_series')),
 target_series_id TEXT NOT NULL DEFAULT '',tier TEXT NOT NULL CHECK(tier IN('strong','supplementary')),
 primary_plan INTEGER NOT NULL DEFAULT 1,customized INTEGER NOT NULL DEFAULT 0,
 revision INTEGER NOT NULL DEFAULT 1,state TEXT NOT NULL CHECK(state IN('pending','dismissed','accepted','superseded','undone')),
 plan_json TEXT NOT NULL,created_at INTEGER NOT NULL
);
CREATE INDEX series_proposals_library ON series_proposals(library_id,state,primary_plan,kind,id);
CREATE TABLE series_proposal_members(
 proposal_id TEXT NOT NULL REFERENCES series_proposals(id) ON DELETE CASCADE,
 work_id TEXT NOT NULL,position INTEGER NOT NULL,role TEXT NOT NULL CHECK(role IN('new','existing')),
 PRIMARY KEY(proposal_id,work_id),UNIQUE(proposal_id,position)
);
CREATE INDEX series_proposals_member ON series_proposal_members(work_id,proposal_id);
CREATE TABLE series_pending_works(
 job_id TEXT NOT NULL REFERENCES series_discovery_runs(job_id) ON DELETE CASCADE,
 work_id TEXT NOT NULL,options_json TEXT NOT NULL,PRIMARY KEY(job_id,work_id)
);
CREATE TABLE series_proposal_decisions(
 proposal_key TEXT PRIMARY KEY,proposal_id TEXT NOT NULL REFERENCES series_proposals(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),decision TEXT NOT NULL CHECK(decision IN('dismissed','accepted','undone')),
 before_json TEXT,after_json TEXT,updated_at INTEGER NOT NULL
);
CREATE TRIGGER series_proposal_insert_event AFTER INSERT ON series_proposals BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('series_proposal',NEW.id,NEW.revision,unixepoch());
END;
CREATE TRIGGER series_proposal_update_event AFTER UPDATE ON series_proposals
WHEN OLD.revision<>NEW.revision OR OLD.state<>NEW.state BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('series_proposal',NEW.id,NEW.revision,unixepoch());
END;

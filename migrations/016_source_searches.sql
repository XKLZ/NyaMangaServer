-- Rebuild task constraint and its referencing tables atomically. All old columns, indexes and triggers are preserved.

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

DROP INDEX "series_one_active_library";

DROP INDEX "series_proposals_library";

DROP INDEX "series_proposals_member";

DROP TRIGGER "series_proposal_insert_event";

DROP TRIGGER "series_proposal_update_event";

DROP INDEX "import_children_batch";

DROP TRIGGER "import_link_job";

DROP INDEX "series_proposals_scope_match";

ALTER TABLE "discovery_preprocessing" RENAME TO "v15_discovery_preprocessing";

ALTER TABLE "discovery_run_features" RENAME TO "v15_discovery_run_features";

ALTER TABLE "import_children" RENAME TO "v15_import_children";

ALTER TABLE "job_items" RENAME TO "v15_job_items";

ALTER TABLE "job_resources" RENAME TO "v15_job_resources";

ALTER TABLE "jobs" RENAME TO "v15_jobs";

ALTER TABLE "maintenance_blocks" RENAME TO "v15_maintenance_blocks";

ALTER TABLE "maintenance_plans" RENAME TO "v15_maintenance_plans";

ALTER TABLE "recommendation_batches" RENAME TO "v15_recommendation_batches";

ALTER TABLE "recommendation_feedback" RENAME TO "v15_recommendation_feedback";

ALTER TABLE "recommendation_misses" RENAME TO "v15_recommendation_misses";

ALTER TABLE "scan_entries" RENAME TO "v15_scan_entries";

ALTER TABLE "series_discovery_runs" RENAME TO "v15_series_discovery_runs";

ALTER TABLE "series_evidence_blocks" RENAME TO "v15_series_evidence_blocks";

ALTER TABLE "series_evidence_generations" RENAME TO "v15_series_evidence_generations";

ALTER TABLE "series_match_feedback" RENAME TO "v15_series_match_feedback";

ALTER TABLE "series_pair_rows" RENAME TO "v15_series_pair_rows";

ALTER TABLE "series_pending_works" RENAME TO "v15_series_pending_works";

ALTER TABLE "series_proposal_decisions" RENAME TO "v15_series_proposal_decisions";

ALTER TABLE "series_proposal_members" RENAME TO "v15_series_proposal_members";

ALTER TABLE "series_proposals" RENAME TO "v15_series_proposals";

ALTER TABLE "source_previews" RENAME TO "v15_source_previews";

ALTER TABLE "work_discovery_runs" RENAME TO "v15_work_discovery_runs";

ALTER TABLE "work_pair_blocks" RENAME TO "v15_work_pair_blocks";

ALTER TABLE "work_pair_evidence" RENAME TO "v15_work_pair_evidence";

ALTER TABLE "work_proposal_decisions" RENAME TO "v15_work_proposal_decisions";

ALTER TABLE "work_proposal_members" RENAME TO "v15_work_proposal_members";

ALTER TABLE "work_proposals" RENAME TO "v15_work_proposals";

ALTER TABLE "work_recommendations" RENAME TO "v15_work_recommendations";

CREATE TABLE discovery_preprocessing(
 job_id TEXT PRIMARY KEY REFERENCES work_discovery_runs(job_id) ON DELETE CASCADE,
 fingerprints_json TEXT NOT NULL,version TEXT NOT NULL
);

CREATE TABLE discovery_run_features(
 job_id TEXT NOT NULL REFERENCES work_discovery_runs(job_id) ON DELETE CASCADE,
 edition_id TEXT NOT NULL,input_key TEXT NOT NULL,
 PRIMARY KEY(job_id,edition_id)
);

CREATE TABLE import_children(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id),batch_id TEXT NOT NULL REFERENCES import_batches(id),stage TEXT NOT NULL
);

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
 type TEXT NOT NULL DEFAULT 'scan' CHECK(type IN ('scan','metadata','tag_translation','maintenance','cover_rebuild','metadata_preview','work_recommendation','series_recommendation','metadata_search')),
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

CREATE TABLE series_discovery_runs(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 library_id TEXT NOT NULL,
 pool_json TEXT NOT NULL,context_json TEXT NOT NULL,input_token TEXT NOT NULL,
 features_done INTEGER NOT NULL DEFAULT 0,
 next_row INTEGER NOT NULL DEFAULT 0,edge_count INTEGER NOT NULL DEFAULT 0,
 phase TEXT NOT NULL DEFAULT 'features' CHECK(phase IN('features','pairs','proposals','finished')),
 evidence_version TEXT NOT NULL,refresh_required INTEGER NOT NULL DEFAULT 0,diagnostics_json TEXT NOT NULL DEFAULT '{}',created_at INTEGER NOT NULL
);

CREATE TABLE series_evidence_blocks(
 job_id TEXT NOT NULL REFERENCES series_evidence_generations(job_id) ON DELETE CASCADE,
 block_key TEXT NOT NULL,storage_key TEXT NOT NULL,sha256 TEXT NOT NULL,
 size INTEGER NOT NULL CHECK(size>0),records INTEGER NOT NULL CHECK(records>=0),
 PRIMARY KEY(job_id,block_key)
);

CREATE TABLE series_evidence_generations(
 job_id TEXT PRIMARY KEY REFERENCES series_discovery_runs(job_id) ON DELETE CASCADE,
 library_id TEXT NOT NULL,execution_version TEXT NOT NULL,
 profiles_json TEXT NOT NULL DEFAULT '{}',
 base_job_id TEXT REFERENCES series_evidence_generations(job_id) ON DELETE RESTRICT,
 bytes INTEGER NOT NULL DEFAULT 0 CHECK(bytes>=0 AND bytes<=134217728),
 computed_pairs INTEGER NOT NULL DEFAULT 0,reused_pairs INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'building' CHECK(state IN('building','complete')),
 created_at INTEGER NOT NULL DEFAULT(unixepoch())
);

CREATE TABLE series_match_feedback(
 library_id TEXT NOT NULL,semantic_key TEXT NOT NULL,
 decision TEXT NOT NULL CHECK(decision IN('dismissed','restored')),
 proposal_id TEXT NOT NULL REFERENCES series_proposals(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),
 legacy INTEGER NOT NULL DEFAULT 0 CHECK(legacy IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1,updated_at INTEGER NOT NULL,
 PRIMARY KEY(library_id,semantic_key)
);

CREATE TABLE series_pair_rows(
 job_id TEXT NOT NULL REFERENCES series_discovery_runs(job_id) ON DELETE CASCADE,
 row_index INTEGER NOT NULL,edges_json TEXT NOT NULL,PRIMARY KEY(job_id,row_index)
);

CREATE TABLE series_pending_works(
 job_id TEXT NOT NULL REFERENCES series_discovery_runs(job_id) ON DELETE CASCADE,
 work_id TEXT NOT NULL,options_json TEXT NOT NULL,PRIMARY KEY(job_id,work_id)
);

CREATE TABLE series_proposal_decisions(
 proposal_key TEXT PRIMARY KEY,proposal_id TEXT NOT NULL REFERENCES series_proposals(id),
 account_id TEXT NOT NULL REFERENCES accounts(id),decision TEXT NOT NULL CHECK(decision IN('dismissed','accepted','undone')),
 before_json TEXT,after_json TEXT,updated_at INTEGER NOT NULL
);

CREATE TABLE series_proposal_members(
 proposal_id TEXT NOT NULL REFERENCES series_proposals(id) ON DELETE CASCADE,
 work_id TEXT NOT NULL,position INTEGER NOT NULL,role TEXT NOT NULL CHECK(role IN('new','existing')),
 PRIMARY KEY(proposal_id,work_id),UNIQUE(proposal_id,position)
);

CREATE TABLE series_proposals(
 id TEXT PRIMARY KEY,job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL,
 library_id TEXT NOT NULL,proposal_key TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN('create_series','add_to_series')),
 target_series_id TEXT NOT NULL DEFAULT '',tier TEXT NOT NULL CHECK(tier IN('strong','supplementary')),
 primary_plan INTEGER NOT NULL DEFAULT 1,customized INTEGER NOT NULL DEFAULT 0,
 revision INTEGER NOT NULL DEFAULT 1,state TEXT NOT NULL CHECK(state IN('pending','dismissed','accepted','superseded','undone')),
 plan_json TEXT NOT NULL,created_at INTEGER NOT NULL
, match_key TEXT NOT NULL DEFAULT '', scope_key TEXT NOT NULL DEFAULT 'global');

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
, base_job_id TEXT REFERENCES work_discovery_runs(job_id) ON DELETE RESTRICT, execution_version TEXT NOT NULL DEFAULT '', reused_pairs INTEGER NOT NULL DEFAULT 0, computed_pairs INTEGER NOT NULL DEFAULT 0, reused_features INTEGER NOT NULL DEFAULT 0, prepared_features INTEGER NOT NULL DEFAULT 0);

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

INSERT INTO "discovery_preprocessing" ("job_id","fingerprints_json","version") SELECT "job_id","fingerprints_json","version" FROM "v15_discovery_preprocessing";

INSERT INTO "discovery_run_features" ("job_id","edition_id","input_key") SELECT "job_id","edition_id","input_key" FROM "v15_discovery_run_features";

INSERT INTO "import_children" ("job_id","batch_id","stage") SELECT "job_id","batch_id","stage" FROM "v15_import_children";

INSERT INTO "job_items" ("job_id","ordinal","relative_path","size","mtime_ns","state","error_code","edition_id","content_revision","selection_revision","input_json","candidates_json","attempt","preview_id") SELECT "job_id","ordinal","relative_path","size","mtime_ns","state","error_code","edition_id","content_revision","selection_revision","input_json","candidates_json","attempt","preview_id" FROM "v15_job_items";

INSERT INTO "job_resources" ("resource_key","job_id","generation") SELECT "resource_key","job_id","generation" FROM "v15_job_resources";

INSERT INTO "jobs" ("id","library_id","type","state","stage","generation","total","done","failed","error_code","parent_job_id","created_at","updated_at","mode") SELECT "id","library_id","type","state","stage","generation","total","done","failed","error_code","parent_job_id","created_at","updated_at","mode" FROM "v15_jobs";

INSERT INTO "maintenance_blocks" ("library_id","job_id") SELECT "library_id","job_id" FROM "v15_maintenance_blocks";

INSERT INTO "maintenance_plans" ("id","operation","input_json","summary_json","expires_at","job_id","created_at") SELECT "id","operation","input_json","summary_json","expires_at","job_id","created_at" FROM "v15_maintenance_plans";

INSERT INTO "recommendation_batches" ("job_id","pool_json","algorithm_version","prepared_count","resources_json","summary_json","input_errors_json","created_at") SELECT "job_id","pool_json","algorithm_version","prepared_count","resources_json","summary_json","input_errors_json","created_at" FROM "v15_recommendation_batches";

INSERT INTO "recommendation_feedback" ("recommendation_id","account_id","relation","note","labels_json","revision","accepted_result_json","updated_at") SELECT "recommendation_id","account_id","relation","note","labels_json","revision","accepted_result_json","updated_at" FROM "v15_recommendation_feedback";

INSERT INTO "recommendation_misses" ("id","job_id","query_edition_id","target_work_id","account_id","note","input_snapshot_json","created_at") SELECT "id","job_id","query_edition_id","target_work_id","account_id","note","input_snapshot_json","created_at" FROM "v15_recommendation_misses";

INSERT INTO "scan_entries" ("job_id","ordinal","relative_path","size","mtime_ns") SELECT "job_id","ordinal","relative_path","size","mtime_ns" FROM "v15_scan_entries";

INSERT INTO "series_discovery_runs" ("job_id","library_id","pool_json","context_json","input_token","features_done","next_row","edge_count","phase","evidence_version","refresh_required","diagnostics_json","created_at") SELECT "job_id","library_id","pool_json","context_json","input_token","features_done","next_row","edge_count","phase","evidence_version","refresh_required","diagnostics_json","created_at" FROM "v15_series_discovery_runs";

INSERT INTO "series_evidence_blocks" ("job_id","block_key","storage_key","sha256","size","records") SELECT "job_id","block_key","storage_key","sha256","size","records" FROM "v15_series_evidence_blocks";

INSERT INTO "series_evidence_generations" ("job_id","library_id","execution_version","profiles_json","base_job_id","bytes","computed_pairs","reused_pairs","state","created_at") SELECT "job_id","library_id","execution_version","profiles_json","base_job_id","bytes","computed_pairs","reused_pairs","state","created_at" FROM "v15_series_evidence_generations";

INSERT INTO "series_match_feedback" ("library_id","semantic_key","decision","proposal_id","account_id","legacy","revision","updated_at") SELECT "library_id","semantic_key","decision","proposal_id","account_id","legacy","revision","updated_at" FROM "v15_series_match_feedback";

INSERT INTO "series_pair_rows" ("job_id","row_index","edges_json") SELECT "job_id","row_index","edges_json" FROM "v15_series_pair_rows";

INSERT INTO "series_pending_works" ("job_id","work_id","options_json") SELECT "job_id","work_id","options_json" FROM "v15_series_pending_works";

INSERT INTO "series_proposal_decisions" ("proposal_key","proposal_id","account_id","decision","before_json","after_json","updated_at") SELECT "proposal_key","proposal_id","account_id","decision","before_json","after_json","updated_at" FROM "v15_series_proposal_decisions";

INSERT INTO "series_proposal_members" ("proposal_id","work_id","position","role") SELECT "proposal_id","work_id","position","role" FROM "v15_series_proposal_members";

INSERT INTO "series_proposals" ("id","job_id","library_id","proposal_key","kind","target_series_id","tier","primary_plan","customized","revision","state","plan_json","created_at","match_key","scope_key") SELECT "id","job_id","library_id","proposal_key","kind","target_series_id","tier","primary_plan","customized","revision","state","plan_json","created_at","match_key","scope_key" FROM "v15_series_proposals";

INSERT INTO "source_previews" ("id","edition_id","job_id","result_json","expected_revision","selection_revision","content_revision","expires_at","state") SELECT "id","edition_id","job_id","result_json","expected_revision","selection_revision","content_revision","expires_at","state" FROM "v15_source_previews";

INSERT INTO "work_discovery_runs" ("job_id","library_id","next_row","edge_count","evidence_bytes","refresh_required","phase","evidence_version","created_at","base_job_id","execution_version","reused_pairs","computed_pairs","reused_features","prepared_features") SELECT "job_id","library_id","next_row","edge_count","evidence_bytes","refresh_required","phase","evidence_version","created_at","base_job_id","execution_version","reused_pairs","computed_pairs","reused_features","prepared_features" FROM "v15_work_discovery_runs";

INSERT INTO "work_pair_blocks" ("job_id","row_index","hard_conflicts") SELECT "job_id","row_index","hard_conflicts" FROM "v15_work_pair_blocks";

INSERT INTO "work_pair_evidence" ("job_id","a","b","tier","score","signal_json") SELECT "job_id","a","b","tier","score","signal_json" FROM "v15_work_pair_evidence";

INSERT INTO "work_proposal_decisions" ("proposal_key","proposal_id","account_id","decision","before_json","after_json","accepted_members_json","result_json","revision","updated_at") SELECT "proposal_key","proposal_id","account_id","decision","before_json","after_json","accepted_members_json","result_json","revision","updated_at" FROM "v15_work_proposal_decisions";

INSERT INTO "work_proposal_members" ("proposal_id","edition_id","position","role") SELECT "proposal_id","edition_id","position","role" FROM "v15_work_proposal_members";

INSERT INTO "work_proposals" ("id","job_id","library_id","proposal_key","kind","target_work_id","tier","score","primary_plan","selection_plan","revision","state","plan_json","created_at") SELECT "id","job_id","library_id","proposal_key","kind","target_work_id","tier","score","primary_plan","selection_plan","revision","state","plan_json","created_at" FROM "v15_work_proposals";

INSERT INTO "work_recommendations" ("id","job_id","query_edition_id","target_work_id","query_fingerprint","target_fingerprint","tier","rank","result_json","revision","created_at") SELECT "id","job_id","query_edition_id","target_work_id","query_fingerprint","target_fingerprint","tier","rank","result_json","revision","created_at" FROM "v15_work_recommendations";

DROP TABLE "v15_discovery_preprocessing";

DROP TABLE "v15_discovery_run_features";

DROP TABLE "v15_import_children";

DROP TABLE "v15_job_items";

DROP TABLE "v15_job_resources";

DROP TABLE "v15_maintenance_blocks";

DROP TABLE "v15_maintenance_plans";

DROP TABLE "v15_recommendation_feedback";

DROP TABLE "v15_recommendation_misses";

DROP TABLE "v15_scan_entries";

DROP TABLE "v15_series_evidence_blocks";

DROP TABLE "v15_series_match_feedback";

DROP TABLE "v15_series_pair_rows";

DROP TABLE "v15_series_pending_works";

DROP TABLE "v15_series_proposal_decisions";

DROP TABLE "v15_series_proposal_members";

DROP TABLE "v15_source_previews";

DROP TABLE "v15_work_pair_blocks";

DROP TABLE "v15_work_pair_evidence";

DROP TABLE "v15_work_proposal_decisions";

DROP TABLE "v15_work_proposal_members";

DROP TABLE "v15_series_evidence_generations";

DROP TABLE "v15_series_proposals";

DROP TABLE "v15_work_discovery_runs";

DROP TABLE "v15_work_proposals";

DROP TABLE "v15_work_recommendations";

DROP TABLE "v15_recommendation_batches";

DROP TABLE "v15_series_discovery_runs";

DROP TABLE "v15_jobs";

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

CREATE INDEX series_proposals_library ON series_proposals(library_id,state,primary_plan,kind,id);

CREATE INDEX series_proposals_member ON series_proposal_members(work_id,proposal_id);

CREATE TRIGGER series_proposal_insert_event AFTER INSERT ON series_proposals BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('series_proposal',NEW.id,NEW.revision,unixepoch());
END;

CREATE TRIGGER series_proposal_update_event AFTER UPDATE ON series_proposals
WHEN OLD.revision<>NEW.revision OR OLD.state<>NEW.state BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('series_proposal',NEW.id,NEW.revision,unixepoch());
END;

CREATE INDEX import_children_batch ON import_children(batch_id,stage);

CREATE TRIGGER import_link_job AFTER INSERT ON jobs
 WHEN EXISTS(SELECT 1 FROM import_child_requests WHERE job_id=NEW.id)
 BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM import_child_requests r JOIN import_batches b ON b.id=r.batch_id WHERE r.job_id=NEW.id AND b.state='processing' AND b.stage=r.stage) THEN RAISE(ABORT,'import_stage_changed') END;
 INSERT INTO import_children SELECT job_id,batch_id,stage FROM import_child_requests WHERE job_id=NEW.id;
 END;

CREATE UNIQUE INDEX series_proposals_scope_match ON series_proposals(scope_key,match_key)
 WHERE match_key<>'';

CREATE TABLE source_searches(
 id TEXT PRIMARY KEY,batch_id TEXT NOT NULL,edition_id TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN('matches','title')),query TEXT NOT NULL,site TEXT NOT NULL CHECK(site IN('eh','ex')),
 request_key TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,
 job_id TEXT REFERENCES jobs(id),input_json TEXT NOT NULL,
 content_revision INTEGER NOT NULL,selection_revision INTEGER NOT NULL,edition_revision INTEGER NOT NULL,member_revision INTEGER NOT NULL,
 next_cursor TEXT NOT NULL DEFAULT '',continuation TEXT NOT NULL DEFAULT 'unknown' CHECK(continuation IN('unknown','more','end','limited')),
 selected_result TEXT NOT NULL DEFAULT '',selected_candidate_revision INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL DEFAULT(unixepoch()),
 UNIQUE(batch_id,edition_id,request_key),
 FOREIGN KEY(batch_id,edition_id) REFERENCES import_members(batch_id,edition_id) ON DELETE CASCADE
);
CREATE TABLE source_search_results(
 id TEXT PRIMARY KEY,search_id TEXT NOT NULL REFERENCES source_searches(id) ON DELETE CASCADE,
 url TEXT NOT NULL,gallery_id TEXT NOT NULL,title TEXT NOT NULL,position INTEGER NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN('pending','ready','failed')),
 error_code TEXT NOT NULL DEFAULT '',result_json TEXT,
 UNIQUE(search_id,url),UNIQUE(search_id,gallery_id),UNIQUE(search_id,position)
);
CREATE TABLE source_search_requests(
 search_id TEXT NOT NULL REFERENCES source_searches(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL,page INTEGER NOT NULL,job_id TEXT NOT NULL REFERENCES jobs(id),batch_paused INTEGER NOT NULL DEFAULT 0 CHECK(batch_paused IN(0,1)),
 PRIMARY KEY(search_id,request_key)
);
CREATE INDEX source_search_member ON source_searches(batch_id,edition_id,created_at,id);
CREATE TRIGGER source_search_event AFTER UPDATE ON source_searches BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('source_search',NEW.id,NEW.revision,unixepoch());
END;

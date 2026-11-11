-- Preserve all task references while adding the optional Work recommendation kind.

PRAGMA defer_foreign_keys=ON;

DROP TRIGGER notify_job_insert;

DROP TRIGGER notify_job_update;

DROP INDEX jobs_one_active;

DROP INDEX translation_one_active;

ALTER TABLE jobs RENAME TO v7_jobs;

ALTER TABLE job_items RENAME TO v7_job_items;

ALTER TABLE scan_entries RENAME TO v7_scan_entries;

ALTER TABLE job_resources RENAME TO v7_job_resources;

ALTER TABLE maintenance_plans RENAME TO v7_maintenance_plans;

ALTER TABLE source_previews RENAME TO v7_source_previews;

ALTER TABLE maintenance_blocks RENAME TO v7_maintenance_blocks;

CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 library_id TEXT REFERENCES libraries(id),
 type TEXT NOT NULL DEFAULT 'scan' CHECK(type IN ('scan','metadata','tag_translation','maintenance','cover_rebuild','metadata_preview','work_recommendation')),
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
);

INSERT INTO jobs SELECT * FROM v7_jobs;

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

INSERT INTO job_items SELECT * FROM v7_job_items;

CREATE TABLE scan_entries (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL, relative_path TEXT NOT NULL,
 size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
 PRIMARY KEY(job_id,ordinal), UNIQUE(job_id,relative_path)
);

INSERT INTO scan_entries SELECT * FROM v7_scan_entries;

CREATE TABLE job_resources (
 resource_key TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), generation INTEGER NOT NULL
);

INSERT INTO job_resources SELECT * FROM v7_job_resources;

CREATE TABLE maintenance_plans(id TEXT PRIMARY KEY,operation TEXT NOT NULL,input_json TEXT NOT NULL,summary_json TEXT NOT NULL,expires_at INTEGER NOT NULL,job_id TEXT REFERENCES jobs(id),created_at INTEGER NOT NULL);

INSERT INTO maintenance_plans SELECT * FROM v7_maintenance_plans;

CREATE TABLE source_previews(id TEXT PRIMARY KEY,edition_id TEXT NOT NULL REFERENCES editions(id),job_id TEXT NOT NULL REFERENCES jobs(id),result_json TEXT,expected_revision INTEGER NOT NULL,selection_revision INTEGER NOT NULL,content_revision INTEGER NOT NULL,expires_at INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'pending');

INSERT INTO source_previews SELECT * FROM v7_source_previews;

CREATE TABLE maintenance_blocks(library_id TEXT NOT NULL REFERENCES libraries(id),job_id TEXT NOT NULL REFERENCES jobs(id),PRIMARY KEY(library_id,job_id));

INSERT INTO maintenance_blocks SELECT * FROM v7_maintenance_blocks;

DROP TABLE v7_job_items;

DROP TABLE v7_scan_entries;

DROP TABLE v7_job_resources;

DROP TABLE v7_maintenance_plans;

DROP TABLE v7_source_previews;

DROP TABLE v7_maintenance_blocks;

DROP TABLE v7_jobs;

CREATE UNIQUE INDEX jobs_one_active ON jobs(library_id) WHERE type='scan' AND state IN ('queued','running','paused');

CREATE UNIQUE INDEX translation_one_active ON jobs(type) WHERE type='tag_translation' AND state IN ('queued','running','paused');

CREATE TRIGGER notify_job_insert AFTER INSERT ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;
CREATE TRIGGER notify_job_update AFTER UPDATE ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;
-- Long-term validated snapshot cover input; independent of mutable display selection.
CREATE TABLE recommendation_materials(
 edition_id TEXT PRIMARY KEY REFERENCES editions(id) ON DELETE CASCADE,
 asset_id TEXT NOT NULL REFERENCES assets(id),origin TEXT NOT NULL CHECK(origin='treec-validation'),
 input_sha256 TEXT NOT NULL,source_id TEXT NOT NULL,input_fingerprint TEXT NOT NULL
);
-- Only a registry in the primary DB; vectors live in the rebuildable private index.
CREATE TABLE recommendation_features(
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 algorithm TEXT NOT NULL CHECK(algorithm IN('sscd','phash','minilm')),
 input_fingerprint TEXT NOT NULL,feature_fingerprint TEXT NOT NULL,model_sha256 TEXT NOT NULL,
 algorithm_version TEXT NOT NULL,feature_digest TEXT NOT NULL,updated_at INTEGER NOT NULL,
 PRIMARY KEY(edition_id,algorithm)
);
CREATE TABLE recommendation_batches(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 pool_json TEXT NOT NULL,algorithm_version TEXT NOT NULL,prepared_count INTEGER NOT NULL DEFAULT 0,
 resources_json TEXT NOT NULL DEFAULT '{}',summary_json TEXT NOT NULL DEFAULT '{}',input_errors_json TEXT NOT NULL DEFAULT '{}',created_at INTEGER NOT NULL
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
CREATE INDEX recommendations_query ON work_recommendations(query_edition_id,created_at DESC,id);
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
CREATE TRIGGER recommendation_change AFTER INSERT ON work_recommendations BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_recommendation',NEW.query_edition_id,NEW.revision,unixepoch());
END;
CREATE TRIGGER recommendation_feedback_change AFTER INSERT ON recommendation_feedback BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_recommendation',NEW.recommendation_id,NEW.revision,unixepoch());
END;
CREATE TRIGGER recommendation_feedback_update AFTER UPDATE ON recommendation_feedback BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('work_recommendation',NEW.recommendation_id,NEW.revision,unixepoch());
END;

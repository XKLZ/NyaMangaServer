-- Preserve every task checkpoint while extending maintenance task kinds.
DROP TRIGGER notify_job_insert;
DROP TRIGGER notify_job_update;
DROP INDEX translation_one_active;
ALTER TABLE job_resources RENAME TO previous_job_resources;
ALTER TABLE jobs RENAME TO previous_jobs;
ALTER TABLE job_items RENAME TO previous_job_items;
ALTER TABLE scan_entries RENAME TO previous_scan_entries;
DROP INDEX jobs_one_active;
CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 library_id TEXT REFERENCES libraries(id),
 type TEXT NOT NULL DEFAULT 'scan' CHECK(type IN ('scan','metadata','tag_translation','maintenance','cover_rebuild','metadata_preview')),
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
INSERT INTO jobs SELECT id,library_id,type,state,stage,generation,total,done,failed,error_code,parent_job_id,created_at,updated_at FROM previous_jobs;
CREATE UNIQUE INDEX jobs_one_active ON jobs(library_id) WHERE type='scan' AND state IN ('queued','running','paused');
CREATE UNIQUE INDEX translation_one_active ON jobs(type) WHERE type='tag_translation' AND state IN ('queued','running','paused');
CREATE TABLE job_items (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL,
 relative_path TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, mtime_ns INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('pending','completed','failed','deferred')),
 error_code TEXT NOT NULL DEFAULT '', book_id TEXT REFERENCES books(id),
 content_revision INTEGER NOT NULL DEFAULT 0, selection_revision INTEGER NOT NULL DEFAULT 0,
 input_json TEXT NOT NULL DEFAULT '{}', candidates_json TEXT NOT NULL DEFAULT '[]',
 attempt INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(job_id,ordinal)
);
INSERT INTO job_items SELECT * FROM previous_job_items;
CREATE TABLE scan_entries (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL, relative_path TEXT NOT NULL,
 size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
 PRIMARY KEY(job_id,ordinal), UNIQUE(job_id,relative_path)
);
INSERT INTO scan_entries SELECT * FROM previous_scan_entries;
DROP TABLE previous_job_items;
DROP TABLE previous_scan_entries;

CREATE TABLE job_resources (
 resource_key TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), generation INTEGER NOT NULL
);
INSERT INTO job_resources SELECT * FROM previous_job_resources;
DROP TABLE previous_job_resources;
DROP TABLE previous_jobs;
CREATE TRIGGER notify_job_insert AFTER INSERT ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;
CREATE TRIGGER notify_job_update AFTER UPDATE ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;
ALTER TABLE libraries ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0 CHECK(disabled IN(0,1));
ALTER TABLE libraries ADD COLUMN trashed_at INTEGER;
ALTER TABLE books ADD COLUMN trashed_at INTEGER;
ALTER TABLE job_items ADD COLUMN preview_id TEXT NOT NULL DEFAULT '';
CREATE TABLE scan_exclusions(library_id TEXT NOT NULL REFERENCES libraries(id),relative_path TEXT NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(library_id,relative_path));
CREATE TABLE cover_selections(book_id TEXT PRIMARY KEY REFERENCES books(id),mode TEXT NOT NULL CHECK(mode IN('auto','page','asset')),page_index INTEGER,content_revision INTEGER NOT NULL,asset_id TEXT,revision INTEGER NOT NULL DEFAULT 1);
CREATE TABLE assets(id TEXT PRIMARY KEY,storage_key TEXT NOT NULL UNIQUE,digest TEXT NOT NULL,size INTEGER NOT NULL,created_at INTEGER NOT NULL);
CREATE TABLE maintenance_plans(id TEXT PRIMARY KEY,operation TEXT NOT NULL,input_json TEXT NOT NULL,summary_json TEXT NOT NULL,expires_at INTEGER NOT NULL,job_id TEXT REFERENCES jobs(id),created_at INTEGER NOT NULL);
CREATE TABLE source_previews(id TEXT PRIMARY KEY,book_id TEXT NOT NULL REFERENCES books(id),job_id TEXT NOT NULL REFERENCES jobs(id),result_json TEXT,expected_revision INTEGER NOT NULL,selection_revision INTEGER NOT NULL,content_revision INTEGER NOT NULL,expires_at INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'pending');
CREATE INDEX books_trash ON books(trashed_at,library_id);
CREATE TRIGGER library_management_catalog AFTER UPDATE OF disabled,trashed_at ON libraries BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER source_delete_catalog AFTER DELETE ON book_sources BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TABLE maintenance_blocks(library_id TEXT NOT NULL REFERENCES libraries(id),job_id TEXT NOT NULL REFERENCES jobs(id),PRIMARY KEY(library_id,job_id));
CREATE TRIGGER book_management_catalog AFTER UPDATE OF trashed_at ON books BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER book_delete_catalog AFTER DELETE ON books BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('book',OLD.id,OLD.revision+1,unixepoch()); END;

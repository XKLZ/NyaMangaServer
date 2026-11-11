-- Import batches are durable orchestration, not another kind of worker job.
CREATE TABLE import_batches(
 id TEXT PRIMARY KEY,library_id TEXT NOT NULL REFERENCES libraries(id),
 request_key TEXT NOT NULL UNIQUE,
 stage TEXT NOT NULL DEFAULT 'scan' CHECK(stage IN('scan','metadata','versions','series','finished')),
 state TEXT NOT NULL DEFAULT 'processing' CHECK(state IN('processing','review','paused','failed','completed','cancelled')),
 revision INTEGER NOT NULL DEFAULT 1,error_code TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL DEFAULT(unixepoch()),updated_at INTEGER NOT NULL DEFAULT(unixepoch())
);
CREATE UNIQUE INDEX import_library_active ON import_batches(library_id) WHERE state NOT IN('completed','cancelled');
-- Reserving the child identity is restart-safe. The job trigger links the actual
-- child in the same transaction that creates its frozen inputs and resources.
CREATE TABLE import_child_requests(
 job_id TEXT PRIMARY KEY,batch_id TEXT NOT NULL REFERENCES import_batches(id),
 stage TEXT NOT NULL,input_json TEXT NOT NULL DEFAULT '{}',
 created_at INTEGER NOT NULL DEFAULT(unixepoch())
);
CREATE TABLE import_children(
 job_id TEXT PRIMARY KEY REFERENCES jobs(id),batch_id TEXT NOT NULL REFERENCES import_batches(id),stage TEXT NOT NULL
);
CREATE INDEX import_children_batch ON import_children(batch_id,stage);
CREATE TRIGGER import_link_job AFTER INSERT ON jobs
 WHEN EXISTS(SELECT 1 FROM import_child_requests WHERE job_id=NEW.id)
 BEGIN
 SELECT CASE WHEN NOT EXISTS(SELECT 1 FROM import_child_requests r JOIN import_batches b ON b.id=r.batch_id WHERE r.job_id=NEW.id AND b.state='processing' AND b.stage=r.stage) THEN RAISE(ABORT,'import_stage_changed') END;
 INSERT INTO import_children SELECT job_id,batch_id,stage FROM import_child_requests WHERE job_id=NEW.id;
 END;
CREATE TABLE import_members(
 batch_id TEXT NOT NULL REFERENCES import_batches(id),edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 change_kind TEXT NOT NULL CHECK(change_kind IN('new','changed')),
 content_revision INTEGER NOT NULL,scan_error TEXT NOT NULL DEFAULT '',
 metadata_decision TEXT NOT NULL DEFAULT '',metadata_token TEXT NOT NULL DEFAULT '',
 version_decision TEXT NOT NULL DEFAULT '',version_token TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1,request_generation INTEGER NOT NULL DEFAULT 0,
 active_job_id TEXT,fetch_error TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(batch_id,edition_id)
);
CREATE TABLE import_candidates(
 batch_id TEXT NOT NULL,edition_id TEXT NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1,request_generation INTEGER NOT NULL,
 content_revision INTEGER NOT NULL,selection_revision INTEGER NOT NULL,input_json TEXT NOT NULL,result_json TEXT NOT NULL,
 created_at INTEGER NOT NULL DEFAULT(unixepoch()),
 PRIMARY KEY(batch_id,edition_id),
 FOREIGN KEY(batch_id,edition_id) REFERENCES import_members(batch_id,edition_id) ON DELETE CASCADE
);
CREATE TABLE import_works(
 batch_id TEXT NOT NULL REFERENCES import_batches(id),work_id TEXT NOT NULL REFERENCES works(id) ON DELETE CASCADE,
 decision TEXT NOT NULL DEFAULT '',input_token TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(batch_id,work_id)
);
CREATE TABLE import_proposals(
 batch_id TEXT NOT NULL REFERENCES import_batches(id),stage TEXT NOT NULL,proposal_id TEXT NOT NULL,
 PRIMARY KEY(batch_id,stage,proposal_id)
);
CREATE TRIGGER import_batch_event AFTER UPDATE ON import_batches BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('import_batch',NEW.id,NEW.revision,unixepoch());
 END;
-- An ordinary/periodic scan may index a file before a complete import starts.
-- Keep its import watermark separate from its physical scan identity. Existing
-- pre-workflow files form the preserved baseline; newly scanned files do not.
CREATE TABLE file_import_state(
 edition_id TEXT PRIMARY KEY REFERENCES editions(id) ON DELETE CASCADE,
 content_revision INTEGER NOT NULL CHECK(content_revision>0)
);
INSERT INTO file_import_state SELECT edition_id,content_revision FROM files;

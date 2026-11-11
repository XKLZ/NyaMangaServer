-- Rebuild task constraints without disabling foreign keys or losing checkpoints.
ALTER TABLE jobs RENAME TO previous_jobs;
ALTER TABLE job_items RENAME TO previous_job_items;
ALTER TABLE scan_entries RENAME TO previous_scan_entries;
DROP INDEX jobs_one_active;
CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 library_id TEXT REFERENCES libraries(id),
 type TEXT NOT NULL DEFAULT 'scan' CHECK(type IN ('scan','metadata','tag_translation')),
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
INSERT INTO jobs(id,library_id,state,stage,generation,total,done,failed,error_code,created_at,updated_at)
 SELECT id,library_id,state,stage,generation,total,done,failed,error_code,created_at,updated_at FROM previous_jobs;
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
INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state,error_code,book_id)
 SELECT job_id,ordinal,relative_path,size,mtime_ns,state,error_code,book_id FROM previous_job_items;
CREATE TABLE scan_entries (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL, relative_path TEXT NOT NULL,
 size INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
 PRIMARY KEY(job_id,ordinal), UNIQUE(job_id,relative_path)
);
INSERT INTO scan_entries SELECT * FROM previous_scan_entries;
DROP TABLE previous_job_items;
DROP TABLE previous_scan_entries;
DROP TABLE previous_jobs;
CREATE TABLE job_resources (
 resource_key TEXT PRIMARY KEY, job_id TEXT NOT NULL REFERENCES jobs(id), generation INTEGER NOT NULL
);
CREATE TABLE external_metadata (
 id TEXT PRIMARY KEY, provider TEXT NOT NULL, external_id TEXT NOT NULL,
 normalized_json TEXT NOT NULL, raw_json TEXT NOT NULL CHECK(length(raw_json)<=4194304),
 parser_version INTEGER NOT NULL, fetched_at INTEGER NOT NULL, revision INTEGER NOT NULL,
 UNIQUE(provider,external_id)
);
CREATE TABLE book_metadata_state (
 book_id TEXT PRIMARY KEY REFERENCES books(id), manual_url TEXT NOT NULL DEFAULT '',
 selection_revision INTEGER NOT NULL DEFAULT 1 CHECK(selection_revision>0)
);
CREATE TABLE book_sources (
 book_id TEXT PRIMARY KEY REFERENCES books(id), metadata_id TEXT NOT NULL REFERENCES external_metadata(id),
 url TEXT NOT NULL, match_method TEXT NOT NULL, matched_content_revision INTEGER NOT NULL
);
CREATE TABLE field_overrides (
 book_id TEXT NOT NULL REFERENCES books(id), field TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('value','empty')), value_json TEXT NOT NULL,
 PRIMARY KEY(book_id,field)
);
CREATE TABLE effective_books (
 book_id TEXT PRIMARY KEY REFERENCES books(id), title TEXT NOT NULL, fields_json TEXT NOT NULL
);
INSERT INTO effective_books SELECT id,filename_title,'{}' FROM books;
CREATE TABLE tags (
 id TEXT PRIMARY KEY, namespace TEXT NOT NULL, name TEXT NOT NULL, UNIQUE(namespace,name)
);
CREATE TABLE effective_book_tags (
 book_id TEXT NOT NULL REFERENCES books(id), tag_id TEXT NOT NULL REFERENCES tags(id),
 PRIMARY KEY(book_id,tag_id)
);
CREATE INDEX effective_tags_lookup ON effective_book_tags(tag_id,book_id);
CREATE TABLE translation_versions (
 id TEXT PRIMARY KEY, source TEXT NOT NULL, digest TEXT NOT NULL, cache_key TEXT NOT NULL,
 activated_at INTEGER NOT NULL
);
CREATE TABLE translation_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1), active_version TEXT REFERENCES translation_versions(id)
);
INSERT INTO translation_state VALUES(1,NULL);
CREATE TABLE tag_translations (
 tag_id TEXT NOT NULL REFERENCES tags(id), locale TEXT NOT NULL,
 origin TEXT NOT NULL CHECK(origin IN ('manual','remote')), dataset TEXT NOT NULL DEFAULT '',
 text TEXT, revision INTEGER NOT NULL CHECK(revision>0),
 PRIMARY KEY(tag_id,locale,origin,dataset)
);
CREATE TABLE hash_lookup_cache (
 site TEXT NOT NULL, sha1 TEXT NOT NULL, candidates_json TEXT NOT NULL, expires_at INTEGER NOT NULL,
 PRIMARY KEY(site,sha1)
);
CREATE TABLE events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL, resource_id TEXT NOT NULL,
 revision INTEGER NOT NULL, created_at INTEGER NOT NULL
);
CREATE INDEX events_age ON events(created_at);
CREATE TRIGGER retain_events AFTER INSERT ON events BEGIN
 DELETE FROM events WHERE created_at<unixepoch()-86400;
 DELETE FROM events WHERE id<=COALESCE((SELECT id FROM events ORDER BY id DESC LIMIT 1 OFFSET 10000),0);
END;
CREATE TRIGGER notify_book_insert AFTER INSERT ON books BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('book',NEW.id,NEW.revision,unixepoch());
END;
CREATE TRIGGER notify_book_update AFTER UPDATE ON books BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('book',NEW.id,NEW.revision,unixepoch());
END;
CREATE TRIGGER notify_job_insert AFTER INSERT ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;
CREATE TRIGGER notify_job_update AFTER UPDATE ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;

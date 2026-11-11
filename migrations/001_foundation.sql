CREATE TABLE accounts (
 singleton INTEGER NOT NULL PRIMARY KEY CHECK(singleton = 1),
 id TEXT NOT NULL UNIQUE,
 password_hash TEXT,
 auth_revision INTEGER NOT NULL CHECK(auth_revision > 0),
 password_changed_at INTEGER NOT NULL
);
CREATE TABLE sessions (
 token_hash BLOB NOT NULL PRIMARY KEY CHECK(length(token_hash) = 32),
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL CHECK(expires_at > created_at)
);
CREATE INDEX sessions_expiry ON sessions(expires_at);

CREATE TABLE libraries (
 id TEXT PRIMARY KEY,
 root_key TEXT NOT NULL UNIQUE,
 display_name TEXT NOT NULL,
 legacy_zip_encoding TEXT NOT NULL CHECK(legacy_zip_encoding IN ('utf8','gb18030','shift_jis')),
 scan_interval_minutes INTEGER CHECK(scan_interval_minutes IS NULL OR scan_interval_minutes>=15),
 next_scan_at INTEGER,
 root_identity TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 last_complete_scan_id TEXT,
 last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE books (
 id TEXT PRIMARY KEY,
 library_id TEXT NOT NULL REFERENCES libraries(id),
 relative_path TEXT NOT NULL,
 filename_title TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 content_revision INTEGER NOT NULL CHECK(content_revision>0),
 representative_sha1 TEXT,
 page_count INTEGER NOT NULL CHECK(page_count>=0),
 file_state TEXT NOT NULL CHECK(file_state IN ('available','missing','corrupt','needs_review')),
 error_code TEXT NOT NULL DEFAULT '',
 indexed_at INTEGER NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 UNIQUE(library_id,relative_path)
);
CREATE INDEX books_library_state ON books(library_id,file_state,relative_path,id);
CREATE INDEX books_sha1 ON books(representative_sha1);
CREATE TABLE book_pages (
 book_id TEXT NOT NULL REFERENCES books(id),
 content_revision INTEGER NOT NULL,
 page_index INTEGER NOT NULL CHECK(page_index>0),
 raw_entry_index INTEGER NOT NULL,
 display_name TEXT NOT NULL,
 media_type TEXT NOT NULL,
 PRIMARY KEY(book_id,content_revision,page_index)
);
CREATE TABLE jobs (
 id TEXT PRIMARY KEY,
 library_id TEXT NOT NULL REFERENCES libraries(id),
 state TEXT NOT NULL CHECK(state IN ('queued','running','completed','failed','cancelled')),
 stage TEXT NOT NULL CHECK(stage IN ('enumerating','processing','finalizing','finished')),
 generation INTEGER NOT NULL CHECK(generation>0),
 total INTEGER CHECK(total IS NULL OR total>=0),
 done INTEGER NOT NULL DEFAULT 0 CHECK(done>=0 AND (total IS NULL OR done<=total)),
 failed INTEGER NOT NULL DEFAULT 0 CHECK(failed>=0 AND failed<=done),
 error_code TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX jobs_one_active ON jobs(library_id) WHERE state IN ('queued','running');
CREATE TABLE scan_entries (
 job_id TEXT NOT NULL REFERENCES jobs(id),
 ordinal INTEGER NOT NULL,
 relative_path TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 PRIMARY KEY(job_id,ordinal),
 UNIQUE(job_id,relative_path)
);
CREATE TABLE job_items (
 job_id TEXT NOT NULL REFERENCES jobs(id),
 ordinal INTEGER NOT NULL,
 relative_path TEXT NOT NULL,
 size INTEGER NOT NULL,
 mtime_ns INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','completed','failed')),
 error_code TEXT NOT NULL DEFAULT '',
 book_id TEXT REFERENCES books(id),
 PRIMARY KEY(job_id,ordinal),
 UNIQUE(job_id,relative_path)
);

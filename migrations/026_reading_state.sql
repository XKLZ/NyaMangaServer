CREATE TABLE reading_states(
 account_id TEXT NOT NULL REFERENCES accounts(id),
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 page_index INTEGER CHECK(page_index IS NULL OR page_index>=0),
 recorded_page_count INTEGER CHECK(recorded_page_count IS NULL OR recorded_page_count>0),
 content_revision INTEGER NOT NULL CHECK(content_revision>0),
 completed INTEGER NOT NULL CHECK(completed IN(0,1)),
 completed_at INTEGER,
 last_read_at INTEGER,
 read_count INTEGER NOT NULL CHECK(read_count>=0),
 revision INTEGER NOT NULL CHECK(revision>0),
 CHECK((completed=0 AND completed_at IS NULL) OR (completed=1 AND completed_at IS NOT NULL)),
 CHECK(page_index IS NULL OR (recorded_page_count IS NOT NULL AND page_index<recorded_page_count)),
 PRIMARY KEY(account_id,edition_id)
);
CREATE TABLE reading_sessions(
 id TEXT PRIMARY KEY,
 account_id TEXT NOT NULL REFERENCES accounts(id),
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 idempotency_key TEXT NOT NULL,
 content_revision INTEGER NOT NULL CHECK(content_revision>0),
 started_at INTEGER NOT NULL,
 UNIQUE(account_id,edition_id,idempotency_key)
);
CREATE TABLE reading_state_requests(
 account_id TEXT NOT NULL REFERENCES accounts(id),
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 idempotency_key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 response_json TEXT NOT NULL CHECK(json_valid(response_json)),
 PRIMARY KEY(account_id,edition_id,idempotency_key)
);
CREATE TRIGGER reading_state_insert AFTER INSERT ON reading_states BEGIN
 INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) VALUES('reading_state',NEW.edition_id,NEW.revision,unixepoch(),NEW.account_id);
END;
CREATE TRIGGER reading_state_update AFTER UPDATE ON reading_states BEGIN
 INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) VALUES('reading_state',NEW.edition_id,NEW.revision,unixepoch(),NEW.account_id);
END;

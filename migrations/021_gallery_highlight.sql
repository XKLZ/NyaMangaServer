-- Separate setting preserves the three-feature CHECK and all transition FKs.
CREATE TABLE gallery_highlight_settings(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)),
 revision INTEGER NOT NULL CHECK(revision>0)
);
INSERT INTO gallery_highlight_settings VALUES(1,0,1);
CREATE TABLE gallery_highlight_connections(
 id TEXT PRIMARY KEY,
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash)=32),
 auth_revision INTEGER NOT NULL CHECK(auth_revision>0),
 created_at INTEGER NOT NULL,
 expires_at INTEGER NOT NULL CHECK(expires_at>created_at),
 revoked_at INTEGER,
 revision INTEGER NOT NULL CHECK(revision>0)
);
CREATE INDEX gallery_connections_account ON gallery_highlight_connections(account_id,created_at DESC,id);
CREATE INDEX gallery_sources_metadata ON edition_sources(metadata_id,edition_id);

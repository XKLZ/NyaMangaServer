-- No tables reference connections; rebuild only this leaf to allow real NULL expiry.
CREATE TABLE gallery_highlight_connections_v22(
 id TEXT PRIMARY KEY,
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash)=32),
 auth_revision INTEGER NOT NULL CHECK(auth_revision>0),
 created_at INTEGER NOT NULL,
 expires_at INTEGER CHECK(expires_at IS NULL OR expires_at>created_at),
 revoked_at INTEGER,
 revision INTEGER NOT NULL CHECK(revision>0)
);
INSERT INTO gallery_highlight_connections_v22(id,account_id,name,token_hash,auth_revision,created_at,expires_at,revoked_at,revision)
 SELECT id,account_id,name,token_hash,auth_revision,created_at,expires_at,revoked_at,revision FROM gallery_highlight_connections;
DROP TABLE gallery_highlight_connections;
ALTER TABLE gallery_highlight_connections_v22 RENAME TO gallery_highlight_connections;
CREATE INDEX gallery_connections_account ON gallery_highlight_connections(account_id,created_at DESC,id);

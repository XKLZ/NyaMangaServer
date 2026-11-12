CREATE TABLE device_sessions (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    client_instance_id TEXT NOT NULL,
    device_name TEXT NOT NULL,
    token_hash BLOB NOT NULL UNIQUE CHECK(length(token_hash)=32),
    auth_revision INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    last_active_at INTEGER NOT NULL,
    revoked_at INTEGER
) STRICT;
CREATE INDEX device_sessions_account ON device_sessions(account_id, revoked_at, expires_at);

CREATE TABLE instance_client_identity (
    singleton INTEGER PRIMARY KEY CHECK(singleton=1),
    instance_id TEXT NOT NULL,
    cache_epoch TEXT NOT NULL
) STRICT;
INSERT INTO instance_client_identity VALUES(1,lower(hex(randomblob(16))),lower(hex(randomblob(16))));

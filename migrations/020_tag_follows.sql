CREATE TABLE tag_follows(
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 tag_id TEXT NOT NULL REFERENCES tags(id),
 followed INTEGER NOT NULL CHECK(followed IN(0,1)),
 revision INTEGER NOT NULL CHECK(revision>0),
 PRIMARY KEY(account_id,tag_id)
);
CREATE INDEX tag_follows_active ON tag_follows(account_id,followed,tag_id);
CREATE TABLE tag_follow_epochs(
 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL CHECK(revision>0)
);
ALTER TABLE events ADD COLUMN owner_account_id TEXT REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX events_owner ON events(owner_account_id,id);

-- Rebuild only the legacy identity slot constraint; account IDs and children survive.
CREATE TABLE accounts_next (
 singleton INTEGER NOT NULL PRIMARY KEY CHECK(singleton>0),
 id TEXT NOT NULL UNIQUE,
 password_hash TEXT,
 auth_revision INTEGER NOT NULL CHECK(auth_revision>0),
 password_changed_at INTEGER NOT NULL
);
INSERT INTO accounts_next SELECT * FROM accounts;
DROP TABLE accounts;
ALTER TABLE accounts_next RENAME TO accounts;
CREATE TABLE account_profiles (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id),
 username TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK(length(username) BETWEEN 1 AND 64),
 role TEXT NOT NULL CHECK(role IN('admin','user')),
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)),
 must_change_password INTEGER NOT NULL CHECK(must_change_password IN(0,1)),
 revision INTEGER NOT NULL CHECK(revision>0),
 acl_revision INTEGER NOT NULL CHECK(acl_revision>0)
);
INSERT INTO account_profiles SELECT id,'admin','admin',1,0,1,1 FROM accounts;
CREATE TRIGGER account_profile_create AFTER INSERT ON accounts BEGIN
 INSERT INTO account_profiles VALUES(NEW.id,CASE WHEN NEW.singleton=1 THEN 'admin' ELSE NEW.id END,CASE WHEN NEW.singleton=1 THEN 'admin' ELSE 'user' END,1,0,1,1);
END;
CREATE TABLE account_library_grants (
 account_id TEXT NOT NULL REFERENCES accounts(id),
 library_id TEXT NOT NULL REFERENCES libraries(id),
 role TEXT NOT NULL CHECK(role IN('viewer','editor')),
 PRIMARY KEY(account_id,library_id)
);
CREATE TABLE account_security_audit (
 id INTEGER PRIMARY KEY,
 actor_id TEXT NOT NULL REFERENCES accounts(id),
 target_id TEXT NOT NULL REFERENCES accounts(id),
 action TEXT NOT NULL,
 changes_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(changes_json)),
 created_at INTEGER NOT NULL
);
-- Persist initiator and the permission generation; legacy execution states stay intact.
DROP TRIGGER notify_job_update;
DROP TRIGGER import_batch_event;
ALTER TABLE jobs ADD COLUMN actor_id TEXT REFERENCES accounts(id);
ALTER TABLE jobs ADD COLUMN actor_acl_revision INTEGER NOT NULL DEFAULT 1;
UPDATE jobs SET actor_id=(SELECT id FROM accounts WHERE singleton=1);
ALTER TABLE import_batches ADD COLUMN actor_id TEXT REFERENCES accounts(id);
ALTER TABLE import_batches ADD COLUMN actor_acl_revision INTEGER NOT NULL DEFAULT 1;
UPDATE import_batches SET actor_id=(SELECT id FROM accounts WHERE singleton=1);
CREATE TRIGGER notify_job_update AFTER UPDATE ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;
CREATE TRIGGER import_batch_event AFTER UPDATE ON import_batches BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('import_batch',NEW.id,NEW.revision,unixepoch());
END;

ALTER TABLE events ADD COLUMN acl_library_id TEXT;
UPDATE events SET acl_library_id=CASE type
 WHEN 'edition' THEN (SELECT library_id FROM editions WHERE id=resource_id)
 WHEN 'book' THEN (SELECT library_id FROM editions WHERE id=resource_id)
 WHEN 'cover' THEN (SELECT library_id FROM editions WHERE id=resource_id)
 WHEN 'work' THEN (SELECT library_id FROM works WHERE id=resource_id)
 WHEN 'series' THEN (SELECT library_id FROM series WHERE id=resource_id)
 WHEN 'job' THEN (SELECT library_id FROM jobs WHERE id=resource_id)
 WHEN 'import_batch' THEN (SELECT library_id FROM import_batches WHERE id=resource_id)
 WHEN 'work_proposal' THEN (SELECT library_id FROM work_proposals WHERE id=resource_id)
 WHEN 'series_proposal' THEN (SELECT library_id FROM series_proposals WHERE id=resource_id) END;
DROP TRIGGER state_insert;
DROP TRIGGER state_update;
CREATE TRIGGER state_insert AFTER INSERT ON edition_user_states BEGIN
 UPDATE catalog_state SET revision=revision+1;
 INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) VALUES('user_state',NEW.edition_id,NEW.revision,unixepoch(),NEW.account_id);
END;
CREATE TRIGGER state_update AFTER UPDATE ON edition_user_states BEGIN
 UPDATE catalog_state SET revision=revision+1;
 INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) VALUES('user_state',NEW.edition_id,NEW.revision,unixepoch(),NEW.account_id);
END;
CREATE TRIGGER event_scope AFTER INSERT ON events BEGIN
 UPDATE events SET acl_library_id=CASE NEW.type
 WHEN 'edition' THEN (SELECT library_id FROM editions WHERE id=NEW.resource_id)
 WHEN 'book' THEN (SELECT library_id FROM editions WHERE id=NEW.resource_id)
 WHEN 'cover' THEN (SELECT library_id FROM editions WHERE id=NEW.resource_id)
 WHEN 'work' THEN (SELECT library_id FROM works WHERE id=NEW.resource_id)
 WHEN 'series' THEN (SELECT library_id FROM series WHERE id=NEW.resource_id)
 WHEN 'job' THEN (SELECT library_id FROM jobs WHERE id=NEW.resource_id)
 WHEN 'import_batch' THEN (SELECT library_id FROM import_batches WHERE id=NEW.resource_id)
 WHEN 'source_search' THEN (SELECT b.library_id FROM source_searches s JOIN import_batches b ON b.id=s.batch_id WHERE s.id=NEW.resource_id)
 WHEN 'work_proposal' THEN (SELECT library_id FROM work_proposals WHERE id=NEW.resource_id)
 WHEN 'series_proposal' THEN (SELECT library_id FROM series_proposals WHERE id=NEW.resource_id) END WHERE id=NEW.id;
END;
ALTER TABLE libraries ADD COLUMN scan_actor_id TEXT REFERENCES accounts(id);
UPDATE libraries SET scan_actor_id=(SELECT id FROM accounts WHERE singleton=1);
ALTER TABLE enhancement_transitions ADD COLUMN actor_id TEXT REFERENCES accounts(id);
ALTER TABLE enhancement_transitions ADD COLUMN actor_acl_revision INTEGER NOT NULL DEFAULT 1;
UPDATE enhancement_transitions SET actor_id=(SELECT id FROM accounts WHERE singleton=1);
ALTER TABLE instance_operations ADD COLUMN actor_id TEXT REFERENCES accounts(id);
ALTER TABLE instance_operations ADD COLUMN actor_acl_revision INTEGER NOT NULL DEFAULT 1;
DROP TRIGGER instance_operation_notify;
UPDATE instance_operations SET actor_id=(SELECT id FROM accounts WHERE singleton=1);
CREATE TRIGGER instance_operation_notify AFTER UPDATE ON instance_operations BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('instance_operation',NEW.id,NEW.revision,unixepoch());
END;

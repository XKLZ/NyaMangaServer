CREATE TABLE instance_operations(
 id TEXT PRIMARY KEY,kind TEXT NOT NULL CHECK(kind IN('backup','restore')),
 request_key TEXT NOT NULL UNIQUE,state TEXT NOT NULL,phase TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1,private_json TEXT NOT NULL DEFAULT '{}',
 bytes INTEGER NOT NULL DEFAULT 0,digest TEXT NOT NULL DEFAULT '',error_code TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL
);
CREATE TABLE instance_state(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),owner TEXT,
 background_hold INTEGER NOT NULL DEFAULT 0 CHECK(background_hold IN(0,1)),
 write_epoch INTEGER NOT NULL DEFAULT 0,revision INTEGER NOT NULL DEFAULT 1
);
INSERT INTO instance_state(singleton) VALUES(1);
CREATE TABLE instance_root_links(
 library_id TEXT PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
 source_root_key TEXT NOT NULL,target_root_key TEXT,linked INTEGER NOT NULL CHECK(linked IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1
);
CREATE TRIGGER instance_operation_notify AFTER UPDATE ON instance_operations BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('instance_operation',NEW.id,NEW.revision,unixepoch());
END;

CREATE TABLE health_checks(
 id TEXT PRIMARY KEY,job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id),request_key TEXT NOT NULL UNIQUE,
 mode TEXT NOT NULL CHECK(mode IN('quick','application','sources')),scope_json TEXT NOT NULL,
 rules_version INTEGER NOT NULL DEFAULT 1,phase INTEGER NOT NULL DEFAULT 0,cursor TEXT NOT NULL DEFAULT '',
 checked INTEGER NOT NULL DEFAULT 0,bytes_read INTEGER NOT NULL DEFAULT 0,source_bytes_read INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL,finished_at INTEGER,
 revision INTEGER NOT NULL DEFAULT 1,error_code TEXT NOT NULL DEFAULT ''
);
CREATE TABLE health_findings(
 id TEXT PRIMARY KEY,check_id TEXT NOT NULL REFERENCES health_checks(id),category TEXT NOT NULL,
 object_kind TEXT NOT NULL,object_id TEXT NOT NULL,object_revision INTEGER NOT NULL DEFAULT 0,fingerprint TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN('normal','confirmed','inaccessible','manual','not_checked','stale')),
 code TEXT NOT NULL,repair TEXT NOT NULL DEFAULT '',private_json TEXT NOT NULL DEFAULT '{}',details_json TEXT NOT NULL DEFAULT '{}',checked_at INTEGER NOT NULL,
 UNIQUE(check_id,category,object_kind,object_id,code)
);
CREATE INDEX health_findings_page ON health_findings(check_id,status,category,id);
CREATE TABLE health_repair_plans(
 id TEXT PRIMARY KEY,request_key TEXT NOT NULL UNIQUE,check_id TEXT NOT NULL REFERENCES health_checks(id),
 input_json TEXT NOT NULL,summary_json TEXT NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,
 revision INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE health_repairs(
 id TEXT PRIMARY KEY,plan_id TEXT NOT NULL REFERENCES health_repair_plans(id),job_id TEXT NOT NULL UNIQUE REFERENCES jobs(id),
 request_key TEXT NOT NULL UNIQUE,rollback_request_key TEXT NOT NULL DEFAULT '',operation TEXT NOT NULL DEFAULT 'repair',state TEXT NOT NULL,revision INTEGER NOT NULL DEFAULT 1,
 checkpoint INTEGER NOT NULL DEFAULT 0,error_code TEXT NOT NULL DEFAULT '',created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL
);
CREATE TABLE health_repair_steps(
 repair_id TEXT NOT NULL REFERENCES health_repairs(id),ordinal INTEGER NOT NULL,action_json TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending',before_key TEXT NOT NULL DEFAULT '',after_fingerprint TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',PRIMARY KEY(repair_id,ordinal)
);
CREATE TABLE health_state(
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),phase TEXT NOT NULL,repair_id TEXT,
 revision INTEGER NOT NULL DEFAULT 1,updated_at INTEGER NOT NULL
);
INSERT INTO health_state VALUES(1,'idle',NULL,1,unixepoch());
CREATE TABLE health_paused_jobs(
 repair_id TEXT NOT NULL REFERENCES health_repairs(id),job_id TEXT NOT NULL,old_state TEXT NOT NULL,old_generation INTEGER NOT NULL,
 PRIMARY KEY(repair_id,job_id)
);
CREATE TABLE health_artifacts(
 key TEXT PRIMARY KEY,repair_id TEXT NOT NULL REFERENCES health_repairs(id),digest TEXT NOT NULL,size INTEGER NOT NULL,
 role TEXT NOT NULL,pinned INTEGER NOT NULL DEFAULT 1 CHECK(pinned IN(0,1))
);

CREATE TABLE health_check_objects(
 check_id TEXT NOT NULL REFERENCES health_checks(id),kind TEXT NOT NULL,object_id TEXT NOT NULL,fingerprint TEXT NOT NULL,object_revision INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(check_id,kind,object_id)
);

CREATE TRIGGER health_state_notify AFTER UPDATE ON health_state BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('health_state','global',NEW.revision,unixepoch());
END;
CREATE TRIGGER health_repair_notify AFTER UPDATE ON health_repairs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('health_repair',NEW.id,NEW.revision,unixepoch());
END;

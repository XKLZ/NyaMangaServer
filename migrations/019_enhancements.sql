CREATE TABLE enhancement_revision(singleton INTEGER PRIMARY KEY CHECK(singleton=1),revision INTEGER NOT NULL CHECK(revision>0));
INSERT INTO enhancement_revision VALUES(1,1);
CREATE TABLE enhancement_settings(
 feature TEXT PRIMARY KEY CHECK(feature IN('work_recommendations','series_recommendations','chinese_dictionary')),
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)),
 state TEXT NOT NULL CHECK(state IN('stable','enabling','disabling','failed')),
 transition_id TEXT,
 attempt INTEGER NOT NULL DEFAULT 0,
 runtime_armed INTEGER NOT NULL DEFAULT 0 CHECK(runtime_armed IN(0,1))
);
INSERT INTO enhancement_settings(feature,enabled,state) VALUES('work_recommendations',0,'stable'),('series_recommendations',0,'stable'),('chinese_dictionary',1,'stable');
CREATE TABLE enhancement_transitions(
 id TEXT PRIMARY KEY,feature TEXT NOT NULL REFERENCES enhancement_settings(feature),attempt INTEGER NOT NULL,
 state TEXT NOT NULL CHECK(state IN('pending','completed','failed')),target_enabled INTEGER NOT NULL DEFAULT 0 CHECK(target_enabled IN(0,1)),error_code TEXT NOT NULL DEFAULT '',
 affected_tasks INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL
);
CREATE TABLE import_enhancements(
 batch_id TEXT PRIMARY KEY REFERENCES import_batches(id) ON DELETE CASCADE,
 versions_enabled INTEGER NOT NULL CHECK(versions_enabled IN(0,1)),series_enabled INTEGER NOT NULL CHECK(series_enabled IN(0,1))
);

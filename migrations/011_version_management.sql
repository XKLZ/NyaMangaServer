-- Manual strings remain untouched. Automatic presets have separate ownership.
CREATE TABLE edition_label_policy(
 edition_id TEXT PRIMARY KEY REFERENCES editions(id) ON DELETE CASCADE,
 mode TEXT NOT NULL CHECK(mode IN('legacy','auto','manual')),
 revision INTEGER NOT NULL DEFAULT 1,rule_version TEXT NOT NULL DEFAULT '',
 needs_review INTEGER NOT NULL DEFAULT 0 CHECK(needs_review IN(0,1))
);
INSERT INTO edition_label_policy(edition_id,mode) SELECT id,CASE WHEN EXISTS(SELECT 1 FROM edition_version_labels v WHERE v.edition_id=editions.id) THEN 'manual' ELSE 'legacy' END FROM editions;
CREATE TABLE edition_automatic_labels(
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 position INTEGER NOT NULL,preset_id TEXT NOT NULL,label TEXT NOT NULL,
 PRIMARY KEY(edition_id,preset_id),UNIQUE(edition_id,position)
);
-- A running incremental task pins its complete baseline until copied blocks exist.
ALTER TABLE work_discovery_runs ADD COLUMN base_job_id TEXT REFERENCES work_discovery_runs(job_id) ON DELETE RESTRICT;
ALTER TABLE work_discovery_runs ADD COLUMN execution_version TEXT NOT NULL DEFAULT '';
ALTER TABLE work_discovery_runs ADD COLUMN reused_pairs INTEGER NOT NULL DEFAULT 0;
ALTER TABLE work_discovery_runs ADD COLUMN computed_pairs INTEGER NOT NULL DEFAULT 0;
ALTER TABLE work_discovery_runs ADD COLUMN reused_features INTEGER NOT NULL DEFAULT 0;
ALTER TABLE work_discovery_runs ADD COLUMN prepared_features INTEGER NOT NULL DEFAULT 0;
CREATE TABLE discovery_feature_inputs(
 edition_id TEXT PRIMARY KEY REFERENCES editions(id) ON DELETE CASCADE,
 input_key TEXT NOT NULL,result_json TEXT NOT NULL
);
CREATE TABLE discovery_run_features(
 job_id TEXT NOT NULL REFERENCES work_discovery_runs(job_id) ON DELETE CASCADE,
 edition_id TEXT NOT NULL,input_key TEXT NOT NULL,
 PRIMARY KEY(job_id,edition_id)
);

-- Pair signal generations refer only to bounded, content-addressed private blocks.
CREATE TABLE series_evidence_generations(
 job_id TEXT PRIMARY KEY REFERENCES series_discovery_runs(job_id) ON DELETE CASCADE,
 library_id TEXT NOT NULL,execution_version TEXT NOT NULL,
 profiles_json TEXT NOT NULL DEFAULT '{}',
 base_job_id TEXT REFERENCES series_evidence_generations(job_id) ON DELETE RESTRICT,
 bytes INTEGER NOT NULL DEFAULT 0 CHECK(bytes>=0 AND bytes<=134217728),
 computed_pairs INTEGER NOT NULL DEFAULT 0,reused_pairs INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL DEFAULT 'building' CHECK(state IN('building','complete')),
 created_at INTEGER NOT NULL DEFAULT(unixepoch())
);
CREATE TABLE series_evidence_blocks(
 job_id TEXT NOT NULL REFERENCES series_evidence_generations(job_id) ON DELETE CASCADE,
 block_key TEXT NOT NULL,storage_key TEXT NOT NULL,sha256 TEXT NOT NULL,
 size INTEGER NOT NULL CHECK(size>0),records INTEGER NOT NULL CHECK(records>=0),
 PRIMARY KEY(job_id,block_key)
);
CREATE TABLE discovery_preprocessing(
 job_id TEXT PRIMARY KEY REFERENCES work_discovery_runs(job_id) ON DELETE CASCADE,
 fingerprints_json TEXT NOT NULL,version TEXT NOT NULL
);

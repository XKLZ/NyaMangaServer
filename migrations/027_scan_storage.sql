-- Parsing identity is independent of physical content identity. Old values and
-- scan history remain intact; no automatic scan, retry or deletion is scheduled.
ALTER TABLE files ADD COLUMN encoding_override TEXT CHECK(encoding_override IS NULL OR encoding_override IN('utf8','gb18030','shift_jis'));
ALTER TABLE files ADD COLUMN parsing_revision INTEGER NOT NULL DEFAULT 1 CHECK(parsing_revision>0);
ALTER TABLE files ADD COLUMN parser_version TEXT NOT NULL DEFAULT 'natural-bmp-v2';
ALTER TABLE files ADD COLUMN directory_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE files ADD COLUMN effective_encoding TEXT NOT NULL DEFAULT '';
ALTER TABLE cover_selections ADD COLUMN parsing_revision INTEGER NOT NULL DEFAULT 1 CHECK(parsing_revision>0);
ALTER TABLE reading_states ADD COLUMN parsing_revision INTEGER NOT NULL DEFAULT 1 CHECK(parsing_revision>0);
ALTER TABLE reading_sessions ADD COLUMN parsing_revision INTEGER NOT NULL DEFAULT 1 CHECK(parsing_revision>0);
ALTER TABLE job_items ADD COLUMN scan_result_json TEXT NOT NULL DEFAULT '' CHECK(scan_result_json='' OR json_valid(scan_result_json));
-- Compact receipts outlive the large files and are included in normal backups.
CREATE TABLE storage_material_receipts (
 id TEXT PRIMARY KEY, kind TEXT NOT NULL, operation_id TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state IN('cleaning','cleaned')), manifest_json TEXT NOT NULL CHECK(json_valid(manifest_json)),
 released_bytes INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL
);

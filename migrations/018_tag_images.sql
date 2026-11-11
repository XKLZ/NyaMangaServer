-- Per-role tombstones retain independent monotonic CAS generations.
CREATE TABLE tag_images (
 tag_id TEXT NOT NULL REFERENCES tags(id),
 role TEXT NOT NULL CHECK(role IN ('foreground','background')),
 asset_id TEXT REFERENCES assets(id),
 width INTEGER NOT NULL DEFAULT 0 CHECK(width>=0),
 height INTEGER NOT NULL DEFAULT 0 CHECK(height>=0),
 format TEXT NOT NULL DEFAULT '' CHECK(format IN ('','jpeg','png','webp','bmp')),
 revision INTEGER NOT NULL CHECK(revision>0),
 PRIMARY KEY(tag_id,role),
 CHECK((asset_id IS NULL AND width=0 AND height=0 AND format='') OR (asset_id IS NOT NULL AND width>0 AND height>0 AND format!=''))
);
CREATE INDEX tag_images_asset ON tag_images(asset_id) WHERE asset_id IS NOT NULL;

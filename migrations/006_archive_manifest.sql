-- Page directories are disposable archive-session data, not business records.
ALTER TABLE books ADD COLUMN representative_algorithm TEXT NOT NULL DEFAULT 'first-page-v1';
ALTER TABLE books ADD COLUMN legacy_cover_sha1 TEXT;
UPDATE books SET legacy_cover_sha1=representative_sha1;
ALTER TABLE cover_selections ADD COLUMN entry_index INTEGER;
ALTER TABLE cover_selections ADD COLUMN entry_name TEXT;
ALTER TABLE cover_selections ADD COLUMN directory_version TEXT NOT NULL DEFAULT 'natural-v1';
UPDATE cover_selections SET
 entry_index=(SELECT raw_entry_index FROM book_pages p WHERE p.book_id=cover_selections.book_id AND p.content_revision=cover_selections.content_revision AND p.page_index=cover_selections.page_index),
 entry_name=(SELECT display_name FROM book_pages p WHERE p.book_id=cover_selections.book_id AND p.content_revision=cover_selections.content_revision AND p.page_index=cover_selections.page_index)
 WHERE mode='page';
DROP TABLE book_pages;

CREATE TABLE version_member_labels (
 book_id TEXT NOT NULL REFERENCES version_members(book_id) ON DELETE CASCADE,
 position INTEGER NOT NULL CHECK(position>=0 AND position<20),
 label TEXT NOT NULL CHECK(length(label)>0 AND length(label)<=128),
 PRIMARY KEY(book_id,position), UNIQUE(book_id,label)
);
INSERT INTO version_member_labels SELECT book_id,0,version_label FROM version_members WHERE version_label<>'';

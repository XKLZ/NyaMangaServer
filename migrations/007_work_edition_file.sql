-- Work identity is independent of Edition/File availability and cardinality.
PRAGMA defer_foreign_keys=ON;

DROP TRIGGER book_delete_catalog;

DROP TRIGGER book_management_catalog;

DROP TRIGGER catalog_book_update;

DROP TRIGGER catalog_event;

DROP TRIGGER catalog_source_insert;

DROP TRIGGER catalog_source_update;

DROP TRIGGER catalog_tag_delete;

DROP TRIGGER catalog_tag_insert;

DROP TRIGGER document_insert;

DROP TRIGGER document_update;

DROP TRIGGER library_management_catalog;

DROP TRIGGER notify_book_insert;

DROP TRIGGER notify_book_update;

DROP TRIGGER notify_job_insert;

DROP TRIGGER notify_job_update;

DROP TRIGGER organize_new_book;

DROP TRIGGER retain_events;

DROP TRIGGER source_delete_catalog;

DROP TRIGGER state_insert;

DROP TRIGGER state_update;

DROP TRIGGER tag_search_insert;

CREATE TABLE works (
 id TEXT PRIMARY KEY NOT NULL, library_id TEXT NOT NULL REFERENCES libraries(id),
 name_override TEXT CHECK(name_override IS NULL OR (length(name_override)>0 AND length(name_override)<=512)),
 preferred_edition_id TEXT NOT NULL, revision INTEGER NOT NULL CHECK(revision>0),
 UNIQUE(id,library_id),
 FOREIGN KEY(preferred_edition_id,id) REFERENCES editions(id,work_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE TABLE editions (
 id TEXT PRIMARY KEY NOT NULL, library_id TEXT NOT NULL REFERENCES libraries(id),work_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),organization_revision INTEGER NOT NULL CHECK(organization_revision>0),trashed_at INTEGER,
 UNIQUE(id,work_id),UNIQUE(id,library_id),
 FOREIGN KEY(work_id,library_id) REFERENCES works(id,library_id) DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(id) REFERENCES files(edition_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX editions_work ON editions(work_id,id);
CREATE INDEX editions_trash ON editions(trashed_at,library_id,id);
CREATE TABLE files (
 edition_id TEXT PRIMARY KEY NOT NULL,library_id TEXT NOT NULL,
 relative_path TEXT NOT NULL,filename_title TEXT NOT NULL,size INTEGER NOT NULL,mtime_ns INTEGER NOT NULL,
 content_revision INTEGER NOT NULL CHECK(content_revision>0),representative_sha1 TEXT,
 representative_algorithm TEXT NOT NULL,legacy_cover_sha1 TEXT,page_count INTEGER NOT NULL CHECK(page_count>=0),
 file_state TEXT NOT NULL CHECK(file_state IN('available','missing','corrupt','needs_review')),
 error_code TEXT NOT NULL DEFAULT '',indexed_at INTEGER NOT NULL,
 UNIQUE(library_id,relative_path),
 FOREIGN KEY(edition_id,library_id) REFERENCES editions(id,library_id) DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX files_library_state ON files(library_id,file_state,relative_path,edition_id);
CREATE INDEX files_sha1 ON files(representative_sha1);
INSERT INTO works SELECT l.id,l.library_id,NULL,
 COALESCE((SELECT m.book_id FROM version_groups g JOIN version_members m ON m.group_id=g.id WHERE g.item_id=l.id AND m.is_primary=1),(SELECT min(bi.book_id) FROM book_items bi WHERE bi.item_id=l.id)),
 max(l.revision,COALESCE((SELECT g.revision FROM version_groups g WHERE g.item_id=l.id),1)) FROM logical_items l;
INSERT INTO editions SELECT b.id,b.library_id,bi.item_id,b.revision,COALESCE(m.revision,1),b.trashed_at FROM books b JOIN book_items bi ON bi.book_id=b.id LEFT JOIN version_members m ON m.book_id=b.id;
INSERT INTO files SELECT id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,representative_algorithm,legacy_cover_sha1,page_count,file_state,error_code,indexed_at FROM books;
CREATE TABLE edition_version_labels (
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 position INTEGER NOT NULL CHECK(position>=0 AND position<20),label TEXT NOT NULL CHECK(length(label)>0 AND length(label)<=128),
 PRIMARY KEY(edition_id,position),UNIQUE(edition_id,label)
);
INSERT INTO edition_version_labels SELECT book_id,position,label FROM version_member_labels;

ALTER TABLE book_metadata_state RENAME TO v6_book_metadata_state;

CREATE TABLE edition_metadata_state (
 edition_id TEXT PRIMARY KEY REFERENCES editions(id), manual_url TEXT NOT NULL DEFAULT '',
 selection_revision INTEGER NOT NULL DEFAULT 1 CHECK(selection_revision>0)
);

INSERT INTO edition_metadata_state SELECT * FROM v6_book_metadata_state;

DROP TABLE v6_book_metadata_state;

ALTER TABLE book_sources RENAME TO v6_book_sources;

CREATE TABLE edition_sources (
 edition_id TEXT PRIMARY KEY REFERENCES editions(id), metadata_id TEXT NOT NULL REFERENCES external_metadata(id),
 url TEXT NOT NULL, match_method TEXT NOT NULL, matched_content_revision INTEGER NOT NULL
);

INSERT INTO edition_sources SELECT * FROM v6_book_sources;

DROP TABLE v6_book_sources;

ALTER TABLE book_user_states RENAME TO v6_book_user_states;

CREATE TABLE edition_user_states(account_id TEXT NOT NULL REFERENCES accounts(id),edition_id TEXT NOT NULL REFERENCES editions(id),favorite INTEGER NOT NULL CHECK(favorite IN (0,1)),hidden INTEGER NOT NULL CHECK(hidden IN (0,1)),revision INTEGER NOT NULL CHECK(revision>0),PRIMARY KEY(account_id,edition_id));

INSERT INTO edition_user_states SELECT * FROM v6_book_user_states;

DROP TABLE v6_book_user_states;

ALTER TABLE cover_selections RENAME TO v6_cover_selections;

CREATE TABLE cover_selections(edition_id TEXT PRIMARY KEY REFERENCES editions(id),mode TEXT NOT NULL CHECK(mode IN('auto','page','asset')),page_index INTEGER,content_revision INTEGER NOT NULL,asset_id TEXT,revision INTEGER NOT NULL DEFAULT 1, entry_index INTEGER, entry_name TEXT, directory_version TEXT NOT NULL DEFAULT 'natural-v1');

INSERT INTO cover_selections SELECT * FROM v6_cover_selections;

DROP TABLE v6_cover_selections;

ALTER TABLE effective_book_languages RENAME TO v6_effective_book_languages;

CREATE TABLE effective_edition_languages (
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 language TEXT NOT NULL,position INTEGER NOT NULL CHECK(position>=0),
 PRIMARY KEY(edition_id,language),UNIQUE(edition_id,position)
);

INSERT INTO effective_edition_languages SELECT * FROM v6_effective_book_languages;

DROP TABLE v6_effective_book_languages;

ALTER TABLE effective_book_properties RENAME TO v6_effective_book_properties;

CREATE TABLE effective_edition_properties (
 edition_id TEXT PRIMARY KEY REFERENCES editions(id) ON DELETE CASCADE,
 translated INTEGER CHECK(translated IN (0,1)),
 color_mode TEXT CHECK(color_mode IN ('monochrome','color')),
 censorship TEXT CHECK(censorship IN ('uncensored','mosaic','full')),
 extraneous_ads INTEGER CHECK(extraneous_ads IN (0,1)),
 rule_version INTEGER NOT NULL,
 properties_json TEXT NOT NULL
);

INSERT INTO effective_edition_properties SELECT * FROM v6_effective_book_properties;

DROP TABLE v6_effective_book_properties;

ALTER TABLE effective_book_tags RENAME TO v6_effective_book_tags;

CREATE TABLE effective_edition_tags (
 edition_id TEXT NOT NULL REFERENCES editions(id), tag_id TEXT NOT NULL REFERENCES tags(id),
 PRIMARY KEY(edition_id,tag_id)
);

INSERT INTO effective_edition_tags SELECT * FROM v6_effective_book_tags;

DROP TABLE v6_effective_book_tags;

ALTER TABLE effective_books RENAME TO v6_effective_books;

CREATE TABLE effective_editions (
 edition_id TEXT PRIMARY KEY REFERENCES editions(id), title TEXT NOT NULL, fields_json TEXT NOT NULL
);

INSERT INTO effective_editions SELECT * FROM v6_effective_books;

DROP TABLE v6_effective_books;

ALTER TABLE field_overrides RENAME TO v6_field_overrides;

CREATE TABLE field_overrides (
 edition_id TEXT NOT NULL REFERENCES editions(id), field TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('value','empty')), value_json TEXT NOT NULL,
 PRIMARY KEY(edition_id,field)
);

INSERT INTO field_overrides SELECT * FROM v6_field_overrides;

DROP TABLE v6_field_overrides;

ALTER TABLE job_items RENAME TO v6_job_items;

CREATE TABLE job_items (
 job_id TEXT NOT NULL REFERENCES jobs(id), ordinal INTEGER NOT NULL,
 relative_path TEXT NOT NULL DEFAULT '', size INTEGER NOT NULL DEFAULT 0, mtime_ns INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('pending','completed','failed','deferred')),
 error_code TEXT NOT NULL DEFAULT '', edition_id TEXT REFERENCES editions(id),
 content_revision INTEGER NOT NULL DEFAULT 0, selection_revision INTEGER NOT NULL DEFAULT 0,
 input_json TEXT NOT NULL DEFAULT '{}', candidates_json TEXT NOT NULL DEFAULT '[]',
 attempt INTEGER NOT NULL DEFAULT 0, preview_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(job_id,ordinal)
);

INSERT INTO job_items SELECT * FROM v6_job_items;

DROP TABLE v6_job_items;

ALTER TABLE search_documents RENAME TO v6_search_documents;

CREATE TABLE search_documents(edition_id TEXT PRIMARY KEY REFERENCES editions(id),title TEXT NOT NULL,original_title TEXT NOT NULL,sort_key BLOB NOT NULL);

INSERT INTO search_documents SELECT * FROM v6_search_documents;

DROP TABLE v6_search_documents;

ALTER TABLE series_members RENAME TO v6_series_members;

CREATE TABLE series_members(series_id TEXT NOT NULL REFERENCES series(id),work_id TEXT NOT NULL UNIQUE REFERENCES works(id) DEFERRABLE INITIALLY DEFERRED,position INTEGER NOT NULL,PRIMARY KEY(series_id,work_id),UNIQUE(series_id,position));

INSERT INTO series_members SELECT * FROM v6_series_members;

DROP TABLE v6_series_members;

ALTER TABLE source_previews RENAME TO v6_source_previews;

CREATE TABLE source_previews(id TEXT PRIMARY KEY,edition_id TEXT NOT NULL REFERENCES editions(id),job_id TEXT NOT NULL REFERENCES jobs(id),result_json TEXT,expected_revision INTEGER NOT NULL,selection_revision INTEGER NOT NULL,content_revision INTEGER NOT NULL,expires_at INTEGER NOT NULL,state TEXT NOT NULL DEFAULT 'pending');

INSERT INTO source_previews SELECT * FROM v6_source_previews;

DROP TABLE v6_source_previews;

-- Existing maintenance checkpoints keep their inputs, with aggregate CAS keys
-- translated before the old group IDs disappear. Already-stale plans stay stale.
UPDATE maintenance_plans SET input_json=json_set(input_json,'$.expected_revisions',json((
 SELECT json_group_object(new_key,wanted) FROM (
  SELECT CASE WHEN substr(j.key,1,5)='item:' THEN 'work:'||substr(j.key,6)
              WHEN substr(j.key,1,6)='group:' THEN 'work:'||COALESCE(g.item_id,'missing:'||substr(j.key,7))
              ELSE j.key END new_key,
         min(CASE WHEN substr(j.key,1,5)='item:' THEN CASE WHEN l.revision=j.value THEN w.revision ELSE -1 END
                  WHEN substr(j.key,1,6)='group:' THEN CASE WHEN g.revision=j.value THEN w.revision ELSE -1 END
                  ELSE j.value END) wanted
  FROM json_each(maintenance_plans.input_json,'$.expected_revisions') j
  LEFT JOIN logical_items l ON substr(j.key,1,5)='item:' AND l.id=substr(j.key,6)
  LEFT JOIN version_groups g ON substr(j.key,1,6)='group:' AND g.id=substr(j.key,7)
  LEFT JOIN works w ON w.id=COALESCE(l.id,g.item_id)
  GROUP BY new_key
 )
))) WHERE json_type(input_json,'$.expected_revisions')='object';

DROP TABLE version_member_labels;

DROP TABLE version_members;

DROP TABLE version_groups;

DROP TABLE book_items;

DROP TABLE logical_items;

DROP TABLE books;

CREATE INDEX documents_title_order ON search_documents(sort_key,edition_id);

CREATE INDEX effective_tags_lookup ON effective_edition_tags(tag_id,edition_id);

CREATE INDEX languages_filter ON effective_edition_languages(language,edition_id);

CREATE INDEX user_state_filter ON edition_user_states(account_id,hidden,favorite,edition_id);

CREATE TRIGGER catalog_event AFTER UPDATE ON catalog_state BEGIN INSERT INTO events(type,resource_id,revision,created_at) VALUES('catalog','catalog',NEW.revision,unixepoch()); END;

CREATE TRIGGER catalog_source_insert AFTER INSERT ON edition_sources BEGIN UPDATE catalog_state SET revision=revision+1; END;

CREATE TRIGGER catalog_source_update AFTER UPDATE ON edition_sources BEGIN UPDATE catalog_state SET revision=revision+1; END;

CREATE TRIGGER catalog_tag_delete AFTER DELETE ON effective_edition_tags BEGIN UPDATE catalog_state SET revision=revision+1; END;

CREATE TRIGGER catalog_tag_insert AFTER INSERT ON effective_edition_tags BEGIN UPDATE catalog_state SET revision=revision+1; END;

CREATE TRIGGER library_management_catalog AFTER UPDATE OF disabled,trashed_at ON libraries BEGIN UPDATE catalog_state SET revision=revision+1; END;

CREATE TRIGGER notify_job_insert AFTER INSERT ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;

CREATE TRIGGER notify_job_update AFTER UPDATE ON jobs BEGIN
 INSERT INTO events(type,resource_id,revision,created_at) VALUES('job',NEW.id,NEW.updated_at,unixepoch());
END;

CREATE TRIGGER retain_events AFTER INSERT ON events BEGIN
 DELETE FROM events WHERE created_at<unixepoch()-86400;
 DELETE FROM events WHERE id<=COALESCE((SELECT id FROM events ORDER BY id DESC LIMIT 1 OFFSET 10000),0);
END;

CREATE TRIGGER source_delete_catalog AFTER DELETE ON edition_sources BEGIN UPDATE catalog_state SET revision=revision+1; END;

CREATE TRIGGER state_insert AFTER INSERT ON edition_user_states BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('user_state',NEW.edition_id,NEW.revision,unixepoch()); END;

CREATE TRIGGER state_update AFTER UPDATE ON edition_user_states BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('user_state',NEW.edition_id,NEW.revision,unixepoch()); END;

CREATE TRIGGER tag_search_insert AFTER INSERT ON tags BEGIN INSERT INTO search_tags VALUES(NEW.id,NEW.namespace,nya_normalize_v1(NEW.name)); END;

CREATE TRIGGER notify_edition_insert AFTER INSERT ON editions BEGIN INSERT INTO events(type,resource_id,revision,created_at) VALUES('edition',NEW.id,NEW.revision,unixepoch()); END;
CREATE TRIGGER notify_edition_update AFTER UPDATE ON editions BEGIN INSERT INTO events(type,resource_id,revision,created_at) VALUES('edition',NEW.id,NEW.revision,unixepoch()); END;
CREATE TRIGGER edition_management_catalog AFTER UPDATE OF trashed_at ON editions BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER edition_delete_catalog AFTER DELETE ON editions BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('edition',OLD.id,OLD.revision+1,unixepoch()); END;
CREATE TRIGGER edition_capacity_insert AFTER INSERT ON editions WHEN (SELECT count(*) FROM editions WHERE work_id=NEW.work_id)>100 BEGIN SELECT RAISE(ABORT,'organization_capacity'); END;
CREATE TRIGGER edition_capacity_move AFTER UPDATE OF work_id ON editions WHEN (SELECT count(*) FROM editions WHERE work_id=NEW.work_id)>100 BEGIN SELECT RAISE(ABORT,'organization_capacity'); END;
CREATE TRIGGER file_catalog_update AFTER UPDATE OF filename_title,file_state,indexed_at,content_revision ON files BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER work_catalog_update AFTER UPDATE ON works BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('work',NEW.id,NEW.revision,unixepoch()); END;
CREATE TRIGGER document_insert AFTER INSERT ON effective_editions BEGIN
 INSERT INTO search_documents VALUES(NEW.edition_id,nya_normalize_v1(NEW.title),nya_normalize_v1(COALESCE(json_extract(NEW.fields_json,'$.title_original'),'')),nya_natural_v1(NEW.title)) ON CONFLICT(edition_id) DO UPDATE SET title=excluded.title,original_title=excluded.original_title,sort_key=excluded.sort_key;
 UPDATE catalog_state SET revision=revision+1;
END;
CREATE TRIGGER document_update AFTER UPDATE ON effective_editions BEGIN
 UPDATE search_documents SET title=nya_normalize_v1(NEW.title),original_title=nya_normalize_v1(COALESCE(json_extract(NEW.fields_json,'$.title_original'),'')),sort_key=nya_natural_v1(NEW.title) WHERE edition_id=NEW.edition_id;
 UPDATE catalog_state SET revision=revision+1;
END;
UPDATE catalog_state SET revision=revision+1;

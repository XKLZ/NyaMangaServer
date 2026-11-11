-- M4 is additive. Version-one Unicode functions are shared with application writes.
CREATE TABLE logical_items(id TEXT PRIMARY KEY,library_id TEXT NOT NULL REFERENCES libraries(id),kind TEXT NOT NULL CHECK(kind IN ('standalone','group')),revision INTEGER NOT NULL CHECK(revision>0));
CREATE TABLE book_items(book_id TEXT PRIMARY KEY REFERENCES books(id),item_id TEXT NOT NULL REFERENCES logical_items(id));
CREATE INDEX book_items_item ON book_items(item_id,book_id);
CREATE TABLE version_groups(id TEXT PRIMARY KEY,item_id TEXT NOT NULL UNIQUE REFERENCES logical_items(id),library_id TEXT NOT NULL REFERENCES libraries(id),revision INTEGER NOT NULL CHECK(revision>0));
CREATE TABLE version_members(group_id TEXT NOT NULL REFERENCES version_groups(id),book_id TEXT NOT NULL UNIQUE REFERENCES books(id),is_primary INTEGER NOT NULL CHECK(is_primary IN (0,1)),version_label TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL CHECK(revision>0),PRIMARY KEY(group_id,book_id));
CREATE UNIQUE INDEX version_one_primary ON version_members(group_id) WHERE is_primary=1;
CREATE TABLE series(id TEXT PRIMARY KEY,library_id TEXT NOT NULL REFERENCES libraries(id),name TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>0));
CREATE TABLE series_members(series_id TEXT NOT NULL REFERENCES series(id),item_id TEXT NOT NULL UNIQUE REFERENCES logical_items(id),position INTEGER NOT NULL,PRIMARY KEY(series_id,item_id),UNIQUE(series_id,position));
CREATE TABLE book_user_states(account_id TEXT NOT NULL REFERENCES accounts(id),book_id TEXT NOT NULL REFERENCES books(id),favorite INTEGER NOT NULL CHECK(favorite IN (0,1)),hidden INTEGER NOT NULL CHECK(hidden IN (0,1)),revision INTEGER NOT NULL CHECK(revision>0),PRIMARY KEY(account_id,book_id));
CREATE INDEX user_state_filter ON book_user_states(account_id,hidden,favorite,book_id);
CREATE TABLE saved_searches(id TEXT PRIMARY KEY,account_id TEXT NOT NULL REFERENCES accounts(id),name TEXT NOT NULL,query_version INTEGER NOT NULL,conditions_json TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>0));
CREATE INDEX saved_search_account ON saved_searches(account_id,id);
CREATE TABLE search_documents(book_id TEXT PRIMARY KEY REFERENCES books(id),title TEXT NOT NULL,original_title TEXT NOT NULL,sort_key BLOB NOT NULL);
CREATE INDEX documents_title_order ON search_documents(sort_key,book_id);
CREATE TABLE search_tags(tag_id TEXT PRIMARY KEY REFERENCES tags(id),namespace TEXT NOT NULL,name TEXT NOT NULL);
CREATE INDEX search_tags_prefix ON search_tags(namespace,name,tag_id);
CREATE TABLE catalog_state(singleton INTEGER PRIMARY KEY CHECK(singleton=1),revision INTEGER NOT NULL CHECK(revision>0));
INSERT INTO catalog_state VALUES(1,1);
INSERT INTO logical_items SELECT 'i:'||id,library_id,'standalone',1 FROM books;
INSERT INTO book_items SELECT id,'i:'||id FROM books;
INSERT INTO search_documents SELECT b.id,nya_normalize_v1(COALESCE(e.title,b.filename_title)),nya_normalize_v1(COALESCE(json_extract(e.fields_json,'$.title_original'),'')),nya_natural_v1(COALESCE(e.title,b.filename_title)) FROM books b LEFT JOIN effective_books e ON e.book_id=b.id;
INSERT INTO search_tags SELECT id,namespace,nya_normalize_v1(name) FROM tags;
CREATE TRIGGER catalog_event AFTER UPDATE ON catalog_state BEGIN INSERT INTO events(type,resource_id,revision,created_at) VALUES('catalog','catalog',NEW.revision,unixepoch()); END;
CREATE TRIGGER organize_new_book AFTER INSERT ON books BEGIN
 INSERT INTO logical_items VALUES('i:'||NEW.id,NEW.library_id,'standalone',1);
 INSERT INTO book_items VALUES(NEW.id,'i:'||NEW.id);
 INSERT INTO search_documents VALUES(NEW.id,nya_normalize_v1(NEW.filename_title),'',nya_natural_v1(NEW.filename_title));
 UPDATE catalog_state SET revision=revision+1;
END;
CREATE TRIGGER catalog_book_update AFTER UPDATE OF filename_title,file_state,indexed_at,content_revision ON books BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER document_insert AFTER INSERT ON effective_books BEGIN
 INSERT INTO search_documents VALUES(NEW.book_id,nya_normalize_v1(NEW.title),nya_normalize_v1(COALESCE(json_extract(NEW.fields_json,'$.title_original'),'')),nya_natural_v1(NEW.title)) ON CONFLICT(book_id) DO UPDATE SET title=excluded.title,original_title=excluded.original_title,sort_key=excluded.sort_key;
 UPDATE catalog_state SET revision=revision+1;
END;
CREATE TRIGGER document_update AFTER UPDATE ON effective_books BEGIN
 UPDATE search_documents SET title=nya_normalize_v1(NEW.title),original_title=nya_normalize_v1(COALESCE(json_extract(NEW.fields_json,'$.title_original'),'')),sort_key=nya_natural_v1(NEW.title) WHERE book_id=NEW.book_id;
 UPDATE catalog_state SET revision=revision+1;
END;
CREATE TRIGGER tag_search_insert AFTER INSERT ON tags BEGIN INSERT INTO search_tags VALUES(NEW.id,NEW.namespace,nya_normalize_v1(NEW.name)); END;
CREATE TRIGGER catalog_source_insert AFTER INSERT ON book_sources BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER catalog_source_update AFTER UPDATE ON book_sources BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER catalog_tag_insert AFTER INSERT ON effective_book_tags BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER catalog_tag_delete AFTER DELETE ON effective_book_tags BEGIN UPDATE catalog_state SET revision=revision+1; END;
CREATE TRIGGER state_insert AFTER INSERT ON book_user_states BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('user_state',NEW.book_id,NEW.revision,unixepoch()); END;
CREATE TRIGGER state_update AFTER UPDATE ON book_user_states BEGIN UPDATE catalog_state SET revision=revision+1; INSERT INTO events(type,resource_id,revision,created_at) VALUES('user_state',NEW.book_id,NEW.revision,unixepoch()); END;

-- Derived projections; source metadata and manual overrides retain ownership.
CREATE TABLE effective_book_properties (
 book_id TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
 translated INTEGER CHECK(translated IN (0,1)),
 color_mode TEXT CHECK(color_mode IN ('monochrome','color')),
 censorship TEXT CHECK(censorship IN ('uncensored','mosaic','full')),
 extraneous_ads INTEGER CHECK(extraneous_ads IN (0,1)),
 rule_version INTEGER NOT NULL,
 properties_json TEXT NOT NULL
);
CREATE TABLE effective_book_languages (
 book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
 language TEXT NOT NULL,position INTEGER NOT NULL CHECK(position>=0),
 PRIMARY KEY(book_id,language),UNIQUE(book_id,position)
);
CREATE INDEX languages_filter ON effective_book_languages(language,book_id);
CREATE TABLE projection_state (
 name TEXT PRIMARY KEY,rule_version INTEGER NOT NULL,last_book_id TEXT NOT NULL DEFAULT '',
 ready INTEGER NOT NULL DEFAULT 0 CHECK(ready IN (0,1))
);
INSERT INTO projection_state(name,rule_version) VALUES('book_properties',1);
CREATE TABLE dictionary_generations (
 id TEXT PRIMARY KEY,version TEXT NOT NULL,digest TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('building','complete')),
 expected_count INTEGER NOT NULL,checkpoint INTEGER NOT NULL DEFAULT 0,
 created_at INTEGER NOT NULL
);
CREATE TABLE dictionary_entries (
 generation_id TEXT NOT NULL REFERENCES dictionary_generations(id) ON DELETE CASCADE,
 namespace TEXT NOT NULL,name TEXT NOT NULL,translation TEXT,
 normalized_name TEXT NOT NULL,normalized_translation TEXT NOT NULL,
 PRIMARY KEY(generation_id,namespace,name)
);
CREATE INDEX dictionary_names ON dictionary_entries(generation_id,namespace,normalized_name,name);
CREATE TABLE dictionary_state (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),active_generation TEXT REFERENCES dictionary_generations(id),
 previous_generation TEXT REFERENCES dictionary_generations(id),manual_revision INTEGER NOT NULL DEFAULT 1
);
INSERT INTO dictionary_state(singleton) VALUES(1);

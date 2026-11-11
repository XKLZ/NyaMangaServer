-- Independent rebuildable single-parody evidence and durable human decisions.
CREATE TABLE tag_relation_projection (
 singleton INTEGER PRIMARY KEY CHECK(singleton=1),rule_version INTEGER NOT NULL CHECK(rule_version=1),
 last_edition_id TEXT NOT NULL DEFAULT '',ready INTEGER NOT NULL DEFAULT 0 CHECK(ready IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0)
);
INSERT INTO tag_relation_projection(singleton,rule_version) VALUES(1,1);
CREATE TABLE tag_character_evidence (
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 parody_id TEXT NOT NULL REFERENCES tags(id),character_id TEXT NOT NULL REFERENCES tags(id),
 PRIMARY KEY(edition_id,parody_id,character_id)
);
CREATE INDEX tag_character_seed ON tag_character_evidence(character_id,parody_id,edition_id);
CREATE TABLE parody_character_decisions (
 parody_id TEXT NOT NULL REFERENCES tags(id),character_id TEXT NOT NULL REFERENCES tags(id),
 decision TEXT NOT NULL CHECK(decision IN('auto','confirmed','excluded')),
 revision INTEGER NOT NULL CHECK(revision>0),updated_at INTEGER NOT NULL,
 PRIMARY KEY(parody_id,character_id)
);
CREATE INDEX parody_character_reverse ON parody_character_decisions(character_id,parody_id);
CREATE TABLE edition_character_suppressions (
 edition_id TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
 parody_id TEXT NOT NULL REFERENCES tags(id),character_id TEXT NOT NULL REFERENCES tags(id),
 suppressed INTEGER NOT NULL CHECK(suppressed IN(0,1)),revision INTEGER NOT NULL CHECK(revision>0),updated_at INTEGER NOT NULL,
 PRIMARY KEY(edition_id,parody_id,character_id)
);
CREATE TRIGGER tag_evidence_insert AFTER INSERT ON effective_edition_tags BEGIN
DELETE FROM tag_character_evidence WHERE edition_id=NEW.edition_id;
 INSERT INTO tag_character_evidence(edition_id,parody_id,character_id)
 SELECT p.edition_id,p.tag_id,c.tag_id FROM effective_edition_tags p JOIN tags pt ON pt.id=p.tag_id AND pt.namespace='parody' AND nya_normalize_v1(pt.name) NOT IN('original','origin')
 JOIN effective_edition_tags c ON c.edition_id=p.edition_id JOIN tags ct ON ct.id=c.tag_id AND ct.namespace='character'
 WHERE p.edition_id=NEW.edition_id AND (SELECT count(*) FROM effective_edition_tags x JOIN tags xt ON xt.id=x.tag_id WHERE x.edition_id=p.edition_id AND xt.namespace='parody' AND nya_normalize_v1(xt.name) NOT IN('original','origin'))=1;
END;
CREATE TRIGGER tag_evidence_delete AFTER DELETE ON effective_edition_tags BEGIN
DELETE FROM tag_character_evidence WHERE edition_id=OLD.edition_id;
 INSERT INTO tag_character_evidence(edition_id,parody_id,character_id)
 SELECT p.edition_id,p.tag_id,c.tag_id FROM effective_edition_tags p JOIN tags pt ON pt.id=p.tag_id AND pt.namespace='parody' AND nya_normalize_v1(pt.name) NOT IN('original','origin')
 JOIN effective_edition_tags c ON c.edition_id=p.edition_id JOIN tags ct ON ct.id=c.tag_id AND ct.namespace='character'
 WHERE p.edition_id=OLD.edition_id AND (SELECT count(*) FROM effective_edition_tags x JOIN tags xt ON xt.id=x.tag_id WHERE x.edition_id=p.edition_id AND xt.namespace='parody' AND nya_normalize_v1(xt.name) NOT IN('original','origin'))=1;
END;
CREATE TRIGGER tag_evidence_update AFTER UPDATE ON effective_edition_tags BEGIN
DELETE FROM tag_character_evidence WHERE edition_id=OLD.edition_id;
 INSERT INTO tag_character_evidence(edition_id,parody_id,character_id)
 SELECT p.edition_id,p.tag_id,c.tag_id FROM effective_edition_tags p JOIN tags pt ON pt.id=p.tag_id AND pt.namespace='parody' AND nya_normalize_v1(pt.name) NOT IN('original','origin')
 JOIN effective_edition_tags c ON c.edition_id=p.edition_id JOIN tags ct ON ct.id=c.tag_id AND ct.namespace='character'
 WHERE p.edition_id=OLD.edition_id AND (SELECT count(*) FROM effective_edition_tags x JOIN tags xt ON xt.id=x.tag_id WHERE x.edition_id=p.edition_id AND xt.namespace='parody' AND nya_normalize_v1(xt.name) NOT IN('original','origin'))=1;
DELETE FROM tag_character_evidence WHERE edition_id=NEW.edition_id;
 INSERT INTO tag_character_evidence(edition_id,parody_id,character_id)
 SELECT p.edition_id,p.tag_id,c.tag_id FROM effective_edition_tags p JOIN tags pt ON pt.id=p.tag_id AND pt.namespace='parody' AND nya_normalize_v1(pt.name) NOT IN('original','origin')
 JOIN effective_edition_tags c ON c.edition_id=p.edition_id JOIN tags ct ON ct.id=c.tag_id AND ct.namespace='character'
 WHERE p.edition_id=NEW.edition_id AND (SELECT count(*) FROM effective_edition_tags x JOIN tags xt ON xt.id=x.tag_id WHERE x.edition_id=p.edition_id AND xt.namespace='parody' AND nya_normalize_v1(xt.name) NOT IN('original','origin'))=1;
END;
CREATE TRIGGER parody_character_decisions_insert AFTER INSERT ON parody_character_decisions BEGIN UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1; INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1; END;
CREATE TRIGGER parody_character_decisions_update AFTER UPDATE ON parody_character_decisions BEGIN UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1; INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1; END;
CREATE TRIGGER parody_character_decisions_delete AFTER DELETE ON parody_character_decisions BEGIN UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1; INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1; END;
CREATE TRIGGER edition_character_suppressions_insert AFTER INSERT ON edition_character_suppressions BEGIN UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1; INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1; END;
CREATE TRIGGER edition_character_suppressions_update AFTER UPDATE ON edition_character_suppressions BEGIN UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1; INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1; END;
CREATE TRIGGER edition_character_suppressions_delete AFTER DELETE ON edition_character_suppressions BEGIN UPDATE tag_relation_projection SET revision=revision+1 WHERE singleton=1; INSERT INTO events(type,resource_id,revision,created_at) SELECT 'tag_relations','global',revision,unixepoch() FROM tag_relation_projection WHERE singleton=1; END;

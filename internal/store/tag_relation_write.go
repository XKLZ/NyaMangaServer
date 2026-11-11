package store

import (
	"context"
	"database/sql"
	"fmt"
	"nyamangaserver.local/server/internal/model"
)

// PrepareTagRelations never opens source files or remote services. Each batch
// and its durable cursor commit together, independently of the schema ledger.
func (s *Store) PrepareTagRelations(ctx context.Context) error {
	var last string
	var ready, rule int
	if e := scopeRow(ctx, s.Write, "SELECT last_edition_id,ready,rule_version FROM tag_relation_projection WHERE singleton=1").Scan(&last, &ready, &rule); e != nil {
		return e
	}
	if rule != 1 {
		return fmt.Errorf("unsupported tag relation projection rule %d", rule)
	}
	if ready == 1 {
		return nil
	}
	for {
		done, e := s.tagRelationBatch(ctx, last)
		if e != nil {
			return e
		}
		last = done.last
		if done.ready {
			return nil
		}
	}
}

type tagBatchResult struct {
	last  string
	ready bool
}

func (s *Store) tagRelationBatch(ctx context.Context, last string) (tagBatchResult, error) {
	out := tagBatchResult{last: last}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	rows, e := scopeRows(ctx, tx, "SELECT id FROM editions WHERE id>? ORDER BY id LIMIT 500", last)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, id := range ids {
		if e = refreshCharacterEvidence(ctx, tx, id); e != nil {
			return out, e
		}
		out.last = id
	}
	out.ready = len(ids) < 500
	if _, e = tx.ExecContext(ctx, "UPDATE tag_relation_projection SET last_edition_id=?,ready=? WHERE singleton=1", out.last, out.ready); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func refreshCharacterEvidence(ctx context.Context, tx *sql.Tx, id string) error {
	if _, e := tx.ExecContext(ctx, "DELETE FROM tag_character_evidence WHERE edition_id=?", id); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO tag_character_evidence(edition_id,parody_id,character_id)
 SELECT p.edition_id,p.tag_id,c.tag_id FROM effective_edition_tags p JOIN tags pt ON pt.id=p.tag_id AND pt.namespace='parody' AND nya_normalize_v1(pt.name) NOT IN('original','origin')
 JOIN effective_edition_tags c ON c.edition_id=p.edition_id JOIN tags ct ON ct.id=c.tag_id AND ct.namespace='character'
 WHERE p.edition_id=? AND (SELECT count(*) FROM effective_edition_tags x JOIN tags xt ON xt.id=x.tag_id WHERE x.edition_id=p.edition_id AND xt.namespace='parody' AND nya_normalize_v1(xt.name) NOT IN('original','origin'))=1`, id)
	return e
}
func validateCharacterPair(ctx context.Context, q queryer, p, c string) error {
	var n int
	if e := scopeRow(ctx, q, `SELECT count(*) FROM tags p JOIN tags c ON c.id=? AND c.namespace='character' WHERE p.id=? AND p.namespace='parody' AND nya_normalize_v1(p.name) NOT IN('original','origin')`, c, p).Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return model.ErrInput
	}
	return nil
}
func (s *Store) DecideCharacterRelation(ctx context.Context, p, c string, expected int64, decision string) (model.TagRelationDecision, error) {
	out := model.TagRelationDecision{ParodyID: p, CharacterID: c, Decision: decision, Revision: expected + 1, Scope: "global"}
	if expected < 0 || (decision != "auto" && decision != "confirmed" && decision != "excluded") {
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = tagProjectionReady(ctx, tx); e != nil {
		return out, e
	}
	if e = validateCharacterPair(ctx, tx, p, c); e != nil {
		return out, e
	}
	var revision int64
	e = scopeRow(ctx, tx, "SELECT revision FROM parody_character_decisions WHERE parody_id=? AND character_id=?", p, c).Scan(&revision)
	if e != nil && e != sql.ErrNoRows {
		return out, e
	}
	if revision != expected {
		return out, model.ErrConflict
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO parody_character_decisions VALUES(?,?,?,?,unixepoch()) ON CONFLICT(parody_id,character_id) DO UPDATE SET decision=excluded.decision,revision=excluded.revision,updated_at=excluded.updated_at`, p, c, decision, out.Revision)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) SetCharacterSuppression(ctx context.Context, id, p, c string, expected int64, active bool) (model.CharacterSuppression, error) {
	out := model.CharacterSuppression{EditionID: id, ParodyID: p, CharacterID: c, Suppressed: active, Revision: expected + 1, Scope: "edition_automatic"}
	if expected < 0 {
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = tagProjectionReady(ctx, tx); e != nil {
		return out, e
	}
	if e = validateCharacterPair(ctx, tx, p, c); e != nil {
		return out, e
	}
	if e = managementGuard(ctx, tx, id); e != nil {
		return out, e
	}
	var count int
	if e = scopeRow(ctx, tx, `SELECT count(*) FROM editions e JOIN libraries l ON l.id=e.library_id WHERE e.id=? AND e.trashed_at IS NULL AND l.trashed_at IS NULL AND (NOT ? OR (EXISTS(SELECT 1 FROM effective_edition_tags WHERE edition_id=e.id AND tag_id=?) AND EXISTS(SELECT 1 FROM effective_edition_tags WHERE edition_id=e.id AND tag_id=?)))`, id, active, p, c).Scan(&count); e != nil {
		return out, e
	}
	if count != 1 {
		return out, model.ErrNotFound
	}
	var revision int64
	e = scopeRow(ctx, tx, "SELECT revision FROM edition_character_suppressions WHERE edition_id=? AND parody_id=? AND character_id=?", id, p, c).Scan(&revision)
	if e != nil && e != sql.ErrNoRows {
		return out, e
	}
	if expected != revision {
		return out, model.ErrConflict
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO edition_character_suppressions VALUES(?,?,?,?,?,unixepoch()) ON CONFLICT(edition_id,parody_id,character_id) DO UPDATE SET suppressed=excluded.suppressed,revision=excluded.revision,updated_at=excluded.updated_at`, id, p, c, active, out.Revision)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func tagProjectionReady(ctx context.Context, q queryer) error {
	var ready int
	if e := scopeRow(ctx, q, "SELECT ready FROM tag_relation_projection WHERE singleton=1").Scan(&ready); e != nil {
		return e
	}
	if ready != 1 {
		return model.ErrTagProjection
	}
	return nil
}

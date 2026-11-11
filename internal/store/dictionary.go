package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"sort"
	"time"
)

func dictionaryID(v model.TranslationVersion) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(v.ID+"\x00"+v.Digest)))
}

// stageDictionary's sorted immutable input permits checkpoint reuse after pause
// or restart. Every online batch checks the task generation in its transaction.
func (s *Store) stageDictionary(ctx context.Context, j *model.Job, v model.TranslationVersion, dictionary map[model.TagName]string) (string, error) {
	id := dictionaryID(v)
	keys := make([]model.TagName, 0, len(dictionary))
	for k := range dictionary {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Namespace != keys[j].Namespace {
			return keys[i].Namespace < keys[j].Namespace
		}
		return keys[i].Name < keys[j].Name
	})
	if len(keys) == 0 || len(keys) > 500000 {
		return "", model.ErrInput
	}
	for {
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			return "", e
		}
		fail := func(e error) (string, error) { tx.Rollback(); return "", e }
		if e = offlineDictionaryGate(ctx, tx); e != nil {
			return fail(e)
		}
		if j != nil {
			if e = guard(ctx, tx, *j); e != nil {
				return fail(e)
			}
		}
		if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO dictionary_generations VALUES(?,?,?,'building',?,0,?)", id, v.ID, v.Digest, len(keys), time.Now().Unix()); e != nil {
			return fail(e)
		}
		var count, checkpoint int
		var state string
		if e = scopeRow(ctx, tx, "SELECT expected_count,checkpoint,state FROM dictionary_generations WHERE id=?", id).Scan(&count, &checkpoint, &state); e != nil {
			return fail(e)
		}
		if count != len(keys) || checkpoint > count {
			return fail(model.ErrConflict)
		}
		if state == "complete" {
			tx.Rollback()
			return id, nil
		}
		end := checkpoint + 1000
		if end > len(keys) {
			end = len(keys)
		}
		stmt, e := tx.PrepareContext(ctx, "INSERT INTO dictionary_entries VALUES(?,?,?,?,?,?)")
		if e != nil {
			return fail(e)
		}
		for _, key := range keys[checkpoint:end] {
			text := dictionary[key]
			var translation any
			if text != "" {
				translation = text
			}
			if _, e = stmt.ExecContext(ctx, id, key.Namespace, key.Name, translation, textkey.Normalize(key.Name), textkey.Normalize(text)); e != nil {
				stmt.Close()
				return fail(e)
			}
		}
		stmt.Close()
		if _, e = tx.ExecContext(ctx, "UPDATE dictionary_generations SET checkpoint=? WHERE id=?", end, id); e != nil {
			return fail(e)
		}
		if e = tx.Commit(); e != nil {
			return "", e
		}
		if checkpoint == 0 && j != nil {
			if e = s.pruneDictionary(ctx, j, id); e != nil {
				return "", e
			}
		}
		if end == len(keys) {
			return id, nil
		}
	}
}
func materializeIndex(ctx context.Context, tx *sql.Tx, id, version string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO tag_translations(tag_id,locale,origin,dataset,text,revision)
 SELECT t.id,'zh-CN','remote',?,e.translation,1 FROM tags t JOIN dictionary_entries e ON e.namespace=t.namespace AND e.name=t.name
 WHERE e.generation_id=? AND e.translation IS NOT NULL
 ON CONFLICT(tag_id,locale,origin,dataset) DO UPDATE SET text=excluded.text,revision=tag_translations.revision+1 WHERE tag_translations.text IS NOT excluded.text`, version, id)
	return e
}
func activateDictionary(ctx context.Context, tx *sql.Tx, id, version string) error {
	var expected, count, checkpoint int
	if e := scopeRow(ctx, tx, "SELECT expected_count,checkpoint,(SELECT count(*) FROM dictionary_entries WHERE generation_id=?) FROM dictionary_generations WHERE id=?", id, id).Scan(&expected, &checkpoint, &count); e != nil {
		return e
	}
	if expected != count || checkpoint != count {
		return model.ErrConflict
	}
	if _, e := tx.ExecContext(ctx, "UPDATE dictionary_generations SET state='complete' WHERE id=?", id); e != nil {
		return e
	}
	if e := materializeIndex(ctx, tx, id, version); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, "UPDATE dictionary_state SET previous_generation=CASE WHEN active_generation IS NOT ? THEN active_generation ELSE previous_generation END,active_generation=? WHERE singleton=1", id, id); e != nil {
		return e
	}
	return nil
}

// Delete only stale derived generations. Building input is protected for resume.
func (s *Store) pruneDictionary(ctx context.Context, j *model.Job, keep string) error {
	for {
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		if j != nil {
			if e = guard(ctx, tx, *j); e != nil {
				tx.Rollback()
				return e
			}
		}
		protected := `id!=? AND id NOT IN(SELECT active_generation FROM dictionary_state WHERE active_generation IS NOT NULL UNION SELECT previous_generation FROM dictionary_state WHERE previous_generation IS NOT NULL)`
		result, e := tx.ExecContext(ctx, `DELETE FROM dictionary_entries WHERE rowid IN(SELECT e.rowid FROM dictionary_entries e JOIN dictionary_generations g ON g.id=e.generation_id WHERE g.`+protected+` LIMIT 1000)`, keep)
		if e != nil {
			tx.Rollback()
			return e
		}
		n, _ := result.RowsAffected()
		if n < 1000 {
			if _, e = tx.ExecContext(ctx, `DELETE FROM dictionary_generations WHERE `+protected+` AND NOT EXISTS(SELECT 1 FROM dictionary_entries e WHERE e.generation_id=dictionary_generations.id)`, keep); e != nil {
				tx.Rollback()
				return e
			}
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		if n < 1000 {
			return nil
		}
	}
}
func (s *Store) DictionaryAvailable(ctx context.Context) (bool, error) {
	if e := enhancementGate(ctx, s.Read, model.EnhancementDictionary); e != nil {
		if e == model.ErrEnhancementDisabled {
			return false, nil
		}
		return false, e
	}
	return s.DictionaryInstalled(ctx)
}
func (s *Store) DictionaryInstalled(ctx context.Context) (bool, error) {
	var count int
	e := scopeRow(ctx, s.Read, `SELECT count(*) FROM dictionary_state s JOIN dictionary_generations g ON g.id=s.active_generation JOIN translation_state t ON t.singleton=1 JOIN translation_versions v ON v.id=t.active_version WHERE g.state='complete' AND g.version=v.id AND g.digest=v.digest AND g.checkpoint=g.expected_count`).Scan(&count)
	return count == 1, e
}
func (s *Store) MaterializeActiveDictionary(ctx context.Context) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = offlineDictionaryGate(ctx, tx); e != nil {
		return e
	}
	var id, version string
	if e = scopeRow(ctx, tx, "SELECT s.active_generation,g.version FROM dictionary_state s JOIN dictionary_generations g ON g.id=s.active_generation WHERE g.state='complete'").Scan(&id, &version); e != nil {
		return e
	}
	if e = materializeIndex(ctx, tx, id, version); e != nil {
		return e
	}
	return tx.Commit()
}

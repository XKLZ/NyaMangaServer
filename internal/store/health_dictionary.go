package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"sort"
)

func healthRepairDictionary(ctx context.Context, tx *sql.Tx, dictionary map[model.TagName]string) error {
	if len(dictionary) == 0 || len(dictionary) > 500000 {
		return model.ErrInput
	}
	var e error

	var v model.TranslationVersion
	if e = scopeRow(ctx, tx, "SELECT v.id,v.digest FROM translation_state t JOIN translation_versions v ON v.id=t.active_version WHERE t.singleton=1").Scan(&v.ID, &v.Digest); e != nil {
		return e
	}
	id := dictionaryID(v)
	if _, e = tx.ExecContext(ctx, "DELETE FROM dictionary_entries WHERE generation_id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO dictionary_generations(id,version,digest,state,expected_count,checkpoint,created_at) VALUES(?,?,?,'building',?,0,unixepoch()) ON CONFLICT(id) DO UPDATE SET state='building',expected_count=excluded.expected_count,checkpoint=0", id, v.ID, v.Digest, len(dictionary)); e != nil {
		return e
	}
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
	for _, key := range keys {
		var text any
		if dictionary[key] != "" {
			text = dictionary[key]
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO dictionary_entries VALUES(?,?,?,?,?,?)", id, key.Namespace, key.Name, text, textkey.Normalize(key.Name), textkey.Normalize(dictionary[key])); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE dictionary_generations SET state='complete',checkpoint=expected_count WHERE id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE dictionary_state SET active_generation=? WHERE singleton=1", id); e != nil {
		return e
	}
	if e = materialize(ctx, tx, v.ID, dictionary); e != nil {
		return e
	}
	return nil
}

func (s *Store) HealthDictionaryMatches(ctx context.Context, expected map[model.TagName]string) (bool, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var id string
	e = scopeRow(ctx, tx, "SELECT COALESCE(active_generation,'') FROM dictionary_state WHERE singleton=1").Scan(&id)
	if e != nil {
		return false, e
	}
	if id == "" {
		return false, nil
	}
	rows, e := scopeRows(ctx, tx, "SELECT namespace,name,translation,normalized_name,normalized_translation FROM dictionary_entries WHERE generation_id=? ORDER BY namespace,name", id)
	if e != nil {
		return false, e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var key model.TagName
		var text sql.NullString
		var name, translation string
		if e = rows.Scan(&key.Namespace, &key.Name, &text, &name, &translation); e != nil {
			return false, e
		}
		value, ok := expected[key]
		if !ok || text.String != value || (value == "" && text.Valid) || name != textkey.Normalize(key.Name) || translation != textkey.Normalize(value) {
			return false, nil
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return false, e
	}
	rows.Close()
	if e = tx.Commit(); e != nil {
		return false, e
	}
	return count == len(expected), nil
}

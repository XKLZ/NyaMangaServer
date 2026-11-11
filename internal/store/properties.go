package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
)

func inferBookProperties(ctx context.Context, q queryer, id string) (model.BookProperties, error) {
	var fallback string
	if e := scopeRow(ctx, q, "SELECT filename_title FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&fallback); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return model.BookProperties{}, model.ErrNotFound
		}
		return model.BookProperties{}, e
	}
	var raw string
	fields := model.Metadata{}
	e := scopeRow(ctx, q, "SELECT fields_json FROM effective_editions WHERE edition_id=?", id).Scan(&raw)
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &fields); e != nil {
			return model.BookProperties{}, e
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return model.BookProperties{}, e
	}
	if fields.Title == nil {
		fields.Title = &fallback
	}
	var source model.Metadata
	var language *string
	e = scopeRow(ctx, q, "SELECT normalized_json FROM external_metadata m JOIN edition_sources s ON s.metadata_id=m.id WHERE s.edition_id=?", id).Scan(&raw)
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &source); e != nil {
			return model.BookProperties{}, e
		}
		language = source.Language
	} else if !errors.Is(e, sql.ErrNoRows) {
		return model.BookProperties{}, e
	}
	overrides := map[string]model.Override{}
	rows, e := scopeRows(ctx, q, "SELECT field,mode,value_json FROM field_overrides WHERE edition_id=?", id)
	if e != nil {
		return model.BookProperties{}, e
	}
	for rows.Next() {
		var key, mode, value string
		if e = rows.Scan(&key, &mode, &value); e != nil {
			rows.Close()
			return model.BookProperties{}, e
		}
		op := "set"
		if mode == "empty" {
			op = "empty"
		}
		overrides[key] = model.Override{Op: op, Value: json.RawMessage(value)}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return model.BookProperties{}, e
	}
	return model.InferProperties(fields, language, overrides), nil
}
func readProperties(ctx context.Context, q queryer, id string) (model.BookProperties, error) {
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='effective_edition_properties'").Scan(&exists); e != nil {
		return model.BookProperties{}, e
	}
	if exists == 0 {
		return inferBookProperties(ctx, q, id)
	}
	var raw string
	e := scopeRow(ctx, q, "SELECT properties_json FROM effective_edition_properties WHERE edition_id=?", id).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return inferBookProperties(ctx, q, id)
	}
	var out model.BookProperties
	if e == nil {
		e = json.Unmarshal([]byte(raw), &out)
	}
	return out, e
}
func propertySQL(v model.Property) any { var out any; _ = json.Unmarshal(v.Value, &out); return out }
func projectProperties(ctx context.Context, tx *sql.Tx, id string) error {
	var exists int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='effective_edition_properties'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	v, e := inferBookProperties(ctx, tx, id)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	var previous string
	e = scopeRow(ctx, tx, "SELECT properties_json FROM effective_edition_properties WHERE edition_id=?", id).Scan(&previous)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if previous == string(raw) {
		if healthProjecting(ctx) {
			return nil
		}
		return refreshEditionWorkLabels(ctx, tx, id)
	}
	old := model.BookProperties{}
	_ = json.Unmarshal([]byte(previous), &old)
	_, e = tx.ExecContext(ctx, `INSERT INTO effective_edition_properties VALUES(?,?,?,?,?,?,?) ON CONFLICT(edition_id) DO UPDATE SET translated=excluded.translated,color_mode=excluded.color_mode,censorship=excluded.censorship,extraneous_ads=excluded.extraneous_ads,rule_version=excluded.rule_version,properties_json=excluded.properties_json`, id, propertySQL(v.Translated), propertySQL(v.ColorMode), propertySQL(v.Censorship), propertySQL(v.ExtraneousAds), v.RuleVersion, string(raw))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM effective_edition_languages WHERE edition_id=?", id); e != nil {
		return e
	}
	for i, language := range model.Languages(v.Languages) {
		if _, e = tx.ExecContext(ctx, "INSERT INTO effective_edition_languages VALUES(?,?,?)", id, language, i); e != nil {
			return e
		}
	}
	changed := false
	for _, pair := range [][2]model.Property{{v.Languages, old.Languages}, {v.Translated, old.Translated}, {v.ColorMode, old.ColorMode}, {v.Censorship, old.Censorship}, {v.ExtraneousAds, old.ExtraneousAds}} {
		if string(pair[0].Value) != string(pair[1].Value) {
			changed = true
		}
	}
	if changed {
		_, e = tx.ExecContext(ctx, "UPDATE catalog_state SET revision=revision+1")
	}
	if e != nil {
		return e
	}
	if healthProjecting(ctx) {
		return nil
	}
	return refreshEditionWorkLabels(ctx, tx, id)
}

// PrepareProperties commits each batch together with its checkpoint. Call before
// serving requests or starting workers, under the application's instance lock.
func (s *Store) PrepareProperties(ctx context.Context) error {
	var last string
	var ready, version int
	if e := scopeRow(ctx, s.Write, "SELECT last_book_id,ready,rule_version FROM projection_state WHERE name='book_properties'").Scan(&last, &ready, &version); e != nil {
		return e
	}
	if version != model.PropertyRuleVersion {
		return fmt.Errorf("unsupported property projection version %d", version)
	}
	if ready == 1 {
		return nil
	}
	for {
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		rows, e := scopeRows(ctx, tx, "SELECT id FROM "+editionRecordSQL+" editions WHERE id>? ORDER BY id LIMIT 500", last)
		if e != nil {
			tx.Rollback()
			return e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				tx.Rollback()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			tx.Rollback()
			return e
		}
		for _, id := range ids {
			if e = projectProperties(ctx, tx, id); e != nil {
				tx.Rollback()
				return e
			}
			last = id
		}
		done := len(ids) < 500
		if _, e = tx.ExecContext(ctx, "UPDATE projection_state SET last_book_id=?,ready=? WHERE name='book_properties'", last, done); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		if done {
			return nil
		}
	}
}

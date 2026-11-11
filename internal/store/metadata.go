package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"time"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func metadataDetails(ctx context.Context, q queryer, id string) (model.MetadataDetails, error) {
	out := model.MetadataDetails{Tags: []model.Tag{}, Overrides: map[string]model.Override{}, State: "none", SelectionRevision: 1, Fields: model.Metadata{Tags: []model.TagName{}}}
	var maskErr error
	q, maskErr = maskedDictionary(ctx, q)
	if maskErr != nil {
		return out, maskErr
	}
	var content int64
	if e := scopeRow(ctx, q, "SELECT content_revision FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&content); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		return out, e
	}
	e := scopeRow(ctx, q, "SELECT manual_url,selection_revision FROM edition_metadata_state WHERE edition_id=?", id).Scan(&out.ManualURL, &out.SelectionRevision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	var normalized string
	src := model.Source{}
	e = scopeRow(ctx, q, `SELECT m.id,m.provider,m.external_id,s.url,s.match_method,s.matched_content_revision,m.fetched_at,m.normalized_json FROM edition_sources s JOIN external_metadata m ON m.id=s.metadata_id WHERE s.edition_id=?`, id).Scan(&src.ID, &src.Provider, &src.ExternalID, &src.URL, &src.Method, &src.ContentRevision, &src.FetchedAt, &normalized)
	if e == nil {
		if e = json.Unmarshal([]byte(normalized), &src.Fields); e != nil {
			return out, e
		}
		out.Source = &src
		out.State = "matched"
		if content != src.ContentRevision {
			out.State = "needs_review"
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	var effective string
	e = scopeRow(ctx, q, "SELECT fields_json FROM effective_editions WHERE edition_id=?", id).Scan(&effective)
	if e == nil {
		if e = json.Unmarshal([]byte(effective), &out.Fields); e != nil {
			return out, e
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if out.Fields.Tags == nil {
		out.Fields.Tags = []model.TagName{}
	}
	rows, e := scopeRows(ctx, q, "SELECT field,mode,value_json FROM field_overrides WHERE edition_id=? ORDER BY field", id)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var k, mode, v string
		if e = rows.Scan(&k, &mode, &v); e != nil {
			return out, e
		}
		op := "set"
		if mode == "empty" {
			op = "empty"
			v = ""
		}
		out.Overrides[k] = model.Override{Op: op, Value: json.RawMessage(v)}
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	rows.Close()
	tagRows, e := scopeRows(ctx, q, `SELECT t.id,t.namespace,t.name,COALESCE(m.text,r.text,t.name),CASE WHEN m.text IS NOT NULL THEN 'manual' WHEN r.text IS NOT NULL THEN 'remote' ELSE 'original' END,COALESCE(m.revision,0) FROM effective_edition_tags b JOIN tags t ON t.id=b.tag_id LEFT JOIN tag_translations m ON m.tag_id=t.id AND m.locale='zh-CN' AND m.origin='manual' LEFT JOIN translation_state v ON v.singleton=1 LEFT JOIN tag_translations r ON r.tag_id=t.id AND r.locale='zh-CN' AND r.origin='remote' AND r.dataset=v.active_version WHERE b.edition_id=? ORDER BY t.namespace,t.name`, id)
	if e != nil {
		return out, e
	}
	defer tagRows.Close()
	for tagRows.Next() {
		var tag model.Tag
		if e = tagRows.Scan(&tag.ID, &tag.Namespace, &tag.Name, &tag.Translation, &tag.Origin, &tag.Revision); e != nil {
			return out, e
		}
		out.Tags = append(out.Tags, tag)
	}
	if e = tagRows.Err(); e != nil {
		return out, e
	}
	tagRows.Close()
	if e = tagMembership(ctx, q, out.Tags); e != nil {
		return out, e
	}
	props, e := readProperties(ctx, q, id)
	out.Properties = &props
	if e != nil {
		return out, e
	}
	out.Fields.Language = nil
	if v := model.Languages(out.Properties.Languages); len(v) > 0 {
		out.Fields.Language = &v[0]
	}
	if out.Source != nil {
		out.Fields.RemotePageCount = out.Source.Fields.RemotePageCount
		out.Fields.RemoteSizeBytes = out.Source.Fields.RemoteSizeBytes
	}
	return out, nil
}

// EditionWithMetadata reads the public book revision and its projections from one snapshot.
func (s *Store) EditionWithMetadata(ctx context.Context, id string) (*model.Edition, model.MetadataDetails, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, model.MetadataDetails{}, e
	}
	defer tx.Rollback()
	b, e := scanEdition(scopeRow(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id=?", id))
	if e != nil {
		return nil, model.MetadataDetails{}, e
	}
	d, e := metadataDetails(ctx, tx, id)
	if e != nil {
		return nil, d, e
	}
	if s.Version >= 26 {
		page := []model.Edition{*b}
		if e = readingForEditions(ctx, tx, page); e != nil {
			return nil, d, e
		}
		*b = page[0]
	}
	return b, d, tx.Commit()
}
func (s *Store) MetadataDetails(ctx context.Context, id string) (model.MetadataDetails, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.MetadataDetails{}, e
	}
	defer tx.Rollback()
	out, e := metadataDetails(ctx, tx, id)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func reproject(ctx context.Context, tx *sql.Tx, id string) error {
	var fallback string
	if e := scopeRow(ctx, tx, "SELECT filename_title FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&fallback); e != nil {
		return e
	}
	var source model.Metadata
	var raw string
	e := scopeRow(ctx, tx, "SELECT m.normalized_json FROM external_metadata m JOIN edition_sources s ON s.metadata_id=m.id WHERE s.edition_id=?", id).Scan(&raw)
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &source); e != nil {
			return e
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	overrides := map[string]model.Override{}
	rows, e := scopeRows(ctx, tx, "SELECT field,mode,value_json FROM field_overrides WHERE edition_id=?", id)
	if e != nil {
		return e
	}
	for rows.Next() {
		var key, mode, value string
		if e = rows.Scan(&key, &mode, &value); e != nil {
			rows.Close()
			return e
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
		return e
	}
	effective, e := model.ProjectMetadata(source, overrides)
	if e != nil {
		return e
	}
	title := fallback
	if effective.Title != nil {
		title = *effective.Title
	}
	b, e := model.MatchingMetadataJSON(effective)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO effective_editions(edition_id,title,fields_json) VALUES(?,?,?) ON CONFLICT(edition_id) DO UPDATE SET title=excluded.title,fields_json=excluded.fields_json WHERE effective_editions.title IS NOT excluded.title OR effective_editions.fields_json IS NOT excluded.fields_json`, id, title, string(b)); e != nil {
		return e
	}
	existing := map[string]bool{}
	tagRows, e := scopeRows(ctx, tx, "SELECT tag_id FROM effective_edition_tags WHERE edition_id=?", id)
	if e != nil {
		return e
	}
	for tagRows.Next() {
		var key string
		if e = tagRows.Scan(&key); e != nil {
			tagRows.Close()
			return e
		}
		existing[key] = true
	}
	e = tagRows.Err()
	tagRows.Close()
	if e != nil {
		return e
	}
	changed := len(existing) != len(effective.Tags)
	for _, tag := range effective.Tags {
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(tag.Namespace+"\x00"+tag.Name)))
		if !existing[key] {
			changed = true
		}
	}
	if changed {
		if _, e = tx.ExecContext(ctx, "DELETE FROM effective_edition_tags WHERE edition_id=?", id); e != nil {
			return e
		}
		for _, tag := range effective.Tags {
			tagID := fmt.Sprintf("%x", sha256.Sum256([]byte(tag.Namespace+"\x00"+tag.Name)))
			if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO tags VALUES(?,?,?)", tagID, tag.Namespace, tag.Name); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO effective_edition_tags VALUES(?,?)", id, tagID); e != nil {
				return e
			}
		}
	}
	return projectProperties(ctx, tx, id)
}
func (s *Store) ApplyOverrides(ctx context.Context, id string, expected int64, fields map[string]model.Override) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = applyOverridesTx(ctx, tx, id, expected, fields); e != nil {
		return e
	}
	return tx.Commit()
}
func applyOverridesTx(ctx context.Context, tx *sql.Tx, id string, expected int64, fields map[string]model.Override) error {
	if e := model.ValidateOverrides(fields); e != nil {
		return e
	}
	if override, ok := fields["tags"]; ok && override.Op == "set" {
		var tags []model.TagName
		_ = json.Unmarshal(override.Value, &tags)
		for i := range tags {
			tags[i] = model.CanonicalTag(tags[i])
		}
		override.Value, _ = json.Marshal(tags)
		copyFields := make(map[string]model.Override, len(fields))
		for k, v := range fields {
			copyFields[k] = v
		}
		copyFields["tags"] = override
		fields = copyFields
	}

	copyFields := make(map[string]model.Override, len(fields))
	for k, v := range fields {
		copyFields[k] = v
	}
	fields = copyFields
	if old, ok := fields["language"]; ok {
		current, x := readProperties(ctx, tx, id)
		if x != nil {
			return x
		}
		if len(model.Languages(current.Languages)) > 1 {
			return model.ErrMultipleLanguages
		}
		if old.Op == "set" {
			var value string
			_ = json.Unmarshal(old.Value, &value)
			if !model.LanguageValue(value) {
				return model.ErrInput
			}
			old.Value, _ = json.Marshal([]string{model.CanonicalLanguage(value)})
		}
		fields["languages"] = old
		delete(fields, "language")
	}
	if v, ok := fields["languages"]; ok {
		if v.Op == "set" {
			v.Value = model.CanonicalLanguages(v.Value)
			fields["languages"] = v
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM field_overrides WHERE edition_id=? AND field='language'", id); e != nil {
			return e
		}
	}
	r, e := tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE id=? AND revision=?", id, expected)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		var exists int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&exists); e != nil {
			return e
		}
		if exists == 0 {
			return model.ErrNotFound
		}
		return ErrConflict
	}
	for k, v := range fields {
		if v.Op == "inherit" {
			_, e = tx.ExecContext(ctx, "DELETE FROM field_overrides WHERE edition_id=? AND field=?", id, k)
		} else {
			mode := "value"
			value := string(v.Value)
			if v.Op == "empty" {
				mode = "empty"
				value = "null"
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO field_overrides VALUES(?,?,?,?) ON CONFLICT(edition_id,field) DO UPDATE SET mode=excluded.mode,value_json=excluded.value_json`, id, k, mode, value)
		}
		if e != nil {
			return e
		}
	}
	if e = reproject(ctx, tx, id); e != nil {
		return e
	}
	return nil
}
func publishSource(ctx context.Context, tx *sql.Tx, id string, r model.RemoteMetadata) error {
	if e := model.ValidateMetadata(r.Fields); e != nil {
		return e
	}
	if len(r.Raw) > 4<<20 || !json.Valid(r.Raw) {
		return model.ErrInput
	}
	if e := authorizeSourcePublication(ctx, tx, id, r.ExternalID); e != nil {
		return e
	}
	if _, ok := model.Actor(ctx); ok {
		if _, e := tx.ExecContext(ctx, "UPDATE temp.acl_actor SET source_edition=? WHERE singleton=1", id); e != nil {
			return e
		}
		defer tx.ExecContext(context.WithoutCancel(ctx), "UPDATE temp.acl_actor SET source_edition=NULL WHERE singleton=1")
	}
	var previousNormalized, previousMetadata, previousMethod string
	var previousMatched int64
	previousError := tx.QueryRowContext(ctx, "SELECT normalized_json FROM external_metadata WHERE provider='ehentai' AND external_id=?", r.ExternalID).Scan(&previousNormalized)
	if previousError != nil && previousError != sql.ErrNoRows {
		return previousError
	}
	matchingChanged := true
	if previousError == nil {
		var previous model.Metadata
		if e := json.Unmarshal([]byte(previousNormalized), &previous); e != nil {
			return e
		}
		oldJSON, e := model.MatchingMetadataJSON(previous)
		if e != nil {
			return e
		}
		newJSON, e := model.MatchingMetadataJSON(r.Fields)
		if e != nil {
			return e
		}
		matchingChanged = string(oldJSON) != string(newJSON)
	}
	previousError = tx.QueryRowContext(ctx, "SELECT metadata_id,match_method,matched_content_revision FROM edition_sources WHERE edition_id=?", id).Scan(&previousMetadata, &previousMethod, &previousMatched)
	if previousError != nil && previousError != sql.ErrNoRows {
		return previousError
	}
	var targetContent int64
	if e := tx.QueryRowContext(ctx, "SELECT content_revision FROM files WHERE edition_id=?", id).Scan(&targetContent); e != nil {
		return e
	}
	metadataID, e := model.NewID()
	if e != nil {
		return e
	}
	b, e := json.Marshal(r.Fields)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO external_metadata VALUES(?,'ehentai',?,?,?,1,?,1) ON CONFLICT(provider,external_id) DO UPDATE SET normalized_json=excluded.normalized_json,raw_json=excluded.raw_json,parser_version=excluded.parser_version,fetched_at=excluded.fetched_at,revision=external_metadata.revision+1`, metadataID, r.ExternalID, string(b), string(r.Raw), time.Now().Unix())
	if e != nil {
		return e
	}
	if e = tx.QueryRowContext(ctx, "SELECT id FROM external_metadata WHERE provider='ehentai' AND external_id=?", r.ExternalID).Scan(&metadataID); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO edition_sources SELECT id,?,?,?,content_revision FROM `+editionRecordSQL+` editions WHERE id=? ON CONFLICT(edition_id) DO UPDATE SET metadata_id=excluded.metadata_id,url=excluded.url,match_method=excluded.match_method,matched_content_revision=excluded.matched_content_revision`, metadataID, r.URL, r.Method, id)
	if e != nil {
		return e
	}
	rows, e := scopeRows(ctx, tx, "SELECT edition_id FROM edition_sources WHERE metadata_id=?", metadataID)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range ids {
		if !matchingChanged && (v != id || (previousMetadata == metadataID && previousMethod == r.Method && previousMatched == targetContent)) {
			continue
		}
		if e = reproject(ctx, tx, v); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE id=?", v); e != nil {
			return e
		}
	}
	return nil
}

func (s *Store) Tags(ctx context.Context, q, namespace, locale string) ([]model.Tag, error) {
	read, e := maskedDictionary(ctx, s.Read)
	if e != nil {
		return nil, e
	}
	rows, e := scopeRows(ctx, read, `SELECT t.id,t.namespace,t.name,COALESCE(m.text,r.text,t.name),CASE WHEN m.text IS NOT NULL THEN 'manual' WHEN r.text IS NOT NULL THEN 'remote' ELSE 'original' END,COALESCE(m.revision,0) FROM tags t LEFT JOIN tag_translations m ON m.tag_id=t.id AND m.locale=? AND m.origin='manual' AND m.dataset='' LEFT JOIN tag_translations r ON r.tag_id=t.id AND r.locale=? AND r.origin='remote' AND r.dataset=(SELECT active_version FROM translation_state WHERE singleton=1) WHERE (?='' OR t.namespace=?) AND (instr(lower(t.name),lower(?))>0 OR instr(COALESCE(m.text,r.text,''),?)>0) ORDER BY t.namespace,t.name,t.id LIMIT 50`, locale, locale, namespace, namespace, q, q)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Tag{}
	for rows.Next() {
		var v model.Tag
		if e = rows.Scan(&v.ID, &v.Namespace, &v.Name, &v.Translation, &v.Origin, &v.Revision); e != nil {
			return nil, e
		}
		if mask, ok := read.(dictionaryRead); ok && mask.disabled {
			v.DictionaryState = "disabled"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) TranslateTag(ctx context.Context, id, locale string, expected int64, text *string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM tags WHERE id=?", id).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		return ErrNotFound
	}
	var revision int64
	e = scopeRow(ctx, tx, "SELECT revision FROM tag_translations WHERE tag_id=? AND locale=? AND origin='manual' AND dataset=''", id, locale).Scan(&revision)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if revision != expected {
		return ErrConflict
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO tag_translations VALUES(?,?,'manual','',?,?) ON CONFLICT(tag_id,locale,origin,dataset) DO UPDATE SET text=excluded.text,revision=excluded.revision`, id, locale, text, revision+1)
	if e != nil {
		return e
	}
	if s.Version >= 5 {
		if _, e = tx.ExecContext(ctx, "UPDATE dictionary_state SET manual_revision=manual_revision+1 WHERE singleton=1"); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) VALUES('tags',?,?,?)", id, revision+1, time.Now().Unix()); e != nil {
		return e
	}
	return tx.Commit()
}

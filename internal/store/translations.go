package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"time"
)

func (s *Store) TranslationVersion(ctx context.Context) (*model.TranslationVersion, error) {
	var v model.TranslationVersion
	e := scopeRow(ctx, s.Read, `SELECT v.id,v.source,v.digest,v.cache_key,v.activated_at FROM translation_versions v JOIN translation_state s ON s.active_version=v.id WHERE s.singleton=1`).Scan(&v.ID, &v.Source, &v.Digest, &v.CacheKey, &v.ActivatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &v, e
}
func materialize(ctx context.Context, tx *sql.Tx, version string, dictionary map[model.TagName]string) error {
	rows, e := scopeRows(ctx, tx, "SELECT id,namespace,name FROM tags")
	if e != nil {
		return e
	}
	var tags []model.Tag
	for rows.Next() {
		var v model.Tag
		if e = rows.Scan(&v.ID, &v.Namespace, &v.Name); e != nil {
			rows.Close()
			return e
		}
		tags = append(tags, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, tag := range tags {
		if text, ok := dictionary[model.TagName{Namespace: strings.ToLower(tag.Namespace), Name: strings.ToLower(tag.Name)}]; ok && text != "" {
			if _, e = tx.ExecContext(ctx, `INSERT INTO tag_translations VALUES(?,'zh-CN','remote',?,?,1) ON CONFLICT(tag_id,locale,origin,dataset) DO UPDATE SET text=excluded.text,revision=tag_translations.revision+1 WHERE tag_translations.text!=excluded.text`, tag.ID, version, text); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) PublishDictionary(ctx context.Context, j model.Job, item model.Item, v model.TranslationVersion, dictionary map[model.TagName]string) error {
	id, e := s.stageDictionary(ctx, &j, v, dictionary)
	if e != nil {
		return e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var oldDigest string
	e = scopeRow(ctx, tx, "SELECT digest FROM translation_versions WHERE id=?", v.ID).Scan(&oldDigest)
	if e == nil && oldDigest != v.Digest {
		return model.ErrConflict
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO translation_versions VALUES(?,?,?,?,?)", v.ID, v.Source, v.Digest, v.CacheKey, time.Now().Unix()); e != nil {
		return e
	}
	if e = activateDictionary(ctx, tx, id, v.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE translation_state SET active_version=? WHERE singleton=1", v.ID); e != nil {
		return e
	}
	if e = finishItem(ctx, tx, j, item, "completed", "", nil); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) VALUES('translations','zh-CN',?,?)", time.Now().Unix(), time.Now().Unix()); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	_ = s.pruneDictionary(ctx, &j, id)
	return nil
}
func (s *Store) MaterializeTranslations(ctx context.Context, version string, dictionary map[model.TagName]string) error {
	v, e := s.TranslationVersion(ctx)
	if e != nil {
		return e
	}
	if v == nil || v.ID != version {
		return model.ErrConflict
	}
	id, e := s.stageDictionary(ctx, nil, *v, dictionary)
	if e != nil {
		return e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = offlineDictionaryGate(ctx, tx); e != nil {
		return e
	}
	var active, digest string
	e = scopeRow(ctx, tx, "SELECT v.id,v.digest FROM translation_state s JOIN translation_versions v ON v.id=s.active_version WHERE s.singleton=1").Scan(&active, &digest)
	if e != nil {
		return e
	}
	if active != version || digest != v.Digest {
		return model.ErrConflict
	}
	if e = activateDictionary(ctx, tx, id, version); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	// Offline restoration retains every installed generation.
	return nil
}
func (s *Store) ItemCheckpoint(ctx context.Context, j model.Job, item model.Item, input string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE job_items SET input_json=? WHERE job_id=? AND ordinal=? AND state='pending'", input, j.ID, item.Ordinal); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) FailBackgroundItem(ctx context.Context, j model.Job, item model.Item, code string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	state := "failed"
	if code == "" {
		state = "completed"
	}
	if e = finishItem(ctx, tx, j, item, state, code, nil); e != nil {
		return e
	}
	return tx.Commit()
}

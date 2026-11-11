package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
	"time"
)

func (s *Store) CreateSourcePreview(ctx context.Context, id, url string, expected, selection, content int64) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = managementGuard(ctx, tx, id); e != nil {
		return nil, e
	}
	b, e := scanEdition(scopeRow(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id=?", id))
	if e != nil {
		return nil, e
	}
	d, e := metadataDetails(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	if expected != b.Revision || content != b.ContentRevision || selection != d.SelectionRevision {
		return nil, model.ErrConflict
	}
	jid, e := model.NewID()
	if e != nil {
		return nil, e
	}
	pid, e := model.NewID()
	if e != nil {
		return nil, e
	}
	now := time.Now().Unix()
	library, e := jobLibrary(ctx, tx, []string{id})
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO jobs(id,library_id,type,state,stage,generation,total,created_at,updated_at) VALUES(?,?,'metadata_preview','queued','processing',1,1,?,?)", jid, library, now, now); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO job_resources VALUES(?,?,1)", "metadata:book:"+id, jid); e != nil {
		return nil, model.ErrJobActive
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO source_previews(id,edition_id,job_id,expected_revision,selection_revision,content_revision,expires_at) VALUES(?,?,?,?,?,?,?)", pid, id, jid, expected, selection, content, now+1800); e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(model.MetadataInput{URL: url, Force: true, LookupSHA1: b.SHA1, LookupAlgorithm: b.RepresentativeAlgorithm})
	if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,state,edition_id,content_revision,selection_revision,input_json,preview_id) VALUES(?,1,'pending',?,?,?,?,?)", jid, id, content, selection, string(raw), pid); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, jid)
}
func (s *Store) PublishSourcePreview(ctx context.Context, j model.Job, item model.Item, result model.RemoteMetadata) error {
	if len(result.Raw) > 4<<20 || !json.Valid(result.Raw) {
		return model.ErrInput
	}
	if e := model.ValidateMetadata(result.Fields); e != nil {
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
	var current, selection int64
	if e = scopeRow(ctx, tx, "SELECT b.content_revision,COALESCE(m.selection_revision,1) FROM "+editionRecordSQL+" b LEFT JOIN edition_metadata_state m ON m.edition_id=b.id WHERE b.id=?", *item.BookID).Scan(&current, &selection); e != nil {
		return e
	}
	if current != item.ContentRevision || selection != item.SelectionRevision {
		return model.ErrConflict
	}
	if s.Version >= 6 {
		ok, e := lookupMatches(ctx, tx, item)
		if e != nil {
			return e
		}
		if !ok {
			return model.ErrConflict
		}
	}
	raw, _ := json.Marshal(result)
	if _, e = tx.ExecContext(ctx, "UPDATE source_previews SET result_json=?,state='ready' WHERE id=? AND state='pending'", string(raw), item.PreviewID); e != nil {
		return e
	}
	if e = finishItem(ctx, tx, j, item, "completed", "", nil); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SourcePreview(ctx context.Context, book, id string) (model.SourcePreview, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	p := model.SourcePreview{Fields: model.Metadata{Tags: []model.TagName{}}}
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	var raw *string
	e = scopeRow(ctx, tx, "SELECT id,edition_id,job_id,state,expires_at,result_json,expected_revision,selection_revision,content_revision FROM source_previews WHERE id=? AND edition_id=?", id, book).Scan(&p.ID, &p.BookID, &p.JobID, &p.State, &p.Expires, &raw, &p.ExpectedRevision, &p.SelectionRevision, &p.ContentRevision)
	if e == sql.ErrNoRows {
		return p, model.ErrNotFound
	}
	if e != nil {
		return p, e
	}
	p.Current, e = metadataDetails(ctx, tx, book)
	if e != nil {
		return p, e
	}
	if p.Expires < time.Now().Unix() {
		p.State = "expired"
	}
	if raw != nil {
		var r model.RemoteMetadata
		if e = json.Unmarshal([]byte(*raw), &r); e != nil {
			return p, e
		}
		p.Fields = r.Fields
		p.Source = &model.Source{Provider: "ehentai", ExternalID: r.ExternalID, URL: r.URL, Method: "manual", Fields: r.Fields}
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM edition_sources bs JOIN external_metadata m ON m.id=bs.metadata_id WHERE m.provider='ehentai' AND m.external_id=?", r.ExternalID).Scan(&p.SharedBookCount); e != nil {
			return p, e
		}
	}
	return p, tx.Commit()
}
func (s *Store) ConfirmSourcePreview(ctx context.Context, book, id string, expected, selection, content int64) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = managementGuard(ctx, tx, book); e != nil {
		return e
	}
	var raw *string
	var state string
	var expires, original, originalSelection, originalContent int64
	e = scopeRow(ctx, tx, "SELECT result_json,state,expires_at,expected_revision,selection_revision,content_revision FROM source_previews WHERE id=? AND edition_id=?", id, book).Scan(&raw, &state, &expires, &original, &originalSelection, &originalContent)
	if e == sql.ErrNoRows {
		return model.ErrNotFound
	}
	if e != nil {
		return e
	}
	if expires < time.Now().Unix() {
		return model.ErrPreviewExpired
	}
	if state != "ready" || raw == nil {
		return model.ErrConflict
	}
	var actual, actualContent int64
	if e = scopeRow(ctx, tx, "SELECT revision,content_revision FROM "+editionRecordSQL+" editions WHERE id=?", book).Scan(&actual, &actualContent); e != nil {
		return e
	}
	d, e := metadataDetails(ctx, tx, book)
	if e != nil {
		return e
	}
	if original != expected || originalSelection != selection || originalContent != content || actual != expected || actualContent != content || d.SelectionRevision != selection {
		return model.ErrConflict
	}
	if s.Version >= 6 {
		var item model.Item
		item.BookID = &book
		if e = scopeRow(ctx, tx, "SELECT i.input_json FROM job_items i JOIN source_previews p ON p.job_id=i.job_id WHERE p.id=?", id).Scan(&item.Input); e != nil {
			return e
		}
		ok, e := lookupMatches(ctx, tx, item)
		if e != nil {
			return e
		}
		if !ok {
			return model.ErrConflict
		}
	}
	var result model.RemoteMetadata
	if e = json.Unmarshal([]byte(*raw), &result); e != nil {
		return e
	}
	result.Method = "manual"
	if e = publishSource(ctx, tx, book, result); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO edition_metadata_state(edition_id,manual_url,selection_revision) VALUES(?,?,?) ON CONFLICT(edition_id) DO UPDATE SET manual_url=excluded.manual_url,selection_revision=excluded.selection_revision`, book, result.URL, selection+1); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE source_previews SET state='confirmed' WHERE id=?", id); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) RemoveSource(ctx context.Context, id string, expected, selection int64, hintOnly bool) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	d, e := metadataDetails(ctx, tx, id)
	if e != nil {
		return e
	}
	if d.SelectionRevision != selection {
		return model.ErrConflict
	}
	if !hintOnly {
		var actual int64
		if e = scopeRow(ctx, tx, "SELECT revision FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&actual); e != nil {
			return e
		}
		if expected != actual {
			return model.ErrConflict
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM edition_sources WHERE edition_id=?", id); e != nil {
			return e
		}
		if e = reproject(ctx, tx, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE id=?", id); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO edition_metadata_state(edition_id,manual_url,selection_revision) VALUES(?,'',?) ON CONFLICT(edition_id) DO UPDATE SET manual_url='',selection_revision=excluded.selection_revision`, id, selection+1); e != nil {
		return e
	}
	return tx.Commit()
}

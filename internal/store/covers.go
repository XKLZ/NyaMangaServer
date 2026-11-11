package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	"time"
)

func coverSelection(ctx context.Context, q queryer, id string) (model.CoverSelection, error) {
	v := model.CoverSelection{Mode: "auto", State: "ready"}
	var content int64
	if e := scopeRow(ctx, q, "SELECT content_revision FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&content); e != nil {
		if e == sql.ErrNoRows {
			return v, model.ErrNotFound
		}
		return v, e
	}
	v.ContentRevision = content
	parsing, pe := currentParsing(ctx, q, id)
	if pe != nil {
		return v, pe
	}
	v.ParsingRevision = parsing
	e := scopeRow(ctx, q, "SELECT mode,page_index,asset_id,revision,content_revision,entry_index,entry_name,directory_version FROM cover_selections WHERE edition_id=?", id).Scan(&v.Mode, &v.PageIndex, &v.AssetID, &v.Revision, &v.ContentRevision, &v.EntryIndex, &v.EntryName, &v.DirectoryVersion)
	if e == sql.ErrNoRows {
		return v, nil
	}
	if e == nil && hasParsing(ctx, q) {
		if e = scopeRow(ctx, q, "SELECT parsing_revision FROM cover_selections WHERE edition_id=?", id).Scan(&v.ParsingRevision); e != nil {
			return v, e
		}
	}
	if hasParsing(ctx, q) && v.ParsingRevision == parsing {
		if e = scopeRow(ctx, q, "SELECT directory_digest!='' FROM files WHERE edition_id=?", id).Scan(&v.OrderVerified); e != nil {
			return v, e
		}
	}
	if v.Mode == "page" && v.ParsingRevision != parsing {
		v.State = "parsing_changed"
	}
	if v.Mode != "page" {
		v.ContentRevision = content
	}
	if v.Mode == "page" && v.ContentRevision != content {
		v.State = "content_changed"
	}
	return v, e
}
func (s *Store) CoverSelection(ctx context.Context, id string) (model.CoverSelection, error) {
	return coverSelection(ctx, s.Read, id)
}
func (s *Store) SetCover(ctx context.Context, id string, expected int64, v model.CoverSelection, a *model.Asset) (model.CoverSelection, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	var busy int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM maintenance_blocks mb JOIN jobs j ON j.id=mb.job_id JOIN "+editionRecordSQL+" b ON b.library_id=mb.library_id WHERE b.id=? AND j.state IN('queued','running')", id).Scan(&busy); e != nil {
		return v, e
	}
	if busy > 0 {
		return v, model.ErrBusy
	}
	old, e := coverSelection(ctx, tx, id)
	if e != nil {
		return old, e
	}
	if old.Revision != expected {
		return old, model.ErrConflict
	}
	if v.Mode == "page" {
		parsing, err := currentParsing(ctx, tx, id)
		if err != nil {
			return v, err
		}
		if v.ParsingRevision == 0 {
			v.ParsingRevision = 1
		}
		if v.ParsingRevision != parsing {
			return v, model.ErrConflict
		}
		var count int
		if v.PageIndex == nil || v.ContentRevision < 1 {
			return v, model.ErrInput
		}
		if v.EntryIndex == nil || v.EntryName == nil || v.DirectoryVersion == "" {
			return v, model.ErrInput
		}
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+editionRecordSQL+" editions WHERE id=? AND content_revision=? AND page_count>=?", id, v.ContentRevision, *v.PageIndex).Scan(&count); e != nil {
			return v, e
		}
		if count != 1 {
			return v, model.ErrConflict
		}
	} else {
		v.ContentRevision = old.ContentRevision
	}
	if a != nil {
		if _, e = tx.ExecContext(ctx, "INSERT INTO assets VALUES(?,?,?,?,?)", a.ID, a.Key, a.Digest, a.Size, time.Now().Unix()); e != nil {
			return v, e
		}
		v.AssetID = &a.ID
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO cover_selections(edition_id,mode,page_index,content_revision,asset_id,revision,entry_index,entry_name,directory_version) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(edition_id) DO UPDATE SET mode=excluded.mode,page_index=excluded.page_index,content_revision=excluded.content_revision,asset_id=excluded.asset_id,revision=excluded.revision,entry_index=excluded.entry_index,entry_name=excluded.entry_name,directory_version=excluded.directory_version`, id, v.Mode, v.PageIndex, v.ContentRevision, v.AssetID, expected+1, v.EntryIndex, v.EntryName, v.DirectoryVersion); e != nil {
		return v, e
	}
	if hasParsing(ctx, tx) {
		if _, e = tx.ExecContext(ctx, "UPDATE cover_selections SET parsing_revision=? WHERE edition_id=?", max(1, v.ParsingRevision), id); e != nil {
			return v, e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) VALUES('cover',?,?,unixepoch())", id, expected+1); e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	return s.CoverSelection(ctx, id)
}
func (s *Store) Asset(ctx context.Context, id string) (model.Asset, error) {
	var v model.Asset
	e := scopeRow(ctx, s.Read, "SELECT id,storage_key,digest,size FROM assets WHERE id=?", id).Scan(&v.ID, &v.Key, &v.Digest, &v.Size)
	if e == sql.ErrNoRows {
		e = model.ErrNotFound
	}
	return v, e
}
func (s *Store) CreateCoverJob(ctx context.Context, id string, expected int64) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	v, e := coverSelection(ctx, tx, id)
	if e != nil {
		return nil, e
	}
	if v.Revision != expected {
		return nil, model.ErrConflict
	}
	if e = managementGuard(ctx, tx, id); e != nil {
		return nil, e
	}
	jid, e := model.NewID()
	if e != nil {
		return nil, e
	}
	library, e := jobLibrary(ctx, tx, []string{id})
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO jobs(id,library_id,type,state,stage,generation,total,created_at,updated_at) VALUES(?,?,'cover_rebuild','queued','processing',1,1,unixepoch(),unixepoch())", jid, library); e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,state,edition_id,content_revision,selection_revision) VALUES(?,1,'pending',?,?,?)", jid, id, v.ContentRevision, v.Revision); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, jid)
}

package store

import (
	"context"
	"database/sql"
	"errors"

	"nyamangaserver.local/server/internal/model"
)

const manifestColumns = "m.id,m.account_id,m.edition_id,m.cache_epoch,m.content_revision,m.parsing_revision,m.structure_digest,m.page_count,m.directory_version,m.representation_version"

func scanManifest(row scanner) (model.OfflineManifest, error) {
	var m model.OfflineManifest
	e := row.Scan(&m.ID, &m.AccountID, &m.EditionID, &m.CacheEpoch, &m.ContentRevision, &m.ParsingRevision, &m.StructureDigest, &m.PageCount, &m.DirectoryVersion, &m.RepresentationVersion)
	if errors.Is(e, sql.ErrNoRows) {
		e = model.ErrNotFound
	}
	return m, e
}

func (s *Store) CreateOfflineManifest(ctx context.Context, m model.OfflineManifest) (model.OfflineManifest, error) {
	account, e := readingAccount(ctx)
	if e != nil {
		return m, e
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return m, e
	}
	defer tx.Rollback()
	current, e := clientIdentity(ctx, tx)
	if e != nil {
		return m, e
	}
	if current.CacheEpoch != m.CacheEpoch || current.InstanceID != m.InstanceID {
		return m, model.ErrClientEpoch
	}
	var content, parsing int64
	e = scopeRow(ctx, tx, `SELECT f.content_revision,f.parsing_revision FROM editions ed JOIN files f ON f.edition_id=ed.id
		JOIN libraries l ON l.id=ed.library_id WHERE ed.id=? AND ed.trashed_at IS NULL AND l.trashed_at IS NULL AND l.disabled=0 AND f.file_state='available'`, m.EditionID).Scan(&content, &parsing)
	if errors.Is(e, sql.ErrNoRows) {
		return m, model.ErrNotFound
	}
	if e != nil {
		return m, e
	}
	if content != m.ContentRevision {
		return m, model.ErrConflict
	}
	if parsing != m.ParsingRevision {
		return m, model.ErrParsingChanged
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO client_offline_manifests(id,account_id,edition_id,cache_epoch,content_revision,parsing_revision,
		structure_digest,page_count,directory_version,representation_version) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(account_id,edition_id,cache_epoch,structure_digest) DO NOTHING`, m.ID, account, m.EditionID, m.CacheEpoch, m.ContentRevision,
		m.ParsingRevision, m.StructureDigest, m.PageCount, m.DirectoryVersion, m.RepresentationVersion)
	if e != nil {
		return m, e
	}
	out, e := scanManifest(tx.QueryRowContext(ctx, "SELECT "+manifestColumns+" FROM client_offline_manifests m WHERE m.account_id=? AND m.edition_id=? AND m.cache_epoch=? AND m.structure_digest=?",
		account, m.EditionID, m.CacheEpoch, m.StructureDigest))
	if e != nil {
		return out, e
	}
	out.InstanceID = current.InstanceID
	return out, tx.Commit()
}

func (s *Store) OfflineManifest(ctx context.Context, id string) (model.OfflineManifest, error) {
	account, e := readingAccount(ctx)
	if e != nil {
		return model.OfflineManifest{}, e
	}
	return scanManifest(scopeRow(ctx, s.Read, `SELECT `+manifestColumns+` FROM client_offline_manifests m
		JOIN editions ed ON ed.id=m.edition_id JOIN libraries l ON l.id=ed.library_id WHERE m.id=? AND m.account_id=?
		AND ed.trashed_at IS NULL AND l.trashed_at IS NULL`, id, account))
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

// saveEditionIndex commits the stable identity and its physical file together.
// Existing organization and all editorial state remain untouched by scans.
func saveEditionIndex(ctx context.Context, tx *sql.Tx, b model.Edition) error {
	var id string
	e := scopeRow(ctx, tx, "SELECT edition_id FROM files WHERE library_id=? AND relative_path=?", b.LibraryID, b.Path).Scan(&id)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil && id != b.ID {
		return model.ErrConflict
	}
	var exists int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM editions WHERE id=?", b.ID).Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		work := b.WorkID
		if work == "" {
			work = "i:" + b.ID
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO works(id,library_id,preferred_edition_id,revision) VALUES(?,?,?,1)", work, b.LibraryID, b.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO editions(id,library_id,work_id,revision,organization_revision,trashed_at) VALUES(?,?,?,1,1,?)", b.ID, b.LibraryID, work, b.TrashedAt); e != nil {
			return e
		}
	} else {
		if _, e = tx.ExecContext(ctx, "UPDATE editions SET revision=revision+1 WHERE id=?", b.ID); e != nil {
			return e
		}
	}
	algorithm := b.RepresentativeAlgorithm
	if algorithm == "" {
		algorithm = "content-page8-v1"
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,representative_algorithm,legacy_cover_sha1,page_count,file_state,error_code,indexed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(edition_id) DO UPDATE SET size=excluded.size,mtime_ns=excluded.mtime_ns,relative_path=excluded.relative_path,filename_title=excluded.filename_title,legacy_cover_sha1=CASE WHEN files.content_revision=excluded.content_revision THEN files.legacy_cover_sha1 ELSE NULL END,content_revision=excluded.content_revision,representative_sha1=excluded.representative_sha1,representative_algorithm=excluded.representative_algorithm,page_count=excluded.page_count,file_state=excluded.file_state,error_code=excluded.error_code,indexed_at=excluded.indexed_at`, b.ID, b.LibraryID, b.Path, b.FilenameTitle, b.Size, b.Mtime, b.ContentRevision, b.SHA1, algorithm, b.LegacyCoverSHA1, b.PageCount, b.State, b.Error, b.IndexedAt)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO search_documents VALUES(?,nya_normalize_v1(?),'',nya_natural_v1(?))`, b.ID, b.FilenameTitle, b.FilenameTitle)
	return e
}

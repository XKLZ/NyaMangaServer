package testfixture

import (
	"context"
	"database/sql"
)

// SeedEdition creates one anonymous fixture through the same relational constraints.
// It is used only by tests and never by the server dependency graph.
func SeedEdition(ctx context.Context, db interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}, id, library, path, title string) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "INSERT INTO works(id,library_id,preferred_edition_id,revision) VALUES(?,?,?,1)", "i:"+id, library, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO editions(id,library_id,work_id,revision,organization_revision) VALUES(?,?,?,1,1)", id, library, "i:"+id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,representative_algorithm,page_count,file_state,indexed_at) VALUES(?,?,?,?,1,1,1,'0123456789012345678901234567890123456789','content-page8-v1',2,'available',1)`, id, library, path, title); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO search_documents VALUES(?,nya_normalize_v1(?),'',nya_natural_v1(?))", id, title, title); e != nil {
		return e
	}
	return tx.Commit()
}

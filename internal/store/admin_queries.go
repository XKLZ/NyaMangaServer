package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
)

func (s *Store) JobPage(ctx context.Context, kind, state, library string, page, size int) ([]model.Job, int, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	where := ` WHERE (?='' OR type=?) AND (?='' OR state=?) AND (?='' OR library_id=? OR EXISTS(SELECT 1 FROM job_items i JOIN editions b ON b.id=i.edition_id WHERE i.job_id=jobs.id AND b.library_id=?))`
	args := []any{kind, kind, state, state, library, library, library}
	var total int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs"+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+s.jobColumns()+" FROM jobs"+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return nil, 0, e
	}
	out := []model.Job{}
	for rows.Next() {
		v, x := scanJob(rows)
		if x != nil {
			rows.Close()
			return nil, 0, x
		}
		out = append(out, *v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	return out, total, tx.Commit()
}

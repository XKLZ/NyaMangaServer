package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
)

func (s *Store) Edition(ctx context.Context, id string) (*Edition, error) {
	b, e := scanEdition(scopeRow(ctx, s.Read, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id=?", id))
	if e != nil {
		return nil, e
	}
	if s.Version >= 26 {
		page := []model.Edition{*b}
		if e = readingForEditions(ctx, s.Read, page); e != nil {
			return nil, e
		}
		*b = page[0]
	}
	return b, nil
}
func (s *Store) EditionAt(ctx context.Context, library, path string) (*Edition, error) {
	return scanEdition(scopeRow(ctx, s.Read, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE library_id=? AND relative_path=?", library, path))
}
func (s *Store) Editions(ctx context.Context, library, state string, page, size int) ([]Edition, int, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, 0, e
	}
	defer tx.Rollback()
	where := " WHERE trashed_at IS NULL AND EXISTS(SELECT 1 FROM libraries l WHERE l.id=editions.library_id AND l.disabled=0 AND l.trashed_at IS NULL) AND (?='' OR library_id=?) AND (?='' OR file_state=?)"
	args := []any{library, library, state, state}
	var total int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+editionRecordSQL+" editions"+where, args...).Scan(&total); e != nil {
		return nil, 0, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions"+where+" ORDER BY relative_path COLLATE BINARY,id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return nil, 0, e
	}
	out := []Edition{}
	for rows.Next() {
		v, e := scanEdition(rows)
		if e != nil {
			rows.Close()
			return nil, 0, e
		}
		out = append(out, *v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, 0, e
	}
	if s.Version >= 26 {
		if e = readingForEditions(ctx, tx, out); e != nil {
			return nil, 0, e
		}
	}
	return out, total, tx.Commit()
}

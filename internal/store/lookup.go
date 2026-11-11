package store

import (
	"context"
	"encoding/json"
	"nyamangaserver.local/server/internal/model"
)

func lookupMatches(ctx context.Context, q queryer, item model.Item) (bool, error) {
	var in model.MetadataInput
	if json.Unmarshal([]byte(item.Input), &in) != nil {
		return false, model.ErrInput
	}
	var hash *string
	var algorithm string
	if e := scopeRow(ctx, q, "SELECT representative_sha1,representative_algorithm FROM "+editionRecordSQL+" editions WHERE id=?", *item.BookID).Scan(&hash, &algorithm); e != nil {
		return false, e
	}
	if in.LookupAlgorithm == "" {
		return algorithm == "first-page-v1", nil
	}
	equal := hash == nil && in.LookupSHA1 == nil || hash != nil && in.LookupSHA1 != nil && *hash == *in.LookupSHA1
	return equal && algorithm == in.LookupAlgorithm, nil
}

func (s *Store) RehashCandidates(ctx context.Context, after string, limit int) ([]model.Edition, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id>? AND representative_algorithm!='content-page8-v1' ORDER BY id LIMIT ?", after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Edition{}
	for rows.Next() {
		b, e := scanEdition(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// UpdateLookup does not claim a file-content or human-edit change.
func (s *Store) UpdateLookup(ctx context.Context, b model.Edition, hash string, pages int) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, `UPDATE files SET representative_sha1=?,representative_algorithm='content-page8-v1',page_count=? WHERE edition_id=? AND content_revision=? AND (SELECT revision FROM editions WHERE id=files.edition_id)=? AND size=? AND mtime_ns=? AND representative_algorithm=? AND representative_sha1 IS ?`, hash, pages, b.ID, b.ContentRevision, b.Revision, b.Size, b.Mtime, b.RepresentativeAlgorithm, b.SHA1)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return model.ErrConflict
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) VALUES('book',?,?,unixepoch())", b.ID, b.Revision); e != nil {
		return e
	}
	return tx.Commit()
}

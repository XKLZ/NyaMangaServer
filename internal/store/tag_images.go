package store

import (
	"context"
	"database/sql"
	"math"
	"nyamangaserver.local/server/internal/model"
)

func tagImageIdentity(ctx context.Context, q queryer, id string) error {
	var ns string
	e := scopeRow(ctx, q, "SELECT namespace FROM tags WHERE id=?", id).Scan(&ns)
	if e == sql.ErrNoRows {
		return model.ErrNotFound
	}
	if e != nil {
		return e
	}
	if ns != "parody" && ns != "character" {
		return model.ErrInput
	}
	return nil
}

// One batch query hydrates only the requested card identities in the caller's
// read transaction, including empty role generations and orphan-safe references.
func tagImageBatch(ctx context.Context, q queryer, ids []string) (map[string]model.TagImages, error) {
	out := map[string]model.TagImages{}
	if len(ids) == 0 {
		return out, nil
	}
	for _, id := range ids {
		out[id] = model.EmptyTagImages(id)
	}
	rows, e := scopeRows(ctx, q, `SELECT i.tag_id,i.role,i.revision,i.asset_id,i.width,i.height,i.format,a.storage_key,a.digest,a.size FROM tag_images i LEFT JOIN assets a ON a.id=i.asset_id WHERE i.tag_id IN (`+placeholders(len(ids))+`)`, stringArgs(ids)...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		v := model.TagImage{ProcessingVersion: 1}
		var key, digest sql.NullString
		var size sql.NullInt64
		if e = rows.Scan(&id, &v.Role, &v.Revision, &v.AssetID, &v.Width, &v.Height, &v.Format, &key, &digest, &size); e != nil {
			return out, e
		}
		if v.AssetID != nil {
			v.Asset = &model.Asset{ID: *v.AssetID, Key: key.String, Digest: digest.String, Size: size.Int64}
		}
		pair := out[id]
		if v.Role == "foreground" {
			pair.Foreground = v
		} else {
			pair.Background = v
		}
		out[id] = pair
	}
	return out, rows.Err()
}
func (s *Store) TagImages(ctx context.Context, id string) (model.TagImages, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.TagImages{}, e
	}
	defer tx.Rollback()
	if e = tagImageIdentity(ctx, tx, id); e != nil {
		return model.TagImages{}, e
	}
	m, e := tagImageBatch(ctx, tx, []string{id})
	if e != nil {
		return model.TagImages{}, e
	}
	return m[id], tx.Commit()
}
func (s *Store) SetTagImage(ctx context.Context, id, role string, expected int64, v model.TagImage) (model.TagImage, error) {
	if !model.TagImageRole(role) || expected < 0 || expected == math.MaxInt64 {
		return v, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if e = tagImageIdentity(ctx, tx, id); e != nil {
		return v, e
	}
	var rev int64
	e = scopeRow(ctx, tx, "SELECT revision FROM tag_images WHERE tag_id=? AND role=?", id, role).Scan(&rev)
	if e != nil && e != sql.ErrNoRows {
		return v, e
	}
	if rev != expected {
		return v, model.ErrConflict
	}
	if v.Asset != nil {
		a := v.Asset
		if _, e = tx.ExecContext(ctx, "INSERT INTO assets VALUES(?,?,?,?,unixepoch())", a.ID, a.Key, a.Digest, a.Size); e != nil {
			return v, e
		}
		v.AssetID = &a.ID
	} else {
		v.AssetID = nil
		v.Width = 0
		v.Height = 0
		v.Format = ""
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO tag_images(tag_id,role,asset_id,width,height,format,revision) VALUES(?,?,?,?,?,?,?) ON CONFLICT(tag_id,role) DO UPDATE SET asset_id=excluded.asset_id,width=excluded.width,height=excluded.height,format=excluded.format,revision=excluded.revision`, id, role, v.AssetID, v.Width, v.Height, v.Format, expected+1); e != nil {
		return v, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at) VALUES('tag_image',?,?,unixepoch())", id+":"+role, expected+1); e != nil {
		return v, e
	}
	if e = tx.Commit(); e != nil {
		return v, e
	}
	v.Role = role
	v.Revision = expected + 1
	v.ProcessingVersion = 1
	return v, nil
}

func enrichTagImages(ctx context.Context, q queryer, tags ...*model.CatalogTag) error {
	ids := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		if (t.Namespace == "parody" || t.Namespace == "character") && !seen[t.ID] {
			ids = append(ids, t.ID)
			seen[t.ID] = true
		}
	}
	images, e := tagImageBatch(ctx, q, ids)
	if e != nil {
		return e
	}
	for _, t := range tags {
		if v, ok := images[t.ID]; ok {
			copy := v
			t.Images = &copy
		}
	}
	return nil
}

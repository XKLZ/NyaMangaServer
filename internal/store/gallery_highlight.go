package store

import (
	"context"
	"database/sql"
	"math"
	"nyamangaserver.local/server/internal/model"
	"strings"
	"time"
)

func galleryEnabled(ctx context.Context, q queryer) error {
	var enabled bool
	if e := scopeRow(ctx, q, "SELECT enabled FROM gallery_highlight_settings WHERE singleton=1").Scan(&enabled); e != nil {
		return e
	}
	if !enabled {
		return model.ErrGalleryDisabled
	}
	return nil
}
func galleryActor(ctx context.Context, q queryer, actor model.Session, now int64) (int64, error) {
	var revision int64
	e := scopeRow(ctx, q, `SELECT a.auth_revision FROM accounts a JOIN sessions s ON s.account_id=a.id WHERE a.id=? AND s.token_hash=? AND s.expires_at>?`, actor.AccountID, actor.Hash, now).Scan(&revision)
	if e == sql.ErrNoRows {
		return 0, model.ErrGalleryAuth
	}
	return revision, e
}
func (s *Store) CreateGalleryConnection(ctx context.Context, actor model.Session, id, name string, hash []byte, now int64) (model.GalleryConnection, error) {
	out := model.GalleryConnection{}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	revision, e := galleryActor(ctx, tx, actor, now)
	if e != nil {
		return out, e
	}
	if e = galleryEnabled(ctx, tx); e != nil {
		return out, e
	}
	var n int
	if e = scopeRow(ctx, tx, `SELECT count(*) FROM gallery_highlight_connections WHERE account_id=? AND auth_revision=? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`, actor.AccountID, revision, now).Scan(&n); e != nil {
		return out, e
	}
	if n >= 20 {
		return out, model.ErrGalleryCapacity
	}
	out = model.GalleryConnection{ID: id, Name: name, CreatedAt: now, ExpiresAt: nil, Revision: 1, State: "active"}
	_, e = tx.ExecContext(ctx, `INSERT INTO gallery_highlight_connections(id,account_id,name,token_hash,auth_revision,created_at,expires_at,revision) VALUES(?,?,?,?,?,?,?,1)`, id, actor.AccountID, name, hash, revision, now, out.ExpiresAt)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

const galleryConnectionColumns = `id,name,created_at,expires_at,revoked_at,revision,CASE WHEN revoked_at IS NOT NULL THEN 'revoked' WHEN expires_at<=? THEN 'expired' WHEN auth_revision<>? THEN 'invalidated' ELSE 'active' END`

func scanGalleryConnection(row scanner) (model.GalleryConnection, error) {
	var v model.GalleryConnection
	e := row.Scan(&v.ID, &v.Name, &v.CreatedAt, &v.ExpiresAt, &v.RevokedAt, &v.Revision, &v.State)
	return v, e
}
func (s *Store) GalleryConnections(ctx context.Context, actor model.Session, page, size int, now int64) (model.GalleryConnections, error) {
	out := model.GalleryConnections{Items: []model.GalleryConnection{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	revision, e := galleryActor(ctx, tx, actor, now)
	if e != nil {
		return out, e
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM gallery_highlight_connections WHERE account_id=?", actor.AccountID).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+galleryConnectionColumns+" FROM gallery_highlight_connections WHERE account_id=? ORDER BY created_at DESC,id LIMIT ? OFFSET ?", now, revision, actor.AccountID, size, (page-1)*size)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, e := scanGalleryConnection(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) RevokeGalleryConnection(ctx context.Context, actor model.Session, id string, expected, now int64) (model.GalleryConnection, error) {
	s.galleryPublish.Lock()
	defer s.galleryPublish.Unlock()
	out := model.GalleryConnection{}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	revision, e := galleryActor(ctx, tx, actor, now)
	if e != nil {
		return out, e
	}
	out, e = scanGalleryConnection(scopeRow(ctx, tx, "SELECT "+galleryConnectionColumns+" FROM gallery_highlight_connections WHERE id=? AND account_id=?", now, revision, id, actor.AccountID))
	if e == sql.ErrNoRows {
		return out, model.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if out.Revision != expected {
		return out, model.ErrGalleryConflict
	}
	if out.RevokedAt != nil {
		return out, tx.Commit()
	}
	if expected == math.MaxInt64 {
		return out, model.ErrGalleryConflict
	}
	if _, e = tx.ExecContext(ctx, "UPDATE gallery_highlight_connections SET revoked_at=?,revision=revision+1 WHERE id=? AND account_id=?", now, id, actor.AccountID); e != nil {
		return out, e
	}
	out.RevokedAt = &now
	out.Revision++
	out.State = "revoked"
	return out, tx.Commit()
}
func galleryGrant(ctx context.Context, q queryer, hash []byte, now int64) (model.GalleryGrant, error) {
	var v model.GalleryGrant
	var profiles int
	if e := q.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='account_profiles'").Scan(&profiles); e != nil {
		return v, e
	}
	join, epoch := "", "1"
	if profiles > 0 {
		join = " JOIN account_profiles ap ON ap.account_id=a.id AND ap.enabled=1 AND ap.must_change_password=0"
		epoch = "ap.acl_revision"
	}
	e := scopeRow(ctx, q, `SELECT c.id,c.account_id,c.revision,c.auth_revision,c.expires_at,`+epoch+` FROM gallery_highlight_connections c JOIN accounts a ON a.id=c.account_id AND a.auth_revision=c.auth_revision`+join+` WHERE c.token_hash=? AND c.revoked_at IS NULL AND (c.expires_at IS NULL OR c.expires_at>?)`, hash, now).Scan(&v.ID, &v.AccountID, &v.Revision, &v.AuthRevision, &v.ExpiresAt, &v.ACLRevision)
	if e == sql.ErrNoRows {
		return v, model.ErrGalleryAuth
	}
	if e != nil {
		return v, e
	}
	v.Hash = hash
	if e = galleryEnabled(ctx, q); e != nil {
		return v, e
	}
	return v, nil
}
func (s *Store) GalleryGrant(ctx context.Context, hash []byte, now int64) (model.GalleryGrant, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.GalleryGrant{}, e
	}
	defer tx.Rollback()
	v, e := galleryGrant(ctx, tx, hash, now)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}

// Serialize only final authorization and bounded in-memory result freezing with
// disable/revoke/password commits. No write transaction, lookup or network IO
// is held under this read lease. A queued writer also prevents new publishers.
func (s *Store) GalleryPublish(ctx context.Context, grant model.GalleryGrant, now func() int64, freeze func() error) error {
	for !s.galleryPublish.TryRLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	defer s.galleryPublish.RUnlock()
	v, e := s.GalleryGrant(ctx, grant.Hash, now())
	if e != nil {
		return e
	}
	if v.ID != grant.ID || v.AccountID != grant.AccountID || v.Revision != grant.Revision || v.AuthRevision != grant.AuthRevision || v.ACLRevision != grant.ACLRevision {
		return model.ErrGalleryAuth
	}
	return freeze()
}

const galleryLookupFrom = ` FROM external_metadata m CROSS JOIN edition_sources s CROSS JOIN editions e CROSS JOIN files f CROSS JOIN libraries l WHERE s.metadata_id=m.id AND e.id=s.edition_id AND f.edition_id=e.id AND l.id=e.library_id AND m.provider='ehentai' AND l.disabled=0 AND l.trashed_at IS NULL AND e.trashed_at IS NULL AND m.external_id IN (`

func (s *Store) GalleryLookup(ctx context.Context, grant model.GalleryGrant, ids []string, now int64) ([]model.GalleryMatch, error) {
	out := make([]model.GalleryMatch, len(ids))
	for i, id := range ids {
		out[i] = model.GalleryMatch{GalleryID: id, State: "none"}
	}
	a, e := s.AccountByID(ctx, grant.AccountID)
	if e != nil {
		return nil, e
	}
	if a == nil || !a.Enabled || a.MustChangePassword {
		return nil, model.ErrGalleryAuth
	}
	ctx = model.WithActor(ctx, *a)
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if _, e = galleryGrant(ctx, tx, grant.Hash, now); e != nil {
		return nil, e
	}
	if len(ids) > 0 {
		args := make([]any, len(ids))
		marks := make([]string, len(ids))
		for i, id := range ids {
			args[i] = id
			marks[i] = "?"
		}
		rows, e := scopeRows(ctx, tx, `SELECT m.external_id,max(f.file_state='available' AND s.matched_content_revision=f.content_revision)`+galleryLookupFrom+strings.Join(marks, ",")+") GROUP BY m.external_id", args...)
		if e != nil {
			return nil, e
		}
		states := map[string]string{}
		for rows.Next() {
			var id string
			var normal bool
			if e = rows.Scan(&id, &normal); e != nil {
				rows.Close()
				return nil, e
			}
			state := "needs_review"
			if normal {
				state = "collected"
			}
			states[id] = state
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for i := range out {
			if state, ok := states[out[i].GalleryID]; ok {
				out[i].State = state
			}
		}
	}
	return out, tx.Commit()
}

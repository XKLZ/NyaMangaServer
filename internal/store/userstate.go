package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

func userState(ctx context.Context, q queryer, account, id string) (model.UserState, error) {
	var v model.UserState
	e := scopeRow(ctx, q, "SELECT favorite,hidden,revision FROM edition_user_states WHERE account_id=? AND edition_id=?", account, id).Scan(&v.Favorite, &v.Hidden, &v.Revision)
	if errors.Is(e, sql.ErrNoRows) {
		e = nil
	}
	return v, e
}
func (s *Store) UserState(ctx context.Context, account, id string) (model.UserState, error) {
	if _, e := s.Edition(ctx, id); e != nil {
		return model.UserState{}, e
	}
	return userState(ctx, s.Read, account, id)
}
func setState(ctx context.Context, tx *sql.Tx, account, id string, p model.StatePatch) (model.UserState, error) {
	var exists int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM "+editionRecordSQL+" editions WHERE id=?", id).Scan(&exists); e != nil {
		return model.UserState{}, e
	}
	if exists == 0 {
		return model.UserState{}, model.ErrNotFound
	}
	v, e := userState(ctx, tx, account, id)
	if e != nil {
		return v, e
	}
	if v.Revision != p.Expected {
		return v, model.ErrConflict
	}
	if p.Favorite != nil {
		v.Favorite = *p.Favorite
	}
	if p.Hidden != nil {
		v.Hidden = *p.Hidden
	}
	v.Revision++
	_, e = tx.ExecContext(ctx, `INSERT INTO edition_user_states VALUES(?,?,?,?,?) ON CONFLICT(account_id,edition_id) DO UPDATE SET favorite=excluded.favorite,hidden=excluded.hidden,revision=excluded.revision`, account, id, v.Favorite, v.Hidden, v.Revision)
	return v, e
}
func (s *Store) SetUserState(ctx context.Context, account, id string, p model.StatePatch) (model.UserState, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.UserState{}, e
	}
	defer tx.Rollback()
	v, e := setState(ctx, tx, account, id, p)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}

package store

import (
	"context"
	"database/sql"
	"errors"
)

const accountColumns = "a.id,COALESCE(a.password_hash,''),a.auth_revision,p.username,p.role,p.enabled,p.must_change_password,p.revision,p.acl_revision"

func scanAccount(row interface{ Scan(...any) error }) (*Account, error) {
	var a Account
	var profileRevision int64
	e := row.Scan(&a.ID, &a.PasswordHash, &a.Revision, &a.Username, &a.Role, &a.Enabled, &a.MustChangePassword, &profileRevision, &a.ACLRevision)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	return &a, e
}
func (s *Store) Account(ctx context.Context) (*Account, error) {
	if s.Version < 25 {
		var a Account
		e := scopeRow(ctx, s.Read, "SELECT id,COALESCE(password_hash,''),auth_revision FROM accounts WHERE singleton=1").Scan(&a.ID, &a.PasswordHash, &a.Revision)
		if errors.Is(e, sql.ErrNoRows) {
			return nil, nil
		}
		a.Username = "admin"
		a.Role = "admin"
		a.Enabled = true
		return &a, e
	}
	a, e := scanAccount(scopeRow(ctx, s.Read, "SELECT "+accountColumns+" FROM accounts a JOIN account_profiles p ON p.account_id=a.id WHERE a.singleton=1"))
	return s.accountGrants(ctx, a, e)
}
func (s *Store) AccountByID(ctx context.Context, id string) (*Account, error) {
	if s.Version < 25 {
		a, e := s.Account(ctx)
		if a != nil && a.ID != id {
			return nil, nil
		}
		return a, e
	}
	a, e := scanAccount(scopeRow(ctx, s.Read, "SELECT "+accountColumns+" FROM accounts a JOIN account_profiles p ON p.account_id=a.id WHERE a.id=?", id))
	return s.accountGrants(ctx, a, e)
}
func (s *Store) AccountByUsername(ctx context.Context, name string) (*Account, error) {
	a, e := scanAccount(scopeRow(ctx, s.Read, "SELECT "+accountColumns+" FROM accounts a JOIN account_profiles p ON p.account_id=a.id WHERE p.username=? COLLATE NOCASE", name))
	return s.accountGrants(ctx, a, e)
}
func insertSession(ctx context.Context, tx *sql.Tx, v Session) error {
	_, e := tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,account_id,created_at,expires_at) VALUES(?,?,?,?)", v.Hash, v.AccountID, v.Created, v.Expires)
	return e
}
func (s *Store) Initialize(ctx context.Context, a Account, v Session) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var id string
	var hash sql.NullString
	var rev int64
	e = scopeRow(ctx, tx, "SELECT id,password_hash,auth_revision FROM accounts WHERE singleton=1").Scan(&id, &hash, &rev)
	if e == nil && hash.Valid {
		return ErrInitialized
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil {
		if a.ID != id || a.Revision != rev+1 {
			return ErrConflict
		}
		_, e = tx.ExecContext(ctx, "UPDATE accounts SET password_hash=?,auth_revision=?,password_changed_at=? WHERE singleton=1", a.PasswordHash, a.Revision, v.Created)
	} else {
		_, e = tx.ExecContext(ctx, "INSERT INTO accounts VALUES(1,?,?,?,?)", a.ID, a.PasswordHash, a.Revision, v.Created)
	}
	if e != nil {
		return e
	}
	if e = insertSession(ctx, tx, v); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) CreateSession(ctx context.Context, a Account, v Session) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	gate := ""
	if s.Version >= 25 {
		gate = " AND EXISTS(SELECT 1 FROM account_profiles p WHERE p.account_id=accounts.id AND p.enabled=1)"
	}
	r, e := tx.ExecContext(ctx, "INSERT INTO sessions(token_hash,account_id,created_at,expires_at) SELECT ?,id,?,? FROM accounts WHERE id=? AND auth_revision=? AND password_hash IS NOT NULL"+gate, v.Hash, v.Created, v.Expires, a.ID, a.Revision)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", v.Created); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) Session(ctx context.Context, hash []byte, now int64) (*Session, error) {
	var v Session
	e := scopeRow(ctx, s.Read, "SELECT token_hash,account_id,created_at,expires_at FROM sessions WHERE token_hash=? AND expires_at>?", hash, now).Scan(&v.Hash, &v.AccountID, &v.Created, &v.Expires)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	v.Account, e = s.AccountByID(ctx, v.AccountID)
	if e != nil {
		return nil, e
	}
	if v.Account == nil || !v.Account.Enabled {
		return nil, nil
	}
	return &v, nil
}
func (s *Store) Logout(ctx context.Context, hash []byte) error {
	_, e := s.Write.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", hash)
	return e
}
func (s *Store) ChangePassword(ctx context.Context, a Account, newHash string, now int64) error {
	s.galleryPublish.Lock()
	defer s.galleryPublish.Unlock()
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	r, e := tx.ExecContext(ctx, "UPDATE accounts SET password_hash=?,auth_revision=auth_revision+1,password_changed_at=? WHERE id=? AND auth_revision=?", newHash, now, a.ID, a.Revision)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE account_id=?", a.ID); e != nil {
		return e
	}
	if s.Version >= 25 {
		if _, e = tx.ExecContext(ctx, "INSERT INTO account_security_audit(actor_id,target_id,action,created_at) VALUES(?,?,'change_password',?)", a.ID, a.ID, now); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE account_profiles SET must_change_password=0,revision=revision+1 WHERE account_id=?", a.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE gallery_highlight_connections SET revoked_at=?,revision=revision+1 WHERE account_id=? AND revoked_at IS NULL", now, a.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func (s *Store) accountGrants(ctx context.Context, a *Account, e error) (*Account, error) {
	if e != nil || a == nil {
		return a, e
	}
	a.LibraryGrants = map[string]string{}
	rows, e := s.Read.QueryContext(ctx, "SELECT library_id,role FROM account_library_grants WHERE account_id=? ORDER BY library_id", a.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, role string
		if e = rows.Scan(&id, &role); e != nil {
			return nil, e
		}
		a.LibraryGrants[id] = role
	}
	return a, rows.Err()
}

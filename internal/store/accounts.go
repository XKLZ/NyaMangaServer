package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

func administrator(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (string, error) {
	actor, ok := model.Actor(ctx)
	if !ok {
		return "", model.ErrForbidden
	}
	var yes bool
	e := scopeRow(ctx, q, `SELECT EXISTS(SELECT 1 FROM account_profiles WHERE account_id=? AND role='admin' AND enabled=1 AND must_change_password=0)`, actor.ID).Scan(&yes)
	if e != nil {
		return "", e
	}
	if !yes {
		return "", model.ErrForbidden
	}
	return actor.ID, nil
}
func (s *Store) Accounts(ctx context.Context) ([]model.AccountView, error) {
	if _, e := administrator(ctx, s.Read); e != nil {
		return nil, e
	}
	rows, e := scopeRows(ctx, s.Read, `SELECT account_id,username,role,enabled,must_change_password,revision,acl_revision FROM account_profiles ORDER BY username`)
	if e != nil {
		return nil, e
	}
	out := []model.AccountView{}
	for rows.Next() {
		var a model.AccountView
		if e = rows.Scan(&a.ID, &a.Username, &a.Role, &a.Enabled, &a.MustChangePassword, &a.Revision, &a.ACLRevision); e != nil {
			rows.Close()
			return nil, e
		}
		a.Grants = map[string]string{}
		out = append(out, a)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = scopeRows(ctx, s.Read, `SELECT account_id,library_id,role FROM account_library_grants ORDER BY account_id,library_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var id, lib, role string
		if e = rows.Scan(&id, &lib, &role); e != nil {
			return nil, e
		}
		for i := range out {
			if out[i].ID == id {
				out[i].Grants[lib] = role
				break
			}
		}
	}
	return out, rows.Err()
}
func (s *Store) CreateAccount(ctx context.Context, id, username, hash string, now int64) (model.AccountView, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.AccountView{}, e
	}
	defer tx.Rollback()
	actor, e := administrator(ctx, tx)
	if e != nil {
		return model.AccountView{}, e
	}
	var exists bool
	if e = scopeRow(ctx, tx, "SELECT EXISTS(SELECT 1 FROM account_profiles WHERE username=? COLLATE NOCASE)", username).Scan(&exists); e != nil {
		return model.AccountView{}, e
	}
	if exists {
		return model.AccountView{}, model.ErrAccountConflict
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO accounts(singleton,id,password_hash,auth_revision,password_changed_at) SELECT COALESCE(max(singleton),0)+1,?,?,1,? FROM accounts`, id, hash, now)
	if e != nil {
		return model.AccountView{}, e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE account_profiles SET username=?,role='user',must_change_password=1 WHERE account_id=?`, username, id); e != nil {
		return model.AccountView{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO account_security_audit(actor_id,target_id,action,created_at) VALUES(?,?,'create',?)`, actor, id, now); e != nil {
		return model.AccountView{}, e
	}
	if e = tx.Commit(); e != nil {
		return model.AccountView{}, e
	}
	return model.AccountView{ID: id, Username: username, Role: "user", Enabled: true, MustChangePassword: true, Revision: 1, ACLRevision: 1, Grants: map[string]string{}}, nil
}
func (s *Store) UpdateAccount(ctx context.Context, id string, in model.AccountUpdate, newHash string, now int64) error {
	s.galleryPublish.Lock()
	defer s.galleryPublish.Unlock()
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	actor, e := administrator(ctx, tx)
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE temp.acl_actor SET account_management=1 WHERE singleton=1"); e != nil {
		return e
	}
	var role string
	var enabled bool
	var revision int64
	if e = scopeRow(ctx, tx, "SELECT role,enabled,revision FROM account_profiles WHERE account_id=?", id).Scan(&role, &enabled, &revision); e == sql.ErrNoRows {
		return model.ErrNotFound
	} else if e != nil {
		return e
	}
	if revision != in.Expected {
		return model.ErrAccountConflict
	}
	if in.Role != "user" && in.Role != "admin" {
		return model.ErrInput
	}
	if role == "admin" && enabled && (in.Role != "admin" || !in.Enabled) {
		var others int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM account_profiles WHERE account_id<>? AND role='admin' AND enabled=1", id).Scan(&others); e != nil {
			return e
		}
		if others == 0 {
			return model.ErrLastAdministrator
		}
	}
	oldGrants := map[string]string{}
	rows, err := scopeRows(ctx, tx, "SELECT library_id,role FROM account_library_grants WHERE account_id=?", id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var lib, r string
		if err = rows.Scan(&lib, &r); err != nil {
			rows.Close()
			return err
		}
		oldGrants[lib] = r
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for lib, r := range in.Grants {
		if r != "viewer" && r != "editor" {
			return model.ErrInput
		}
		var n int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM libraries WHERE id=?", lib).Scan(&n); e != nil {
			return e
		}
		if n != 1 {
			return model.ErrNotFound
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE account_profiles SET role=?,enabled=?,must_change_password=CASE WHEN ?<>'' THEN 1 ELSE must_change_password END,revision=revision+1,acl_revision=acl_revision+1 WHERE account_id=?`, in.Role, in.Enabled, newHash, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM account_library_grants WHERE account_id=?", id); e != nil {
		return e
	}
	for lib, r := range in.Grants {
		if _, e = tx.ExecContext(ctx, "INSERT INTO account_library_grants VALUES(?,?,?)", id, lib, r); e != nil {
			return e
		}
	}
	// Revoke execution generations in the same authorization transaction.
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='paused',generation=generation+1,error_code='authorization_revoked',updated_at=? WHERE actor_id=? AND state IN('queued','running') AND NOT EXISTS(SELECT 1 FROM account_profiles p WHERE p.account_id=jobs.actor_id AND p.enabled=1 AND p.must_change_password=0 AND (p.role='admin' OR (jobs.library_id IS NOT NULL AND EXISTS(SELECT 1 FROM account_library_grants g WHERE g.account_id=p.account_id AND g.library_id=jobs.library_id AND g.role='editor'))))`, now, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM job_resources WHERE job_id IN(SELECT id FROM jobs WHERE actor_id=? AND state='paused' AND error_code='authorization_revoked')`, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE import_batches SET state='paused',error_code='authorization_revoked',revision=revision+1,updated_at=? WHERE actor_id=? AND state NOT IN('completed','cancelled') AND NOT EXISTS(SELECT 1 FROM account_profiles p WHERE p.account_id=import_batches.actor_id AND p.enabled=1 AND p.must_change_password=0 AND (p.role='admin' OR EXISTS(SELECT 1 FROM account_library_grants g WHERE g.account_id=p.account_id AND g.library_id=import_batches.library_id AND g.role='editor')))`, now, id); e != nil {
		return e
	}
	if !in.Enabled || in.Role != "admin" || newHash != "" {
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_transitions SET state='failed',error_code='authorization_revoked',updated_at=? WHERE actor_id=? AND state='pending'", now, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET state='failed' WHERE transition_id IN(SELECT id FROM enhancement_transitions WHERE actor_id=? AND error_code='authorization_revoked') AND state IN('enabling','disabling')", id); e != nil {
			return e
		}
	}
	// Only disabling or resetting credentials permanently revokes connections.
	securityInvalidation := !in.Enabled || newHash != ""
	if securityInvalidation {
		if _, e = tx.ExecContext(ctx, `UPDATE accounts SET auth_revision=auth_revision+1,password_hash=CASE WHEN ?<>'' THEN ? ELSE password_hash END,password_changed_at=CASE WHEN ?<>'' THEN ? ELSE password_changed_at END WHERE id=?`, newHash, newHash, newHash, now, id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM sessions WHERE account_id=?", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE gallery_highlight_connections SET revoked_at=?,revision=revision+1 WHERE account_id=? AND revoked_at IS NULL", now, id); e != nil {
			return e
		}
	}
	action := "update"
	if newHash != "" {
		action = "reset_password"
	}
	changes, _ := json.Marshal(map[string]any{"previous_role": role, "previous_enabled": enabled, "previous_library_grants": oldGrants, "role": in.Role, "enabled": in.Enabled, "library_grants": in.Grants})
	if _, e = tx.ExecContext(ctx, `INSERT INTO account_security_audit(actor_id,target_id,action,changes_json,created_at) VALUES(?,?,?,?,?)`, actor, id, action, string(changes), now); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) SELECT 'account_permissions',account_id,acl_revision,?,account_id FROM account_profiles WHERE account_id=?", now, id); e != nil {
		return e
	}

	return tx.Commit()
}

func (s *Store) HasEditingLibrary(ctx context.Context) (bool, error) {
	a, ok := model.Actor(ctx)
	if !ok {
		return false, model.ErrForbidden
	}
	var yes bool
	e := s.Read.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM account_profiles p WHERE p.account_id=? AND enabled=1 AND must_change_password=0 AND (role='admin' OR EXISTS(SELECT 1 FROM account_library_grants g WHERE g.account_id=p.account_id AND g.role='editor')))`, a.ID).Scan(&yes)
	return yes, e
}
func (s *Store) RequireAdministrator(ctx context.Context) error {
	if _, ok := model.Actor(ctx); !ok {
		return nil
	}
	_, e := administrator(ctx, s.Read)
	return e
}

func validateAccountIntegrity(ctx context.Context, q queryer) error {
	var profiles int
	if e := q.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='account_profiles'").Scan(&profiles); e != nil {
		return e
	}
	if profiles == 0 {
		var version int
		if e := q.QueryRowContext(ctx, "SELECT COALESCE(max(version),0) FROM schema_migrations").Scan(&version); e != nil {
			return e
		}
		if version >= 25 {
			return errors.New("account_identity_missing")
		}
		return nil
	}
	var accounts, matched, legacy, initialized, administrators int
	e := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(EXISTS(SELECT 1 FROM account_profiles p WHERE p.account_id=a.id)),0),COALESCE(sum(singleton=1),0),COALESCE(sum(password_hash IS NOT NULL),0),COALESCE(sum(password_hash IS NOT NULL AND EXISTS(SELECT 1 FROM account_profiles p WHERE p.account_id=a.id AND p.enabled=1 AND p.role='admin')),0) FROM accounts a`).Scan(&accounts, &matched, &legacy, &initialized, &administrators)

	if e != nil {
		return e
	}
	if accounts != matched || accounts > 0 && legacy != 1 || initialized > 0 && administrators == 0 {
		return errors.New("account_identity_inconsistent")
	}
	return nil
}

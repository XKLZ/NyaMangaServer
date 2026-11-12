package store

import (
	"context"
	"database/sql"
	"errors"

	"nyamangaserver.local/server/internal/model"
)

const deviceColumns = "d.id,d.account_id,d.client_instance_id,d.device_name,d.created_at,d.expires_at,d.last_active_at,d.revoked_at"
const liveDeviceSQL = "d.revoked_at IS NULL AND d.expires_at>? AND d.auth_revision=a.auth_revision AND p.enabled=1"

func scanDevice(row interface{ Scan(...any) error }) (model.DeviceSession, error) {
	var v model.DeviceSession
	e := row.Scan(&v.ID, &v.AccountID, &v.ClientInstanceID, &v.DeviceName, &v.CreatedAt, &v.ExpiresAt, &v.LastActiveAt, &v.RevokedAt)
	return v, e
}

func (s *Store) CreateDeviceSession(ctx context.Context, a model.Account, v model.DeviceSession, hash []byte) error {
	if _, ok := model.Actor(ctx); ok {
		return model.ErrForbidden
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var revision int64
	e = tx.QueryRowContext(ctx, `SELECT a.auth_revision FROM accounts a JOIN account_profiles p ON p.account_id=a.id
		WHERE a.id=? AND a.password_hash IS NOT NULL AND p.enabled=1`, a.ID).Scan(&revision)
	if errors.Is(e, sql.ErrNoRows) || e == nil && revision != a.Revision {
		return model.ErrConflict
	}
	if e != nil {
		return e
	}
	var active int
	e = tx.QueryRowContext(ctx, `SELECT count(*) FROM device_sessions
		WHERE account_id=? AND auth_revision=? AND revoked_at IS NULL AND expires_at>?`, a.ID, revision, v.CreatedAt).Scan(&active)
	if e != nil {
		return e
	}
	if active >= model.DeviceLimit {
		return model.ErrDeviceLimit
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO device_sessions
		(id,account_id,client_instance_id,device_name,token_hash,auth_revision,created_at,expires_at,last_active_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, v.ID, a.ID, v.ClientInstanceID, v.DeviceName, hash, revision, v.CreatedAt, v.ExpiresAt, v.LastActiveAt); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) DeviceSession(ctx context.Context, hash []byte, now int64) (*model.DeviceSession, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	v, e := scanDevice(tx.QueryRowContext(ctx, "SELECT "+deviceColumns+` FROM device_sessions d JOIN accounts a ON a.id=d.account_id
		JOIN account_profiles p ON p.account_id=a.id WHERE d.token_hash=? AND `+liveDeviceSQL, hash, now))
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	v.Account, e = scanAccount(tx.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts a JOIN account_profiles p ON p.account_id=a.id WHERE a.id=?", v.AccountID))
	if e != nil {
		return nil, e
	}
	v.Account, e = accountGrantsFrom(ctx, tx, v.Account, nil)
	if e != nil {
		return nil, e
	}
	return &v, tx.Commit()
}

func (s *Store) DeviceSessions(ctx context.Context) ([]model.DeviceSession, error) {
	a, ok := model.Actor(ctx)
	if !ok {
		return nil, model.ErrForbidden
	}
	rows, e := s.Read.QueryContext(ctx, "SELECT "+deviceColumns+` FROM device_sessions d JOIN account_profiles p ON p.account_id=d.account_id
		WHERE d.account_id=? AND p.enabled=1 AND p.must_change_password=0 ORDER BY d.created_at DESC,d.id`, a.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.DeviceSession{}
	for rows.Next() {
		v, e := scanDevice(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) RenewDeviceSession(ctx context.Context, now int64) (model.DeviceSession, error) {
	var v model.DeviceSession
	d, ok := model.DeviceActor(ctx)
	if !ok {
		return v, model.ErrDeviceSession
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	now = d.Now().Unix()
	a, _ := model.Actor(ctx)
	v, e = scanDevice(tx.QueryRowContext(ctx, "SELECT "+deviceColumns+` FROM device_sessions d JOIN accounts a ON a.id=d.account_id
		JOIN account_profiles p ON p.account_id=a.id WHERE d.id=? AND d.account_id=? AND `+liveDeviceSQL, d.ID, a.ID, now))
	if errors.Is(e, sql.ErrNoRows) {
		return v, model.ErrDeviceSession
	}
	if e != nil {
		return v, e
	}
	if v.ExpiresAt-now >= model.DeviceRenewWindowSeconds {
		return v, tx.Commit()
	}
	v.ExpiresAt = now + model.DeviceLifetimeSeconds
	if _, e = tx.ExecContext(ctx, "UPDATE device_sessions SET expires_at=? WHERE id=? AND account_id=?", v.ExpiresAt, d.ID, a.ID); e != nil {
		return v, e
	}
	return v, tx.Commit()
}

func (s *Store) RevokeDeviceSession(ctx context.Context, id string, now int64) error {
	a, ok := model.Actor(ctx)
	if !ok {
		return model.ErrForbidden
	}
	_, e := s.Write.ExecContext(ctx, "UPDATE device_sessions SET revoked_at=? WHERE account_id=? AND id=? AND revoked_at IS NULL", now, a.ID, id)
	return e
}

func (s *Store) TouchDeviceSession(ctx context.Context, now int64) error {
	d, ok := model.DeviceActor(ctx)
	if !ok {
		return model.ErrDeviceSession
	}
	a, _ := model.Actor(ctx)
	// The predicate skips the write while a maintenance barrier is active.
	_, e := s.Write.ExecContext(ctx, `UPDATE device_sessions SET last_active_at=?
		WHERE id=? AND account_id=? AND last_active_at<=? AND
		(SELECT phase FROM health_state WHERE singleton=1)='idle'`, now, d.ID, a.ID, now-model.DeviceActivitySeconds)
	return e
}

func revokeAccountDevices(ctx context.Context, tx *sql.Tx, id string, now int64) error {
	_, e := tx.ExecContext(ctx, "UPDATE device_sessions SET revoked_at=? WHERE account_id=? AND revoked_at IS NULL", now, id)
	return e
}

func checkDeviceActor(ctx context.Context, q queryer, id string, d model.DevicePrincipal) error {
	var active bool
	e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM device_sessions d JOIN accounts a ON a.id=d.account_id
		JOIN account_profiles p ON p.account_id=a.id WHERE d.id=? AND d.account_id=? AND `+liveDeviceSQL+`) `, d.ID, id, d.Now().Unix()).Scan(&active)
	if e == nil && !active {
		e = model.ErrDeviceSession
	}
	return e
}

func deviceTableWrite(table, op, row, actor, passwordChange string) string {
	owned := row + "account_id=" + actor
	if op == "UPDATE" {
		owned += " AND OLD.account_id=NEW.account_id"
	}
	switch table {
	case "edition_user_states", "reading_states", "reading_sessions", "reading_state_requests", "saved_searches", "tag_follows", "tag_follow_epochs":
		return owned
	case "instance_state":
		if op == "UPDATE" {
			return "NEW.write_epoch=OLD.write_epoch+1 AND NEW.owner IS OLD.owner AND NEW.background_hold=OLD.background_hold AND NEW.revision=OLD.revision"
		}
	case "device_sessions":
		if op == "UPDATE" {
			return owned + " AND NEW.id=OLD.id AND NEW.token_hash=OLD.token_hash AND NEW.auth_revision=OLD.auth_revision AND NEW.client_instance_id=OLD.client_instance_id AND NEW.device_name=OLD.device_name AND NEW.created_at=OLD.created_at"
		}
	case "catalog_state":
		if op == "UPDATE" {
			return "NEW.singleton=OLD.singleton AND NEW.revision=OLD.revision+1"
		}
	case "events":
		switch op {
		case "INSERT":
			return "NEW.owner_account_id=" + actor + " OR (NEW.type='catalog' AND NEW.resource_id='catalog' AND NEW.owner_account_id IS NULL)"
		case "UPDATE":
			// The existing event-scope trigger fills acl_library_id after insertion.
			return "NEW.id=OLD.id AND NEW.type=OLD.type AND NEW.resource_id=OLD.resource_id AND NEW.revision=OLD.revision AND NEW.created_at=OLD.created_at AND NEW.owner_account_id IS OLD.owner_account_id AND NEW.acl_library_id IS NULL"
		case "DELETE":
			return "OLD.created_at<unixepoch()-86400 OR OLD.id<=COALESCE((SELECT id FROM main.events ORDER BY id DESC LIMIT 1 OFFSET 10000),0)"
		}
	case "accounts":
		if op == "UPDATE" {
			return passwordChange + " AND OLD.id=" + actor + " AND NEW.id=OLD.id AND NEW.singleton=OLD.singleton"
		}
	case "account_profiles", "gallery_highlight_connections":
		if op == "UPDATE" {
			return passwordChange + " AND " + owned
		}
	case "sessions":
		if op == "DELETE" {
			return passwordChange + " AND " + owned
		}
	case "account_security_audit":
		if op == "INSERT" {
			return passwordChange + " AND NEW.actor_id=" + actor + " AND NEW.target_id=" + actor + " AND NEW.action='change_password'"
		}
	}
	return "0"
}

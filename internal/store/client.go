package store

import (
	"context"
	"database/sql"

	"nyamangaserver.local/server/internal/model"
)

func (s *Store) ClientIdentity(ctx context.Context) (model.ClientIdentity, error) {
	return clientIdentity(ctx, s.Read)
}

func clientIdentity(ctx context.Context, q queryer) (model.ClientIdentity, error) {
	var v model.ClientIdentity
	e := q.QueryRowContext(ctx, "SELECT instance_id,cache_epoch FROM instance_client_identity WHERE singleton=1").Scan(&v.InstanceID, &v.CacheEpoch)
	return v, e
}

func (s *Store) ClientConfiguration(ctx context.Context) (model.ClientConfiguration, error) {
	var v model.ClientConfiguration
	a, ok := model.Actor(ctx)
	if !ok {
		return v, model.ErrForbidden
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if d, device := model.DeviceActor(ctx); device {
		if e = checkDeviceActor(ctx, tx, a.ID, d); e != nil {
			return v, e
		}
	}
	if v.Identity, e = clientIdentity(ctx, tx); e != nil {
		return v, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT h.phase<>'idle' OR i.owner IS NOT NULL,i.background_hold
		FROM health_state h JOIN instance_state i ON i.singleton=h.singleton WHERE h.singleton=1`).Scan(&v.HealthMaintenance, &v.BackgroundHold); e != nil {
		return v, e
	}
	v.Libraries = []model.ClientScopeLibrary{}
	rows, e := scopeRows(ctx, tx, `SELECT l.id,CASE WHEN p.role='admin' THEN 'admin' ELSE g.role END,l.disabled
		FROM libraries l JOIN account_profiles p ON p.account_id=? AND p.enabled=1 AND p.must_change_password=0
		LEFT JOIN account_library_grants g ON g.account_id=p.account_id AND g.library_id=l.id
		WHERE l.trashed_at IS NULL AND (p.role='admin' OR g.role IS NOT NULL) ORDER BY l.id`, a.ID)
	if e != nil {
		return v, e
	}
	defer rows.Close()
	for rows.Next() {
		var l model.ClientScopeLibrary
		if e = rows.Scan(&l.ID, &l.Role, &l.Disabled); e != nil {
			return v, e
		}
		v.Libraries = append(v.Libraries, l)
	}
	if e = rows.Err(); e != nil {
		return v, e
	}
	return v, tx.Commit()
}

func invalidateClientInstance(ctx context.Context, tx *sql.Tx) error {
	var exists bool
	e := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='instance_client_identity')").Scan(&exists)
	if e != nil || !exists {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM device_sessions"); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE instance_client_identity SET cache_epoch=lower(hex(randomblob(16))) WHERE singleton=1")
	return e
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

const instanceColumns = "id,kind,state,phase,revision,bytes,digest,error_code,created_at,updated_at"

func scanInstance(r scanner) (model.InstanceOperation, error) {
	var v model.InstanceOperation
	e := r.Scan(&v.ID, &v.Kind, &v.State, &v.Phase, &v.Revision, &v.Bytes, &v.Digest, &v.Error, &v.Created, &v.Updated)
	if errors.Is(e, sql.ErrNoRows) {
		e = model.ErrNotFound
	}
	return v, e
}
func (s *Store) InstanceOperation(ctx context.Context, id string) (model.InstanceOperation, error) {
	v, e := scanInstance(scopeRow(ctx, s.Read, "SELECT "+instanceColumns+" FROM instance_operations WHERE id=?", id))
	if e == nil && s.Version >= 27 {
		e = scopeRow(ctx, s.Read, "SELECT EXISTS(SELECT 1 FROM storage_material_receipts WHERE id=?)", "instance:"+id).Scan(&v.MaterialsCleaned)
	}
	return v, e
}
func (s *Store) InstanceOperations(ctx context.Context, kind string, page, size int, requestKey ...string) ([]model.InstanceOperation, error) {
	if page < 1 || size < 1 || size > 100 || (kind != "backup" && kind != "restore") || len(requestKey) > 1 || len(requestKey) == 1 && (len(requestKey[0]) < 8 || len(requestKey[0]) > 128) {
		return nil, model.ErrInput
	}
	query := "SELECT " + instanceColumns + " FROM instance_operations WHERE kind=?"
	args := []any{kind}
	if len(requestKey) == 1 {
		query += " AND request_key=?"
		args = append(args, requestKey[0])
	}
	query += " ORDER BY created_at DESC,id LIMIT ? OFFSET ?"
	args = append(args, size, (page-1)*size)
	rows, e := scopeRows(ctx, s.Read, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.InstanceOperation{}
	for rows.Next() {
		v, e := scanInstance(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	rows.Close()
	if s.Version >= 27 {
		for i := range out {
			if e = scopeRow(ctx, s.Read, "SELECT EXISTS(SELECT 1 FROM storage_material_receipts WHERE id=?)", "instance:"+out[i].ID).Scan(&out[i].MaterialsCleaned); e != nil {
				return nil, e
			}
		}
	}
	return out, nil
}
func (s *Store) CreateInstanceOperation(ctx context.Context, id, kind, key string) (model.InstanceOperation, bool, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return model.InstanceOperation{}, false, e
	}
	defer tx.Rollback()
	old, e := scanInstance(scopeRow(ctx, tx, "SELECT "+instanceColumns+" FROM instance_operations WHERE request_key=?", key))
	if e == nil {
		if old.Kind != kind {
			return old, false, model.ErrConflict
		}
		return old, false, nil
	}
	if !errors.Is(e, model.ErrNotFound) {
		return old, false, e
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO instance_operations(id,kind,request_key,state,created_at,updated_at) VALUES(?,?,?,'pending',unixepoch(),unixepoch())", id, kind, key)
	if e != nil {
		return old, false, e
	}
	if e = tx.Commit(); e != nil {
		return old, false, e
	}
	v, e := s.InstanceOperation(ctx, id)
	return v, true, e
}
func (s *Store) SetInstanceOperation(ctx context.Context, id string, revision int64, state, phase, private string, bytes int64, digest, code string) (model.InstanceOperation, error) {
	r, e := s.Write.ExecContext(ctx, "UPDATE instance_operations SET state=?,phase=?,private_json=?,bytes=?,digest=?,error_code=?,revision=revision+1,updated_at=unixepoch() WHERE id=? AND revision=?", state, phase, private, bytes, digest, code, id, revision)
	if e != nil {
		return model.InstanceOperation{}, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return model.InstanceOperation{}, e
	}
	if n != 1 {
		return model.InstanceOperation{}, model.ErrConflict
	}
	return s.InstanceOperation(ctx, id)
}

// A failed upload owns only its original pending generation. In particular, an
// uncertain SQL response must never demote an uploaded or abandoned operation.
func (s *Store) FailPendingInstanceUpload(ctx context.Context, id string, revision, size int64, code string) (model.InstanceOperation, error) {
	_, e := s.Write.ExecContext(ctx, `UPDATE instance_operations SET state='failed',phase='upload',bytes=?,digest='',error_code=?,revision=revision+1,updated_at=unixepoch() WHERE id=? AND kind='restore' AND state='pending' AND revision=?`, size, code, id, revision)
	if e != nil {
		return model.InstanceOperation{}, e
	}
	return s.InstanceOperation(ctx, id)
}
func (s *Store) InstancePrivate(ctx context.Context, id string) (string, error) {
	var v string
	e := scopeRow(ctx, s.Read, "SELECT private_json FROM instance_operations WHERE id=?", id).Scan(&v)
	return v, e
}
func (s *Store) InstanceState(ctx context.Context) (model.InstanceState, error) {
	var v model.InstanceState
	if s.Version < 24 {
		return v, nil
	}
	e := scopeRow(ctx, s.Read, "SELECT owner,background_hold,write_epoch,revision FROM instance_state WHERE singleton=1").Scan(&v.Owner, &v.BackgroundHold, &v.WriteEpoch, &v.Revision)
	return v, e
}
func (s *Store) AutomaticWorkAllowed(ctx context.Context) (bool, error) {
	v, e := s.InstanceState(ctx)
	return e == nil && !v.BackgroundHold && v.Owner == nil, e
}

func (s *Store) RecoverInstanceOperations(ctx context.Context) error {
	_, e := s.Write.ExecContext(ctx, "UPDATE instance_operations SET state='interrupted',error_code='process_interrupted',revision=revision+1,updated_at=unixepoch() WHERE state IN('pending','running','validating','preparing')")
	return e
}

// Acquire the existing persistent SQL/IO barrier. No task is automatically resumed.
func (s *Store) BeginInstanceProtection(ctx context.Context, id string, revision int64, expectedEpoch ...int64) error {
	return s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var phase string
		var owner *string
		if e = scopeRow(ctx, tx, "SELECT phase FROM health_state WHERE singleton=1").Scan(&phase); e != nil {
			return e
		}
		if e = scopeRow(ctx, tx, "SELECT owner FROM instance_state WHERE singleton=1").Scan(&owner); e != nil {
			return e
		}
		if phase != "idle" || owner != nil {
			return model.ErrHealthMaintenance
		}
		if len(expectedEpoch) > 0 {
			var epoch int64
			if e = scopeRow(ctx, tx, "SELECT write_epoch FROM instance_state WHERE singleton=1").Scan(&epoch); e != nil {
				return e
			}
			if epoch != expectedEpoch[0] {
				return model.ErrConflict
			}
		}
		var actual int64
		if e = scopeRow(ctx, tx, "SELECT revision FROM instance_operations WHERE id=?", id).Scan(&actual); e != nil {
			return e
		}
		if actual != revision {
			return model.ErrConflict
		}
		if _, e = tx.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,updated_at=unixepoch(),error_code='instance_maintenance' WHERE state IN('queued','running')"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET runtime_armed=0 WHERE runtime_armed<>0"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE instance_state SET owner=?,background_hold=1,revision=revision+1 WHERE singleton=1", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_state SET phase='instance_maintenance',revision=revision+1,updated_at=unixepoch() WHERE singleton=1"); e != nil {
			return e
		}
		return tx.Commit()
	})
}
func (s *Store) EndInstanceProtection(ctx context.Context, id string) error {
	return s.healthWrite(ctx, func(c *sql.Conn) error {
		tx, e := c.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		var owner *string
		var phase string
		if e = scopeRow(ctx, tx, "SELECT owner FROM instance_state WHERE singleton=1").Scan(&owner); e != nil {
			return e
		}
		if e = scopeRow(ctx, tx, "SELECT phase FROM health_state WHERE singleton=1").Scan(&phase); e != nil {
			return e
		}
		if owner == nil || *owner != id || phase != "instance_maintenance" {
			return model.ErrConflict
		}
		if _, e = tx.ExecContext(ctx, "UPDATE instance_state SET owner=NULL,revision=revision+1 WHERE singleton=1"); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_state SET phase='idle',revision=revision+1,updated_at=unixepoch() WHERE singleton=1"); e != nil {
			return e
		}
		return tx.Commit()
	})
}

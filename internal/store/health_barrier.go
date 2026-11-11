package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"
)

func init() {
	sqlite.RegisterConnectionHook(func(c sqlite.ExecQuerierContext, dsn string) error {
		u, e := url.Parse(dsn)
		if e != nil {
			return e
		}
		if u.Query().Get("mode") == "ro" {
			return nil
		}
		for _, v := range u.Query()["_pragma"] {
			if v == "query_only(1)" {
				return nil
			}
		}
		rows, e := c.QueryContext(context.Background(), "SELECT name FROM sqlite_master WHERE type='table' AND name='health_state'", nil)
		if e != nil {
			return e
		}
		v := make([]driver.Value, 1)
		e = rows.Next(v)
		rows.Close()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(u.Path), "health-write-unsafe")); err == nil {
			if _, err = c.ExecContext(context.Background(), "UPDATE health_state SET phase='recovery_required' WHERE singleton=1", nil); err != nil {
				return err
			}
		}
		return installHealthDriver(context.Background(), c)
	})
}
func installHealthDriver(ctx context.Context, c sqlite.ExecQuerierContext) error {
	if _, e := c.ExecContext(ctx, "CREATE TEMP TABLE IF NOT EXISTS health_permit(singleton INTEGER PRIMARY KEY,value INTEGER);INSERT OR IGNORE INTO health_permit VALUES(1,0)", nil); e != nil {
		return e
	}
	rows, e := c.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'", nil)
	if e != nil {
		return e
	}
	names := []string{}
	v := make([]driver.Value, 1)
	for {
		e = rows.Next(v)
		if e == io.EOF {
			break
		}
		if e != nil {
			rows.Close()
			return e
		}
		names = append(names, v[0].(string))
	}
	rows.Close()
	instance := false
	for _, name := range names {
		if name == "instance_state" {
			instance = true
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "health_") || strings.HasPrefix(name, "instance_") || name == "sessions" || name == "events" {
			continue
		}
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			if instance {
				statement := fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %q AFTER %s ON main.%q BEGIN UPDATE instance_state SET write_epoch=write_epoch+1 WHERE singleton=1;END`, "instance_epoch_"+name+"_"+op, op, name)
				if _, e = c.ExecContext(ctx, statement, nil); e != nil {
					return e
				}
			}
			condition := ""
			if name == "jobs" && op == "UPDATE" {
				condition = " AND NOT EXISTS(SELECT 1 FROM health_checks WHERE job_id=NEW.id) AND NOT EXISTS(SELECT 1 FROM health_repairs WHERE job_id=NEW.id)"
			}

			statement := fmt.Sprintf(`CREATE TEMP TRIGGER IF NOT EXISTS %q BEFORE %s ON main.%q WHEN COALESCE((SELECT phase FROM health_state WHERE singleton=1),'recovery_required')<>'idle' AND COALESCE((SELECT value FROM health_permit WHERE singleton=1),0)=0 %s BEGIN SELECT RAISE(ABORT,'maintenance_active');END`, "health_barrier_"+name+"_"+op, op, name, condition)
			if _, e = c.ExecContext(ctx, statement, nil); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) installHealthBarrier(ctx context.Context) error {
	if s.Version < 23 {
		return nil
	}
	c, e := s.Write.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	return c.Raw(func(raw any) error { return installHealthDriver(ctx, raw.(sqlite.ExecQuerierContext)) })
}

// The single write connection is exclusively leased. Other writers cannot use
// the temporary permission, which is reset before the connection is released.
func (s *Store) healthWrite(ctx context.Context, fn func(*sql.Conn) error) (result error) {
	c, e := s.Write.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	permit, e := c.ExecContext(ctx, "UPDATE temp.health_permit SET value=1 WHERE singleton=1")
	if e != nil {
		return e
	}
	n, e := permit.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrHealthRecovery
	}
	defer func() {
		reset, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, e := c.ExecContext(reset, "PRAGMA foreign_keys=ON")
		var enabled int
		if e == nil {
			e = scopeRow(reset, c, "PRAGMA foreign_keys").Scan(&enabled)
			if e == nil && enabled != 1 {
				e = model.ErrHealthRecovery
			}
		}
		var r sql.Result
		if e == nil {
			r, e = c.ExecContext(reset, "UPDATE temp.health_permit SET value=0 WHERE singleton=1")
		}
		if e == nil {
			var n int64
			n, e = r.RowsAffected()
			if e == nil && n != 1 {
				e = model.ErrHealthRecovery
			}
		}
		if e != nil {
			_ = os.WriteFile(filepath.Join(s.healthDir, "health-write-unsafe"), []byte("recovery_required"), 0600)
			_ = c.Raw(func(any) error { return driver.ErrBadConn })
			_ = c.Close()
			_, mark := s.Write.ExecContext(reset, "UPDATE health_state SET phase='recovery_required',revision=revision+1,updated_at=unixepoch() WHERE singleton=1")
			result = errors.Join(result, model.ErrHealthRecovery, e, mark)
		}
	}()
	return fn(c)
}

func (s *Store) HealthState(ctx context.Context) (model.HealthState, error) {
	out := model.HealthState{Phase: "idle"}
	if s.Version < 23 {
		return out, nil
	}
	e := scopeRow(ctx, s.Read, "SELECT phase,repair_id,revision,updated_at FROM health_state WHERE singleton=1").Scan(&out.Phase, &out.RepairID, &out.Revision, &out.UpdatedAt)
	if e != nil {
		return out, e
	}
	if out.RepairID == nil && out.Phase != "idle" {
		scopeRow(ctx, s.Read, "SELECT count(*) FROM jobs WHERE state='paused' AND error_code='health_recovery'").Scan(&out.PausedTasks)
		out.CanContinue = out.Phase == "recovery_required"
	}
	if out.RepairID != nil {
		scopeRow(ctx, s.Read, "SELECT count(*) FROM health_paused_jobs WHERE repair_id=?", *out.RepairID).Scan(&out.PausedTasks)
		out.CanContinue = out.Phase != "idle"
		out.CanRollback = out.CanContinue
	}
	return out, nil
}
func healthJobAllowed(ctx context.Context, q queryer, id string) error {
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='health_state'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	var allowed bool
	e := scopeRow(ctx, q, `SELECT phase='idle' OR (repair_id=? AND EXISTS(SELECT 1 FROM health_repairs WHERE job_id=?)) FROM health_state WHERE singleton=1`, id, id).Scan(&allowed)
	if e != nil {
		return e
	}
	if !allowed {
		return model.ErrHealthMaintenance
	}
	return nil
}

// HealthWriteAllowed reads only the global barrier bit, independently of business
// row visibility. Never clear the actor: subsequent SQL must retain its ACL.
func (s *Store) HealthWriteAllowed(ctx context.Context) error {
	if s.Version < 23 {
		return nil
	}
	var allowed bool
	if e := s.Read.QueryRowContext(ctx, "SELECT phase='idle' FROM health_state WHERE singleton=1").Scan(&allowed); e != nil {
		return e
	}
	if !allowed {
		return model.ErrHealthMaintenance
	}
	return nil
}
func (s *Store) BeginHealthIO(ctx context.Context) (func(), error) {
	if e := s.HealthWriteAllowed(ctx); e != nil {
		return nil, e
	}
	for !s.healthIO.TryRLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	if e := s.HealthWriteAllowed(ctx); e != nil {
		s.healthIO.RUnlock()
		return nil, e
	}
	return s.healthIO.RUnlock, nil
}
func (s *Store) HealthStorageLease(ctx context.Context) (func(), error) {
	for !s.healthIO.TryLock() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
	return s.healthIO.Unlock, nil
}

// Poison is cleared only on a verified unprivileged connection while the durable
// barrier still protects the matching repair (or explicit ownerless recovery).
func (s *Store) clearHealthPoison(ctx context.Context, c queryer, owner string) error {
	var permit, foreign int
	if e := scopeRow(ctx, c, "SELECT value FROM temp.health_permit WHERE singleton=1").Scan(&permit); e != nil {
		return e
	}
	if e := scopeRow(ctx, c, "PRAGMA foreign_keys").Scan(&foreign); e != nil {
		return e
	}
	if permit != 0 || foreign != 1 {
		return model.ErrHealthRecovery
	}
	var actual, phase string
	if e := scopeRow(ctx, c, "SELECT COALESCE(repair_id,''),phase FROM health_state WHERE singleton=1").Scan(&actual, &phase); e != nil {
		return e
	}
	if actual != owner || phase == "idle" {
		return model.ErrHealthRecovery
	}
	path := filepath.Join(s.healthDir, "health-write-unsafe")
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	raw, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if string(raw) != "recovery_required" {
		return model.ErrHealthRecovery
	}
	if e = os.Remove(path); e != nil {
		return e
	}
	dir, e := os.Open(s.healthDir)
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *Store) RecoverHealthProtection(ctx context.Context, revision int64) error {
	state, e := s.HealthState(ctx)
	if e != nil {
		return e
	}
	if state.Phase == "idle" {
		return nil
	}
	if state.Revision != revision || state.RepairID != nil || state.Phase != "recovery_required" {
		return model.ErrHealthStale
	}
	var active int
	if e = scopeRow(ctx, s.Read, "SELECT count(*) FROM health_repairs WHERE state NOT IN('completed','rolled_back')").Scan(&active); e != nil {
		return e
	}
	if active != 0 {
		return model.ErrHealthRecovery
	}
	if e = os.MkdirAll(filepath.Join(s.healthDir, "health"), 0700); e != nil {
		return e
	}
	root, e := os.MkdirTemp(filepath.Join(s.healthDir, "health"), "recovery-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(root)
	findings, e := s.HealthDatabase(ctx, false, filepath.Join(root, "unused.sqlite"))
	if e != nil {
		return e
	}
	for _, f := range findings {
		if f.Status != "normal" {
			return model.ErrHealthRecovery
		}
	}
	if e = s.healthWrite(ctx, func(c *sql.Conn) error {
		_, err := c.ExecContext(ctx, "UPDATE jobs SET state='paused',generation=generation+1,error_code='health_recovery',updated_at=unixepoch() WHERE state IN('queued','running')")
		return err
	}); e != nil {
		return e
	}
	c, e := s.Write.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if e = s.clearHealthPoison(ctx, c, ""); e != nil {
		return e
	}
	result, e := c.ExecContext(ctx, "UPDATE health_state SET phase='idle',revision=revision+1,updated_at=unixepoch() WHERE singleton=1 AND repair_id IS NULL AND phase='recovery_required' AND revision=?", revision)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrHealthStale
	}
	return nil
}

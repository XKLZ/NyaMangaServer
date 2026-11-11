package store

import (
	"context"
	"database/sql"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
)

// This function modifies only the independent validated staging database. It
// does not invoke Store.Open, migrations, recovery, scans or task orchestration.
func PrepareInstanceDatabase(ctx context.Context, path, id, request string, roots []model.InstanceRebase, stats []model.InstanceStat) error {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "rw")
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	c, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); e != nil {
		return e
	}
	if _, e = c.ExecContext(ctx, "UPDATE temp.health_permit SET value=1 WHERE singleton=1"); e != nil {
		return e
	}
	tx, e := c.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	statements := []string{
		"DELETE FROM sessions",
		"UPDATE jobs SET state='paused',generation=generation+1,error_code='instance_restored',updated_at=unixepoch() WHERE state IN('queued','running')",
		"UPDATE enhancement_settings SET runtime_armed=0 WHERE runtime_armed<>0",
		"UPDATE health_state SET phase='idle',repair_id=NULL,revision=revision+1,updated_at=unixepoch() WHERE singleton=1 AND repair_id IS NULL",
	}
	for _, statement := range statements {
		if _, e = tx.ExecContext(ctx, statement); e != nil {
			return e
		}
	}
	var phase string
	if e = tx.QueryRowContext(ctx, "SELECT phase FROM health_state WHERE singleton=1").Scan(&phase); e != nil {
		return e
	}
	if phase != "idle" {
		return model.ErrHealthMaintenance
	}
	// Temporary unique keys avoid swap collisions when source/target names cross.
	for _, r := range roots {
		if _, e = tx.ExecContext(ctx, "UPDATE libraries SET root_key=? WHERE id=?", "restore-pending-"+r.LibraryID, r.LibraryID); e != nil {
			return e
		}
	}
	for _, r := range roots {
		key := "restore-unlinked-" + r.LibraryID
		linked := 0
		if r.TargetRootKey != nil {
			key = *r.TargetRootKey
			linked = 1
		}
		if _, e = tx.ExecContext(ctx, "UPDATE libraries SET root_key=?,root_identity=? WHERE id=?", key, r.Identity, r.LibraryID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO instance_root_links(library_id,source_root_key,target_root_key,linked) VALUES(?,?,?,?) ON CONFLICT(library_id) DO UPDATE SET source_root_key=excluded.source_root_key,target_root_key=excluded.target_root_key,linked=excluded.linked,revision=revision+1", r.LibraryID, r.SourceRootKey, r.TargetRootKey, linked); e != nil {
			return e
		}
	}
	for _, s := range stats {
		if _, e = tx.ExecContext(ctx, "UPDATE files SET mtime_ns=? WHERE edition_id=?", s.Mtime, s.EditionID); e != nil {
			return e
		}

	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO instance_operations(id,kind,request_key,state,phase,created_at,updated_at) VALUES(?,'restore',?,'completed','activated',unixepoch(),unixepoch()) ON CONFLICT(id) DO UPDATE SET state='completed',phase='activated',revision=revision+1,updated_at=unixepoch()", id, request); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE instance_state SET owner=NULL,background_hold=1,write_epoch=0,revision=revision+1 WHERE singleton=1"); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if _, e = c.ExecContext(ctx, "UPDATE temp.health_permit SET value=0 WHERE singleton=1"); e != nil {
		return e
	}
	return nil
}

func InstanceOfflineEpoch(ctx context.Context, path string) (int64, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return 0, e
	}
	defer db.Close()
	var epoch int64
	e = db.QueryRowContext(ctx, "SELECT write_epoch FROM instance_state WHERE singleton=1").Scan(&epoch)
	return epoch, e
}

func CheckInstanceRollbackIdentity(ctx context.Context, path, id string) error {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	var valid bool
	e = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM instance_operations WHERE id=? AND kind='restore' AND state='completed') AND (SELECT write_epoch=0 FROM instance_state WHERE singleton=1)", id).Scan(&valid)
	if e != nil {
		return e
	}
	if !valid {
		return model.ErrConflict
	}
	return nil
}

func CheckInstanceOfflineTarget(ctx context.Context, dir string) error {
	return CheckInstanceOfflineReplacement(ctx, dir, false)
}
func CheckInstanceOfflineReplacement(ctx context.Context, dir string, allowUnhealthy bool) error {
	for _, marker := range []string{"health-write-unsafe", "reset-in-progress"} {
		if _, e := os.Lstat(filepath.Join(dir, marker)); e == nil {
			if marker == "health-write-unsafe" && allowUnhealthy {
				continue
			}
			return model.ErrInstanceRecovery
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	path := filepath.Join(dir, "app.sqlite")
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	var phase string
	e = db.QueryRowContext(ctx, "SELECT phase FROM health_state WHERE singleton=1").Scan(&phase)
	if e == nil && phase != "idle" && (!allowUnhealthy || phase == "instance_maintenance") {
		return model.ErrHealthMaintenance
	}
	// A corrupt or pre-health DB is an explicit offline recovery target. Its raw
	// main/WAL/SHM bytes are preserved before activation; no salvage is attempted.
	return nil
}

func (s *Store) ReleaseInstanceHold(ctx context.Context, expected int64) (model.InstanceState, error) {
	r, e := s.Write.ExecContext(ctx, "UPDATE instance_state SET background_hold=0,revision=revision+1 WHERE singleton=1 AND revision=? AND owner IS NULL", expected)
	if e != nil {
		return model.InstanceState{}, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return model.InstanceState{}, e
	}
	if n != 1 {
		return model.InstanceState{}, model.ErrConflict
	}
	return s.InstanceState(ctx)
}

func FinishInstanceRollback(ctx context.Context, dir, id string) error {
	path := filepath.Join(dir, "app.sqlite")
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		return nil
	} else if e != nil {
		return e
	}
	if _, e := os.Lstat(filepath.Join(dir, "health-write-unsafe")); e == nil {
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	read, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	var application, baseline string
	if e = read.QueryRowContext(ctx, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&application, &baseline); e != nil || application != ApplicationID || baseline != BaselineID {
		read.Close()
		return nil
	}
	var instances, health int
	if e = read.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='instance_state'").Scan(&instances); e != nil {
		read.Close()
		return nil
	}
	if e = read.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name='health_state'").Scan(&health); e != nil {
		read.Close()
		return nil
	}
	var owner *string
	phase := "idle"
	if instances != 0 {
		if e = read.QueryRowContext(ctx, "SELECT owner FROM instance_state WHERE singleton=1").Scan(&owner); e != nil {
			read.Close()
			return nil
		}
	}
	if health != 0 {
		if e = read.QueryRowContext(ctx, "SELECT phase FROM health_state WHERE singleton=1").Scan(&phase); e != nil {
			read.Close()
			return nil
		}
	}
	read.Close()
	if owner != nil && *owner != id {
		return model.ErrInstanceRecovery
	}
	if owner == nil && phase != "idle" {
		return nil
	} // prior unhealthy target stays prior unhealthy
	q.Set("mode", "rw")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	c, e := db.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if health != 0 {
		if _, e = c.ExecContext(ctx, "UPDATE temp.health_permit SET value=1 WHERE singleton=1"); e != nil {
			return e
		}
	}
	tx, e := c.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	// Restoring a prior target must not resurrect any previously invalidated login.
	if _, e = tx.ExecContext(ctx, "DELETE FROM sessions"); e != nil {
		return e
	}
	if owner != nil {
		if _, e = tx.ExecContext(ctx, "UPDATE instance_state SET owner=NULL,revision=revision+1 WHERE singleton=1 AND owner=?", id); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE health_state SET phase='idle',revision=revision+1,updated_at=unixepoch() WHERE singleton=1 AND phase='instance_maintenance' AND repair_id IS NULL"); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if health != 0 {
		_, e = c.ExecContext(ctx, "UPDATE temp.health_permit SET value=0 WHERE singleton=1")
	}
	return e
}

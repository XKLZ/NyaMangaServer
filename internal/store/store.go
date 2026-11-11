// Package store owns parameterized SQL and atomic authentication writes.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const ApplicationID = "nyamangaserver"
const BaselineID = "foundation-v1"

var ErrInitialized = model.ErrInitialized
var ErrConflict = model.ErrConflict

type Store struct {
	healthIO              sync.RWMutex
	healthFinishCommitted func() // anonymous deterministic completion-window fault hook
	healthDir             string
	healthMigrations      fs.FS
	galleryPublish        sync.RWMutex
	Write                 *writeDatabase
	Read                  *sql.DB
	Version               int
}
type Account = model.Account
type Session = model.Session

func Open(ctx context.Context, dir string, files fs.FS) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return nil, fmt.Errorf("protect application data directory: %w", e)
	}
	filename := filepath.Join(dir, "app.sqlite")
	u := url.URL{Scheme: "file", Path: filename}
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	w, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	w.SetMaxOpenConns(1)
	s := &Store{Write: &writeDatabase{db: w}, healthDir: dir, healthMigrations: files}
	fail := func(e error) (*Store, error) { w.Close(); return nil, e }
	if e = s.validateIdentity(ctx); e != nil {
		return fail(e)
	}
	var mode string
	if e = scopeRow(ctx, w, "PRAGMA journal_mode=WAL").Scan(&mode); e != nil {
		return fail(e)
	}
	if mode != "wal" {
		return fail(errors.New("WAL unavailable"))
	}
	if e = os.Chmod(filename, 0600); e != nil {
		return fail(e)
	}
	if e = s.migrate(ctx, files, dir); e != nil {
		return fail(e)
	}
	q.Del("_txlock")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	r, e := sql.Open("sqlite", u.String())
	if e != nil {
		return fail(e)
	}
	r.SetMaxOpenConns(4)
	r.SetMaxIdleConns(4)
	s.Read = r
	if e = r.PingContext(ctx); e != nil {
		s.Close()
		return nil, e
	}
	s.Write.version = s.Version
	if e = s.installAuthorization(ctx); e != nil {
		s.Close()
		return nil, e
	}
	if e = s.installHealthBarrier(ctx); e != nil {
		s.Close()
		return nil, e
	}
	state, err := s.HealthState(ctx)
	if err != nil {
		s.Close()
		return nil, err
	}
	if state.Phase != "idle" {
		return s, nil
	}
	instance, err := s.InstanceState(ctx)
	if err != nil {
		s.Close()
		return nil, err
	}
	if instance.BackgroundHold {
		return s, nil
	}
	if s.Version >= 7 {
		if e = s.PrepareProperties(ctx); e != nil {
			s.Close()
			return nil, fmt.Errorf("prepare properties: %w", e)
		}
	}
	if s.Version >= 17 {
		if e = s.PrepareTagRelations(ctx); e != nil {
			s.Close()
			return nil, fmt.Errorf("prepare tag relations: %w", e)
		}
	}
	return s, nil
}
func (s *Store) Close() error {
	var r error
	if s.Read != nil {
		r = s.Read.Close()
	}
	return errors.Join(r, s.Write.Close())
}
func (s *Store) migrate(ctx context.Context, files fs.FS, dir string) (resultErr error) {
	names, e := fs.Glob(files, "*.sql")
	if e != nil {
		return e
	}
	sort.Strings(names)
	if len(names) == 0 {
		return errors.New("no migrations")
	}
	var ledger, tables int
	if e = scopeRow(ctx, s.Write, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'").Scan(&ledger); e != nil {
		return e
	}
	if e = scopeRow(ctx, s.Write, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); e != nil {
		return e
	}
	if ledger == 0 && tables != 0 {
		return errors.New("unrecognized database schema")
	}
	applied := map[int]string{}
	if ledger != 0 {
		rows, e := scopeRows(ctx, s.Write, "SELECT version,checksum FROM schema_migrations ORDER BY version")
		if e != nil {
			return e
		}
		for rows.Next() {
			var v int
			var h string
			if e = rows.Scan(&v, &h); e != nil {
				rows.Close()
				return e
			}
			applied[v] = h
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	type migration struct {
		v         int
		sql, hash string
	}
	var pending []migration
	known := map[int]bool{}
	for _, n := range names {
		prefix := strings.SplitN(n, "_", 2)[0]
		v, e := strconv.Atoi(prefix)
		if e != nil || v < 1 || known[v] {
			return errors.New("invalid migration version")
		}
		known[v] = true
		b, e := fs.ReadFile(files, n)
		if e != nil {
			return e
		}
		h := fmt.Sprintf("%x", sha256.Sum256(b))
		if old, ok := applied[v]; ok {
			if old != h {
				return fmt.Errorf("migration %d checksum mismatch", v)
			}
		} else {
			if v <= s.Version {
				return errors.New("migration gap")
			}
			pending = append(pending, migration{v, string(b), h})
		}
		if v > s.Version {
			s.Version = v
		}
	}
	for v := range applied {
		if !known[v] {
			return errors.New("database schema newer than application")
		}
	}
	// Applied migrations must be a contiguous prefix of the ordered migration set.
	missing := false
	for _, n := range names {
		v, _ := strconv.Atoi(strings.SplitN(n, "_", 2)[0])
		_, ok := applied[v]
		if !ok {
			missing = true
		} else if missing {
			return errors.New("migration history gap")
		}
	}
	if len(pending) == 0 {
		if s.Version >= 7 {
			return finishWorkMigrationReport(dir)
		}
		return nil
	}
	if len(applied) > 0 {
		backupDir := filepath.Join(dir, "upgrade-snapshots")
		if e = os.MkdirAll(backupDir, 0700); e != nil {
			return e
		}
		f, e := os.CreateTemp(backupDir, "before-upgrade-*.sqlite")
		if e != nil {
			return e
		}
		name := f.Name()
		if e = f.Close(); e != nil {
			return e
		}
		if _, e = s.Write.ExecContext(ctx, "VACUUM INTO ?", name); e != nil {
			os.Remove(name)
			return fmt.Errorf("upgrade snapshot: %w", e)
		}
	}
	// Pin the exclusive migration connection throughout pragma/transaction/restore.
	migrationConn, e := s.Write.Conn(ctx)
	if e != nil {
		return e
	}
	defer migrationConn.Close()
	rebuildAccounts := false
	for _, m := range pending {
		if m.v == 25 {
			rebuildAccounts = true
		}
	}
	if rebuildAccounts {
		if _, e = migrationConn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); e != nil {
			return e
		}
		defer func() {
			_, restoreErr := migrationConn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
			var enabled int
			if restoreErr == nil {
				restoreErr = scopeRow(context.Background(), migrationConn, "PRAGMA foreign_keys").Scan(&enabled)
				if restoreErr == nil && enabled != 1 {
					restoreErr = errors.New("foreign key enforcement not restored")
				}
			}
			resultErr = errors.Join(resultErr, restoreErr)
		}()
	}
	tx, e := migrationConn.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at INTEGER NOT NULL)"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS application_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),application TEXT NOT NULL,baseline TEXT NOT NULL)"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO application_identity VALUES(1,?,?)", ApplicationID, BaselineID); e != nil {
		return e
	}
	var workReport *workMigrationReport
	for _, m := range pending {
		if m.v == 7 {
			workReport, e = prepareWorkMigrationReport(ctx, tx)
			if e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, m.sql); e != nil {
			return fmt.Errorf("migration %d failed: %w", m.v, e)
		}
		// applied is the ledger observed BEFORE executing any migration. A fresh
		// database applying 001..019 in one transaction must keep default-off.
		if m.v == 19 && len(applied) > 0 {
			if _, e = tx.ExecContext(ctx, "UPDATE enhancement_settings SET enabled=1,runtime_armed=1 WHERE feature IN('work_recommendations','series_recommendations')"); e != nil {
				return e
			}
		}
		if m.v == 15 {
			if e = migrateSeriesFeedback(ctx, tx); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO schema_migrations VALUES(?,?,?)", m.v, m.hash, time.Now().Unix()); e != nil {
			return e
		}
	}
	if s.Version >= 25 {
		if e = validateAccountIntegrity(ctx, tx); e != nil {
			return e
		}
	}
	if workReport != nil {
		if e = checkOrganization(ctx, tx); e != nil {
			return e
		}
	}
	{
		var issues int
		rows, x := scopeRows(ctx, tx, "PRAGMA foreign_key_check")
		if x != nil {
			return x
		}
		for rows.Next() {
			issues++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if issues != 0 {
			return errors.New("migration foreign key validation failed")
		}
	}
	if workReport != nil {
		if e = writeWorkMigrationReport(dir, workReport); e != nil {
			return fmt.Errorf("write private migration report: %w", e)
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if workReport != nil {
		return finishWorkMigrationReport(dir)
	}
	return nil
}
func (s *Store) Ping(ctx context.Context) error { return s.Read.PingContext(ctx) }
func (s *Store) SchemaVersion() int             { return s.Version }
func (s *Store) Baseline() string               { return BaselineID }

// validateIdentity runs before any schema or journal changes. A legacy database
// cannot be mistaken for a fresh instance, even when its version numbers match.
func (s *Store) validateIdentity(ctx context.Context) error {
	var tables, identity int
	if e := scopeRow(ctx, s.Write, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); e != nil {
		return e
	}
	if tables == 0 {
		return nil
	}
	if e := scopeRow(ctx, s.Write, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='application_identity'").Scan(&identity); e != nil {
		return e
	}
	if identity != 1 {
		return errors.New("legacy or unknown database baseline: explicit development reset required")
	}
	var application, baseline string
	if e := scopeRow(ctx, s.Write, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&application, &baseline); e != nil {
		return fmt.Errorf("invalid database identity: %w", e)
	}
	var count int
	if e := scopeRow(ctx, s.Write, "SELECT count(*) FROM application_identity").Scan(&count); e != nil {
		return e
	}
	if count != 1 || application != ApplicationID || baseline != BaselineID {
		return errors.New("unknown application or database baseline")
	}
	var ledger int
	if e := scopeRow(ctx, s.Write, "SELECT count(*) FROM schema_migrations").Scan(&ledger); e != nil {
		return e
	}
	if ledger == 0 {
		return errors.New("missing migration history for existing baseline")
	}
	return validateAccountIntegrity(ctx, s.Write)
}

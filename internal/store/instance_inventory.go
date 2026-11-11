package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The package DB must be a standalone snapshot: no WAL, migrations, recovery or
// application backfills are ever performed while inspecting untrusted input.
func InspectInstanceDatabase(ctx context.Context, path string, files fs.FS) (model.InstanceInventory, error) {
	out := model.InstanceInventory{Counts: map[string]int64{}, Required: []model.InstanceFile{}, Roots: []model.InstanceRoot{}}
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return out, model.ErrInstancePackage
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Set("immutable", "1")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "trusted_schema(0)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return out, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var app, base string
	if e = db.QueryRowContext(ctx, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&app, &base); e != nil {
		return out, model.ErrInstancePackage
	}
	if app != ApplicationID || base != BaselineID {
		return out, model.ErrInstancePackage
	}
	names, e := fs.Glob(files, "*.sql")
	if e != nil {
		return out, e
	}
	sort.Strings(names)
	rows, e := db.QueryContext(ctx, "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if e != nil {
		return out, e
	}
	ledger := sha256.New()
	count := 0
	for rows.Next() {
		var version int
		var hash string
		if e = rows.Scan(&version, &hash); e != nil {
			rows.Close()
			return out, e
		}
		if count >= len(names) {
			rows.Close()
			return out, model.ErrInstanceBuild
		}
		name := names[count]
		v, er := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		raw, read := fs.ReadFile(files, name)
		sum := sha256.Sum256(raw)
		if er != nil || read != nil || v != version || hex.EncodeToString(sum[:]) != hash {
			rows.Close()
			return out, model.ErrInstanceBuild
		}
		fmt.Fprintf(ledger, "%d:%s\n", version, hash)
		count++
		out.Version = version
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if count != len(names) {
		return out, model.ErrInstanceBuild
	}
	out.Ledger = hex.EncodeToString(ledger.Sum(nil))
	if out.Version >= 25 {
		if e = validateAccountIntegrity(ctx, db); e != nil {
			return out, model.ErrInstancePackage
		}
	}
	var check string
	if e = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
		return out, model.ErrInstancePackage
	}
	rows, e = db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return out, e
	}
	bad := rows.Next()
	e = rows.Err()
	rows.Close()
	if bad || e != nil {
		return out, model.ErrInstancePackage
	}
	rows, e = db.QueryContext(ctx, "SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type,name")
	if e != nil {
		return out, e
	}
	schema := sha256.New()
	tables := []string{}
	for rows.Next() {
		var kind, name, table, statement string
		if e = rows.Scan(&kind, &name, &table, &statement); e != nil {
			rows.Close()
			return out, e
		}
		fmt.Fprintf(schema, "%s\x00%s\x00%s\x00%s\n", kind, name, table, statement)
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	out.Schema = hex.EncodeToString(schema.Sum(nil))
	for _, table := range tables {
		var n int64
		if e = db.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*) FROM %q", table)).Scan(&n); e != nil {
			return out, e
		}
		out.Counts[table] = n
	}
	rows, e = db.QueryContext(ctx, "SELECT storage_key,size,digest FROM assets UNION ALL SELECT key,size,digest FROM health_artifacts WHERE pinned=1")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var f model.InstanceFile
		if e = rows.Scan(&f.Key, &f.Size, &f.SHA256); e != nil {
			rows.Close()
			return out, e
		}
		out.Required = append(out.Required, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = db.QueryContext(ctx, "SELECT id,root_key,root_identity,display_name FROM libraries ORDER BY id")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var r model.InstanceRoot
		if e = rows.Scan(&r.LibraryID, &r.RootKey, &r.Identity, &r.Name); e != nil {
			rows.Close()
			return out, e
		}
		out.Roots = append(out.Roots, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	rows, e = db.QueryContext(ctx, "SELECT edition_id,library_id,relative_path,size,mtime_ns,file_state,content_revision FROM files ORDER BY library_id,relative_path")
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var s model.InstanceSource
		if e = rows.Scan(&s.EditionID, &s.LibraryID, &s.Path, &s.Size, &s.Mtime, &s.State, &s.Revision); e != nil {
			rows.Close()
			return out, e
		}
		out.Sources = append(out.Sources, s)
	}
	e = rows.Err()
	rows.Close()
	return out, e
}

// Compare schema to an independently constructed anonymous database, never to
// SQL supplied in the archive. A forged ledger cannot install extra triggers.
func ValidateInstanceSchema(ctx context.Context, path string, files fs.FS) (model.InstanceInventory, error) {
	source, e := InspectInstanceDatabase(ctx, path, files)
	if e != nil {
		return source, e
	}
	dir, e := os.MkdirTemp(filepath.Dir(path), "canonical-")
	if e != nil {
		return source, e
	}
	defer os.RemoveAll(dir)
	canonical, e := Open(model.WithoutActor(ctx), dir, files)
	if e != nil {
		return source, e
	}
	if e = canonical.Close(); e != nil {
		return source, e
	}
	expected, e := InspectInstanceDatabase(ctx, filepath.Join(dir, "app.sqlite"), files)
	if e != nil {
		return source, e
	}
	if source.Schema != expected.Schema {
		return source, model.ErrInstancePackage
	}
	return source, nil
}

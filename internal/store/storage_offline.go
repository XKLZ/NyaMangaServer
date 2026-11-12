package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"path/filepath"
	"strconv"
	"strings"
)

// Read only: no migrations, startup reconciliation, authentication or tasks.
// Caller must hold the normal instance lock.
func MigrationOriginalAssets(ctx context.Context, dir string, files fs.FS) ([]model.StorageAsset, error) {
	path := filepath.Join(dir, "app.sqlite")
	if e := appdata.NoSymlinks(path); e != nil {
		return nil, e
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return nil, e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var application, baseline string
	if e = tx.QueryRowContext(ctx, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&application, &baseline); e != nil {
		return nil, e
	}
	if application != ApplicationID || baseline != BaselineID {
		return nil, model.ErrInput
	}
	if e = validateOfflineMaintenanceLedger(ctx, tx, files); e != nil {
		return nil, e
	}

	rows, e := tx.QueryContext(ctx, "SELECT id,storage_key,digest,size,("+assetReferencedSQL+") FROM assets a WHERE storage_key LIKE 'assets/treec-originals/%' ORDER BY storage_key")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.StorageAsset{}
	for rows.Next() {
		var a model.StorageAsset
		if e = rows.Scan(&a.ID, &a.Key, &a.Digest, &a.Size, &a.Referenced); e != nil {
			return nil, e
		}
		out = append(out, a)
		if len(out) > 100000 {
			return nil, model.ErrCapacity
		}
	}
	return out, rows.Err()
}

// Offline maintenance requires the exact ledger embedded by its caller, not
// merely a sufficiently recent maximum version. Never migrate while checking.
func validateOfflineMaintenanceLedger(ctx context.Context, tx *sql.Tx, files fs.FS) error {
	names, e := fs.Glob(files, "*.sql")
	// The maintenance queries require the tables introduced through 027.
	if e != nil || len(names) < 27 {
		return model.ErrInput
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); e != nil {
		return e
	}
	if count != len(names) {
		return model.ErrInput
	}
	for i, name := range names {
		version, e := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if e != nil || version != i+1 {
			return model.ErrInput
		}
		raw, e := fs.ReadFile(files, name)
		if e != nil {
			return e
		}
		var hash string
		if e = tx.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=?", version).Scan(&hash); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return model.ErrInput
			}
			return e
		}
		if hash != fmt.Sprintf("%x", sha256.Sum256(raw)) {
			return model.ErrInput
		}
	}
	return nil
}

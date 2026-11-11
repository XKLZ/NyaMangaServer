package store

import (
	"context"
	"database/sql"
	"net/url"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"path/filepath"
)

// Read only: no migrations, startup reconciliation, authentication or tasks.
// Caller must hold the normal instance lock.
func MigrationOriginalAssets(ctx context.Context, dir string) ([]model.StorageAsset, error) {
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
	var application, baseline string
	if e = db.QueryRowContext(ctx, "SELECT application,baseline FROM application_identity WHERE singleton=1").Scan(&application, &baseline); e != nil {
		return nil, e
	}
	if application != ApplicationID || baseline != BaselineID {
		return nil, model.ErrInput
	}
	var version int
	if e = db.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version); e != nil {
		return nil, e
	}
	if version != 27 {
		return nil, model.ErrInput
	}

	rows, e := db.QueryContext(ctx, "SELECT id,storage_key,digest,size,("+assetReferencedSQL+") FROM assets a WHERE storage_key LIKE 'assets/treec-originals/%' ORDER BY storage_key")
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

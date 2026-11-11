package store

import (
	"context"
	"database/sql"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

// The connection-local index is derived, never persisted in the business schema.
// Local writes invalidate it through TEMP triggers; commits on other connections
// invalidate it through data_version. Rebuild and candidate lookup both occur
// under the same writer transaction, so a new reference cannot race an unlink.
func storageCandidateCacheKeys(ctx context.Context, tx *sql.Tx, files []model.StorageDeletion) (map[string]bool, error) {
	keys := []string{}
	args := []any{}
	for _, f := range files {
		if f.OnlyRedundant && (f.Kind == "covers" || f.Kind == "tag_images") {
			keys = append(keys, "?")
			args = append(args, f.Key)
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	for _, stmt := range []string{
		"CREATE TEMP TABLE IF NOT EXISTS storage_cache_guard(key TEXT PRIMARY KEY)",
		"CREATE TEMP TABLE IF NOT EXISTS storage_cache_generation(singleton INTEGER PRIMARY KEY,dirty INTEGER NOT NULL,version INTEGER NOT NULL)",
		"INSERT OR IGNORE INTO storage_cache_generation VALUES(1,1,-1)",
	} {
		if _, e := tx.ExecContext(ctx, stmt); e != nil {
			return nil, e
		}
	}
	for _, table := range []string{"files", "cover_selections", "tag_images", "assets"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			stmt := fmt.Sprintf("CREATE TEMP TRIGGER IF NOT EXISTS storage_guard_%s_%s AFTER %s ON main.%s BEGIN UPDATE storage_cache_generation SET dirty=1; END", table, op, op, table)
			if _, e := tx.ExecContext(ctx, stmt); e != nil {
				return nil, e
			}
		}
	}
	var dirty int
	var old, current int64
	if e := tx.QueryRowContext(ctx, "PRAGMA data_version").Scan(&current); e != nil {
		return nil, e
	}
	if e := tx.QueryRowContext(ctx, "SELECT dirty,version FROM storage_cache_generation WHERE singleton=1").Scan(&dirty, &old); e != nil {
		return nil, e
	}
	if dirty != 0 || old != current {
		all, e := storageCacheKeys(ctx, tx, "")
		if e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM storage_cache_guard"); e != nil {
			return nil, e
		}
		stmt, e := tx.PrepareContext(ctx, "INSERT INTO storage_cache_guard VALUES(?)")
		if e != nil {
			return nil, e
		}
		for key := range all {
			if _, e = stmt.ExecContext(ctx, key); e != nil {
				stmt.Close()
				return nil, e
			}
		}
		if e = stmt.Close(); e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE storage_cache_generation SET dirty=0,version=? WHERE singleton=1", current); e != nil {
			return nil, e
		}
	}
	rows, e := tx.QueryContext(ctx, "SELECT key FROM storage_cache_guard WHERE key IN ("+strings.Join(keys, ",")+")", args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var key string
		if e = rows.Scan(&key); e != nil {
			return nil, e
		}
		out[key] = true
	}
	return out, rows.Err()
}

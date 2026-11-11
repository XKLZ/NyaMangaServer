package store

import (
	"context"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
func stringArgs(ids []string) []any {
	v := make([]any, len(ids))
	for i, id := range ids {
		v[i] = id
	}
	return v
}
func catalogRevision(ctx context.Context, q queryer) (string, error) {
	var n int64
	e := scopeRow(ctx, q, "SELECT revision FROM catalog_state WHERE singleton=1").Scan(&n)
	rev := fmt.Sprintf("cat-%d", n)
	if a, ok := model.Actor(ctx); ok && e == nil {
		var epoch int64
		e = q.QueryRowContext(ctx, "SELECT acl_revision FROM account_profiles WHERE account_id=?", a.ID).Scan(&epoch)
		rev += fmt.Sprintf(":acl:%s:%d", a.ID, epoch)
	}
	return rev, e
}

func checkOrganization(ctx context.Context, q queryer) error {
	for _, sql := range []string{
		`SELECT count(*) FROM works w WHERE (SELECT count(*) FROM editions e WHERE e.work_id=w.id) NOT BETWEEN 1 AND 100 OR NOT EXISTS(SELECT 1 FROM editions e WHERE e.id=w.preferred_edition_id AND e.work_id=w.id AND e.library_id=w.library_id)`,
		`SELECT count(*) FROM editions e LEFT JOIN files f ON f.edition_id=e.id LEFT JOIN works w ON w.id=e.work_id WHERE f.edition_id IS NULL OR f.library_id!=e.library_id OR w.library_id!=e.library_id`,
		`SELECT count(*) FROM files f LEFT JOIN editions e ON e.id=f.edition_id WHERE e.id IS NULL`,
		`SELECT count(*) FROM series_members m JOIN series s ON s.id=m.series_id JOIN works w ON w.id=m.work_id WHERE s.library_id!=w.library_id`,
		`SELECT count(*) FROM (SELECT series_id,count(*) n,min(position) lo,max(position) hi FROM series_members GROUP BY series_id HAVING n>1000 OR lo!=1 OR hi!=n)`,
	} {
		var n int
		if e := scopeRow(ctx, q, sql).Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return fmt.Errorf("work/edition/file invariant failed")
		}
	}
	return nil
}

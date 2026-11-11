package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
)

func importStageFeature(stage string) string {
	if stage == "versions" {
		return model.EnhancementWork
	}
	if stage == "series" {
		return model.EnhancementSeries
	}
	return ""
}
func importEnhancementFlags(ctx context.Context, q queryer, batch string) (map[string]bool, error) {
	out := map[string]bool{"versions": true, "series": true}
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='import_enhancements'").Scan(&exists); e != nil {
		return out, e
	}
	if exists == 0 {
		return out, nil
	}
	var versions, series bool
	e := scopeRow(ctx, q, "SELECT versions_enabled,series_enabled FROM import_enhancements WHERE batch_id=?", batch).Scan(&versions, &series)
	if e == sql.ErrNoRows {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out["versions"], out["series"] = versions, series
	return out, nil
}
func (s *Store) PauseImportForEnhancement(ctx context.Context, id, stage string) error {
	_, e := s.Write.ExecContext(ctx, "UPDATE import_batches SET state='paused',error_code='enhancement_disabled',revision=revision+1,updated_at=unixepoch() WHERE id=? AND stage=? AND state IN('processing','review')", id, stage)
	return e
}
func enrichmentRevision(ctx context.Context, q queryer) (int64, error) {
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='enhancement_revision'").Scan(&exists); e != nil {
		return 0, e
	}
	if exists == 0 {
		return 0, nil
	}
	var rev int64
	e := scopeRow(ctx, q, "SELECT revision FROM enhancement_revision WHERE singleton=1").Scan(&rev)
	return rev, e
}

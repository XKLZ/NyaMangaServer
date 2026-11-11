package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	pilot "nyamangaserver.local/server/internal/recommendationpilot"
	"time"
)

func snapshotEmpty(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) error {
	// An initialized account is permitted, but no previous business data is merged.
	for _, table := range []string{"libraries", "editions", "works", "series", "jobs", "external_metadata", "assets", "saved_searches", "tags"} {
		var n int
		if e := q.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil {
			return e
		}
		if n != 0 {
			return errors.New("recommendation pilot requires an empty business database")
		}
	}
	return nil
}
func (s *Store) RecommendationSnapshotEmpty(ctx context.Context) error {
	return snapshotEmpty(ctx, s.Read)
}
func (s *Store) ImportRecommendationSnapshot(ctx context.Context, records []pilot.Record, inputHash string) (err error) {
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err = snapshotEmpty(ctx, tx); err != nil {
		return
	}
	if len(records) == 0 {
		return model.ErrInput
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision,last_error) VALUES(?,?,'TreeC 隔离验证快照（无源文件）','utf8',?,1,'treec_validation_snapshot_no_source')`, pilot.LibraryID, pilot.LibraryID, "snapshot:"+inputHash)
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, r := range records {
		b := model.Edition{ID: r.EditionID, LibraryID: pilot.LibraryID, WorkID: r.WorkID, File: model.File{Path: "snapshot/" + r.SourceID + ".unavailable", FilenameTitle: r.Original.Title, ContentRevision: 1, RepresentativeAlgorithm: "unavailable-treec-snapshot", PageCount: r.Original.Pages, State: "missing", Error: "treec_validation_snapshot_no_source", IndexedAt: now}}
		if err = saveEditionIndex(ctx, tx, b); err != nil {
			return
		}
		normalized, _ := json.Marshal(r.Metadata)
		raw, _ := json.Marshal(struct {
			Ownership string           `json:"ownership"`
			InputHash string           `json:"input_sha256"`
			Original  pilot.LegacyBook `json:"legacy_values"`
		}{"legacy_unknown", r.InputHash, r.Original})
		sourceID := "pilot-source-" + r.SourceID
		if _, err = tx.ExecContext(ctx, "INSERT INTO external_metadata VALUES(?,'treec-validation',?,?,?,1,?,1)", sourceID, r.SourceID, string(normalized), string(raw), now); err != nil {
			return
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO edition_sources VALUES(?,?,'','legacy_snapshot_unknown_ownership',1)", r.EditionID, sourceID); err != nil {
			return
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO edition_metadata_state(edition_id) VALUES(?)", r.EditionID); err != nil {
			return
		}
		if r.Asset != nil {
			a := r.Asset
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO assets(id,storage_key,digest,size,created_at) VALUES(?,?,?,?,?)", a.ID, a.Key, a.Digest, a.Size, now); err != nil {
				return
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO cover_selections(edition_id,mode,content_revision,asset_id,revision) VALUES(?,'asset',1,?,1)", r.EditionID, a.ID); err != nil {
				return
			}
			if _, err = tx.ExecContext(ctx, "INSERT INTO recommendation_materials VALUES(?,?,'treec-validation',?,?,?)", r.EditionID, a.ID, a.Digest, r.SourceID, r.InputHash); err != nil {
				return
			}
		}
		if err = reproject(ctx, tx, r.EditionID); err != nil {
			return fmt.Errorf("project validation snapshot: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return
	}
	if bad {
		return errors.New("snapshot foreign key violation")
	}
	return tx.Commit()
}

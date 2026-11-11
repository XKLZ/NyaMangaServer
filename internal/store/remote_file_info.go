package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"sort"
)

func (s *Store) BackfillRemoteFileInfo(ctx context.Context, in model.RemoteFileInfoBackfill) (model.RemoteFileInfoReport, error) {
	out := model.RemoteFileInfoReport{DryRun: in.DryRun, Items: []model.RemoteFileInfoRecord{}}
	if e := s.RequireAdministrator(ctx); e != nil {
		return out, e
	}
	if len(in.Expected) == 0 || len(in.Expected) > 1000 {
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, ok := model.Actor(ctx); ok {
		if _, e = administrator(ctx, tx); e != nil {
			return out, e
		}
	}
	ids := make([]string, 0, len(in.Expected))
	for id, rev := range in.Expected {
		if id == "" || len(id) > 128 || rev < 0 || !in.DryRun && rev == 0 {
			return out, model.ErrInput
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var providerName, external, raw, normalized string
		var revision int64
		e = tx.QueryRowContext(ctx, "SELECT provider,external_id,raw_json,normalized_json,revision FROM external_metadata WHERE id=?", id).Scan(&providerName, &external, &raw, &normalized, &revision)
		if e == sql.ErrNoRows {
			return out, model.ErrNotFound
		}
		if e != nil {
			return out, e
		}
		if expected := in.Expected[id]; expected != 0 && expected != revision {
			return out, model.ErrConflict
		}
		record := model.RemoteFileInfoRecord{ID: id, Revision: revision, Status: "already_present"}
		var fields model.Metadata
		if json.Unmarshal([]byte(normalized), &fields) != nil {
			return out, model.ErrInput
		}
		record.RemotePageCount = fields.RemotePageCount
		record.RemoteSizeBytes = fields.RemoteSizeBytes
		if providerName != "ehentai" {
			record.Status = "unsupported_provider"
			out.Items = append(out.Items, record)
			continue
		}
		pages, size, parseErr := provider.ParseRemoteFileInfo([]byte(raw), external)
		if parseErr != nil {
			record.Status = "raw_invalid"
			out.Items = append(out.Items, record)
			continue
		}
		if pages == nil && size == nil {
			record.Status = "raw_unavailable"
			out.Items = append(out.Items, record)
			continue
		}
		if fields.RemotePageCount != nil && pages != nil && *fields.RemotePageCount != *pages || fields.RemoteSizeBytes != nil && size != nil && *fields.RemoteSizeBytes != *size {
			record.Status = "raw_conflict"
			out.Items = append(out.Items, record)
			continue
		}
		changed := false
		if fields.RemotePageCount == nil && pages != nil {
			record.RemotePageCount = pages
			changed = true
		}
		if fields.RemoteSizeBytes == nil && size != nil {
			record.RemoteSizeBytes = size
			changed = true
		}
		if changed {
			if revision == math.MaxInt64 {
				return out, model.ErrCapacity
			}
			record.Status = "fillable"
			if !in.DryRun {
				result, e := tx.ExecContext(ctx, `UPDATE external_metadata SET normalized_json=json_set(normalized_json,'$.remote_page_count',?,'$.remote_size_bytes',?),revision=revision+1 WHERE id=? AND revision=?`, record.RemotePageCount, record.RemoteSizeBytes, id, revision)
				if e != nil {
					return out, e
				}
				n, e := result.RowsAffected()
				if e != nil {
					return out, e
				}
				if n != 1 {
					return out, model.ErrConflict
				}
				record.Revision++
				record.Status = "updated"
				out.Updated++
			}
		}
		out.Items = append(out.Items, record)
	}
	return out, tx.Commit()
}

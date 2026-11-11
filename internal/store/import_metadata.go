package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
)

func importCandidate(ctx context.Context, tx *sql.Tx, batch, id string) (*model.ImportCandidate, error) {
	var c model.ImportCandidate
	var input, result string
	e := scopeRow(ctx, tx, `SELECT revision,request_generation,content_revision,selection_revision,input_json,result_json FROM import_candidates WHERE batch_id=? AND edition_id=?`, batch, id).Scan(&c.Revision, &c.Generation, &c.ContentRevision, &c.SelectionRevision, &input, &result)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if e = json.Unmarshal([]byte(input), &c.Input); e != nil {
		return nil, e
	}
	if e = json.Unmarshal([]byte(result), &c.Result); e != nil {
		return nil, e
	}
	c.Fields, c.URL, c.Method = c.Result.Fields, c.Result.URL, c.Result.Method
	e = scopeRow(ctx, tx, `SELECT count(*) FROM edition_sources s JOIN external_metadata x ON x.id=s.metadata_id WHERE x.provider='ehentai' AND x.external_id=? AND s.edition_id!=?`, c.Result.ExternalID, id).Scan(&c.SharedEditions)
	return &c, e
}

// Return handled=true for import children, including stale requests. No import
// result is allowed to enter the ordinary publish-on-success path.
func stageImportMetadata(ctx context.Context, tx *sql.Tx, j model.Job, item model.Item, result *model.RemoteMetadata, code string, candidates []model.Candidate) (bool, error) {
	var batch string
	e := scopeRow(ctx, tx, "SELECT batch_id FROM import_children WHERE job_id=? AND stage='metadata'", j.ID).Scan(&batch)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return true, e
	}
	var active, state string
	var generation int64
	e = scopeRow(ctx, tx, `SELECT COALESCE(m.active_job_id,''),m.request_generation,b.state FROM import_members m JOIN import_batches b ON b.id=m.batch_id WHERE m.batch_id=? AND m.edition_id=?`, batch, *item.BookID).Scan(&active, &generation, &state)
	if e != nil {
		return true, e
	}
	if active != j.ID || state != "processing" {
		return true, finishItem(ctx, tx, j, item, "deferred", "import_request_superseded", nil)
	}
	itemState := "completed"
	if result != nil {
		if e = model.ValidateMetadata(result.Fields); e != nil {
			return true, e
		}
		if len(result.Raw) > 4<<20 || !json.Valid(result.Raw) {
			return true, model.ErrInput
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return true, e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO import_candidates(batch_id,edition_id,request_generation,content_revision,selection_revision,input_json,result_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(batch_id,edition_id) DO UPDATE SET revision=import_candidates.revision+1,request_generation=excluded.request_generation,content_revision=excluded.content_revision,selection_revision=excluded.selection_revision,input_json=excluded.input_json,result_json=excluded.result_json,created_at=unixepoch()`, batch, *item.BookID, generation, item.ContentRevision, item.SelectionRevision, item.Input, string(raw))
		if e != nil {
			return true, e
		}
	} else {
		itemState = "failed"
		if code == "no_match" || code == "multiple_matches" || code == "no_sha1" {
			itemState = "deferred"
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_members SET fetch_error=?,revision=revision+1 WHERE batch_id=? AND edition_id=?", code, batch, *item.BookID); e != nil {
		return true, e
	}
	if e = importTouch(ctx, tx, batch); e != nil {
		return true, e
	}
	return true, finishItem(ctx, tx, j, item, itemState, code, candidates)
}
func importGuard(ctx context.Context, tx *sql.Tx, batch string, expected int64) (*model.ImportBatch, error) {
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", batch))
	if e != nil {
		return nil, e
	}
	if b.Revision != expected || b.State == "completed" || b.State == "cancelled" || b.State == "paused" {
		return nil, model.ErrConflict
	}
	return b, nil
}
func (s *Store) RefetchImport(ctx context.Context, batch, id, url string, expected int64) (string, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", batch))
	if e != nil {
		return "", e
	}
	if b.Stage != "metadata" || (b.State != "processing" && b.State != "review") {
		return "", model.ErrConflict
	}
	var actual int64
	if e = scopeRow(ctx, tx, "SELECT revision FROM import_members WHERE batch_id=? AND edition_id=?", batch, id).Scan(&actual); e != nil {
		return "", e
	}
	if actual != expected {
		return "", model.ErrConflict
	}
	job, e := reserveImport(ctx, tx, batch, "metadata", []string{id}, url)
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_members SET active_job_id=?,request_generation=request_generation+1,metadata_decision='',metadata_token='',fetch_error='',revision=revision+1 WHERE batch_id=? AND edition_id=?", job, batch, id); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET state='processing',error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", batch); e != nil {
		return "", e
	}
	return job, tx.Commit()
}
func (s *Store) ReviewImport(ctx context.Context, batch string, reviews []model.ImportReview) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = reviewImportTx(ctx, tx, batch, reviews); e != nil {
		return e
	}
	return tx.Commit()
}
func reviewImportTx(ctx context.Context, tx *sql.Tx, batch string, reviews []model.ImportReview) error {
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", batch))
	if e != nil {
		return e
	}
	if (b.State != "review" && b.State != "processing") || b.Stage == "scan" || b.Stage == "finished" {
		return model.ErrConflict
	}
	// Validate human-edit revisions before this transaction refreshes shared
	// sources: its own earlier publications may legitimately bump later rows.
	if b.Stage == "metadata" {
		for _, r := range reviews {
			if r.EditionRevision > 0 {
				var revision int64
				if e = scopeRow(ctx, tx, "SELECT revision FROM editions WHERE id=?", r.ID).Scan(&revision); e != nil {
					return e
				}
				if revision != r.EditionRevision {
					return model.ErrConflict
				}
			}
		}
	}
	for _, r := range reviews {
		table, key, col, tokenCol := "import_members", "edition_id", "metadata_decision", "metadata_token"
		if b.Stage == "versions" {
			col, tokenCol = "version_decision", "version_token"
		}
		if b.Stage == "series" {
			table, key, col, tokenCol = "import_works", "work_id", "decision", "input_token"
		}
		var actual int64
		if e = scopeRow(ctx, tx, "SELECT revision FROM "+table+" WHERE batch_id=? AND "+key+"=?", batch, r.ID).Scan(&actual); e != nil {
			return e
		}
		if actual != r.ExpectedRevision {
			return model.ErrConflict
		}
		if b.Stage == "metadata" {

			var busy int
			if e = scopeRow(ctx, tx, `SELECT count(*) FROM import_members m WHERE batch_id=? AND edition_id=? AND active_job_id IS NOT NULL AND (EXISTS(SELECT 1 FROM job_items i JOIN jobs j ON j.id=i.job_id WHERE i.job_id=m.active_job_id AND i.edition_id=m.edition_id AND i.state='pending' AND j.state IN('queued','running','paused')) OR NOT EXISTS(SELECT 1 FROM jobs WHERE id=m.active_job_id))`, batch, r.ID).Scan(&busy); e != nil {
				return e
			}
			if busy > 0 {
				return model.ErrBusy
			}
			switch r.Decision {
			case "accept":
				c, err := importCandidate(ctx, tx, batch, r.ID)
				if err != nil {
					return err
				}
				if c.Revision != r.CandidateRevision {
					return model.ErrConflict
				}
				var content, selection int64
				if e = scopeRow(ctx, tx, `SELECT f.content_revision,COALESCE(m.selection_revision,1) FROM files f LEFT JOIN edition_metadata_state m ON m.edition_id=f.edition_id WHERE f.edition_id=?`, r.ID).Scan(&content, &selection); e != nil {
					return e
				}
				raw, _ := json.Marshal(c.Input)
				item := model.Item{BookID: &r.ID, Input: string(raw)}
				valid, err := lookupMatches(ctx, tx, item)
				if err != nil {
					return err
				}
				if content != c.ContentRevision || selection != c.SelectionRevision || !valid {
					return model.ErrConflict
				}
				if e = managementGuard(ctx, tx, r.ID); e != nil {
					return e
				}
				if e = publishSource(ctx, tx, r.ID, c.Result); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO edition_metadata_state(edition_id,manual_url,selection_revision) VALUES(?,?,?) ON CONFLICT(edition_id) DO UPDATE SET manual_url=excluded.manual_url,selection_revision=excluded.selection_revision`, r.ID, c.Result.URL, selection+1); e != nil {
					return e
				}
			case "keep", "manual", "skip":
			default:
				return model.ErrInput
			}
			if _, e = tx.ExecContext(ctx, "INSERT INTO file_import_state SELECT edition_id,content_revision FROM files WHERE edition_id=? ON CONFLICT(edition_id) DO UPDATE SET content_revision=excluded.content_revision", r.ID); e != nil {
				return e
			}
		} else if r.Decision != "confirm" && r.Decision != "skip" {
			return model.ErrInput
		}
		var token string
		if b.Stage == "metadata" {
			token, e = importMetadataToken(ctx, tx, r.ID)
		} else {
			token, e = importOrganizationToken(ctx, tx, r.ID, b.Stage == "series")
		}
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "UPDATE "+table+" SET "+col+"=?,"+tokenCol+"=?,revision=revision+1 WHERE batch_id=? AND "+key+"=?", r.Decision, token, batch, r.ID); e != nil {
			return e
		}
	}
	if b.Stage == "metadata" {
		for _, r := range reviews {
			token, err := importMetadataToken(ctx, tx, r.ID)
			if err != nil {
				return err
			}
			if _, e = tx.ExecContext(ctx, "UPDATE import_members SET metadata_token=? WHERE batch_id=? AND edition_id=?", token, batch, r.ID); e != nil {
				return e
			}
		}
	}
	if e = importTouch(ctx, tx, batch); e != nil {
		return e
	}
	return nil
}

// Check ownership before doing remote IO, not merely when publishing its result.
func (s *Store) ImportMetadataInputValid(ctx context.Context, j model.Job, item model.Item) (bool, error) {
	if s.Version < 12 {
		return true, nil
	}
	var owned int
	e := scopeRow(ctx, s.Read, "SELECT count(*) FROM import_children WHERE job_id=?", j.ID).Scan(&owned)
	if e != nil || owned == 0 {
		return owned == 0, e
	}
	var valid int
	e = scopeRow(ctx, s.Read, `SELECT count(*) FROM import_children c JOIN import_batches b ON b.id=c.batch_id JOIN import_members m ON m.batch_id=b.id WHERE c.job_id=? AND m.edition_id=? AND m.active_job_id=c.job_id AND b.stage='metadata' AND b.state='processing'`, j.ID, item.BookID).Scan(&valid)
	return valid == 1, e
}

func (s *Store) ImportCandidateDetail(ctx context.Context, batch, id string) (*model.ImportCandidate, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	c, e := importCandidate(ctx, tx, batch, id)
	if e != nil {
		return nil, e
	}
	return c, tx.Commit()
}
func (s *Store) RetryImportFiles(ctx context.Context, batch string, revision int64, ids []string) (string, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	b, e := importGuard(ctx, tx, batch, revision)
	if e != nil {
		return "", e
	}
	if b.Stage != "metadata" || b.State != "review" {
		return "", model.ErrConflict
	}
	var parent string
	e = scopeRow(ctx, tx, "SELECT r.job_id FROM import_child_requests r JOIN jobs j ON j.id=r.job_id WHERE r.batch_id=? AND r.stage='scan' ORDER BY r.rowid LIMIT 1", batch).Scan(&parent)
	if e != nil {
		return "", e
	}
	ordinals := []int{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return "", model.ErrInput
		}
		seen[id] = true
		var ordinal int
		e = scopeRow(ctx, tx, "SELECT i.ordinal FROM import_members m JOIN job_items i ON i.edition_id=m.edition_id WHERE m.batch_id=? AND m.edition_id=? AND m.scan_error!='' AND i.job_id=?", batch, id, parent).Scan(&ordinal)
		if e == sql.ErrNoRows {
			return "", model.ErrInput
		}
		if e != nil {
			return "", e
		}
		ordinals = append(ordinals, ordinal)
	}
	job, e := reserveImport(ctx, tx, batch, "scan", ids, "")
	if e != nil {
		return "", e
	}
	input, _ := json.Marshal(map[string]any{"edition_ids": ids, "parent_job_id": parent, "ordinals": ordinals})
	if _, e = tx.ExecContext(ctx, "UPDATE import_child_requests SET input_json=? WHERE job_id=?", string(input), job); e != nil {
		return "", e
	}
	for _, id := range ids {
		if _, e = tx.ExecContext(ctx, "UPDATE import_members SET active_job_id=NULL,metadata_decision='',metadata_token='',revision=revision+1 WHERE batch_id=? AND edition_id=?", batch, id); e != nil {
			return "", e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET stage='scan',state='processing',error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", batch); e != nil {
		return "", e
	}
	return job, tx.Commit()
}

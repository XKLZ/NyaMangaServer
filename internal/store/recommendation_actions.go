package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	rec "nyamangaserver.local/server/internal/recommendation"
)

const recommendationColumns = `r.id,r.job_id,r.query_edition_id,r.target_work_id,r.tier,r.rank,r.result_json,r.revision,r.created_at,r.query_fingerprint,r.target_fingerprint,f.relation,f.note,f.labels_json,f.revision,f.accepted_result_json`
const recommendationFrom = ` work_recommendations r LEFT JOIN recommendation_feedback f ON f.recommendation_id=r.id`

type recommendationRow struct {
	rec.Record
	QueryFingerprint, TargetFingerprint string
	AcceptedJSON                        *string
}

func scanRecommendation(row scanner) (recommendationRow, error) {
	var v recommendationRow
	var raw string
	var relation, note, labels *string
	var revision *int64
	e := row.Scan(&v.ID, &v.JobID, &v.QueryID, &v.TargetID, &v.Tier, &v.Rank, &raw, &v.Revision, &v.Created, &v.QueryFingerprint, &v.TargetFingerprint, &relation, &note, &labels, &revision, &v.AcceptedJSON)
	if errors.Is(e, sql.ErrNoRows) {
		return v, model.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	if e = json.Unmarshal([]byte(raw), &v.Result); e != nil {
		return v, e
	}
	if relation != nil {
		v.Feedback = &rec.Feedback{Relation: *relation, Expected: v.Revision}
		if note != nil {
			v.Feedback.Note = *note
		}
		if labels != nil {
			v.Feedback.Labels = json.RawMessage(*labels)
		}
		if revision != nil {
			v.FeedbackRevision = *revision
		}
	}
	v.Accepted = v.AcceptedJSON != nil
	return v, nil
}
func (s *Store) RecommendationPage(ctx context.Context, job, edition, tier string, page, size int) (rec.Page, error) {
	out := rec.Page{Items: []rec.Record{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	where := ` WHERE (?='' OR r.job_id=?) AND (?='' OR r.query_edition_id=?) AND (?='' OR r.tier=?)`
	args := []any{job, job, edition, edition, tier, tier}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM work_recommendations r"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+recommendationColumns+" FROM "+recommendationFrom+where+" ORDER BY r.created_at DESC,r.job_id,r.query_edition_id,r.rank,r.id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, e := scanRecommendation(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v.Record)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if job != "" {
		var raw, summary, inputErrors string
		out.Resources = &rec.Resources{}
		e = scopeRow(ctx, tx, "SELECT resources_json,prepared_count,summary_json,input_errors_json,json_array_length(pool_json,'$.editions') FROM recommendation_batches WHERE job_id=?", job).Scan(&raw, &out.PreparedCount, &summary, &inputErrors, &out.PoolCount)
		if errors.Is(e, sql.ErrNoRows) {
			return out, model.ErrNotFound
		}
		if e != nil {
			return out, e
		}
		if e = json.Unmarshal([]byte(raw), out.Resources); e != nil {
			return out, e
		}
		if e = json.Unmarshal([]byte(summary), &out.QuerySummaries); e != nil {
			return out, e
		}
		if e = json.Unmarshal([]byte(inputErrors), &out.InputErrors); e != nil {
			return out, e
		}
		out.Summary, e = recommendationSummary(ctx, tx, job)
		if e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}
func (s *Store) RecommendationFeedback(ctx context.Context, account, id string, in rec.Feedback) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	row, e := scanRecommendation(scopeRow(ctx, tx, "SELECT "+recommendationColumns+" FROM "+recommendationFrom+" WHERE r.id=?", id))
	if e != nil {
		return e
	}
	if row.Accepted {
		return rec.ErrAlreadyAccepted
	}
	if row.Revision != in.Expected {
		return model.ErrConflict
	}
	if e = validRecommendation(ctx, tx, row); e != nil {
		return e
	}
	labels := in.Labels
	if len(labels) == 0 {
		labels = []byte("{}")
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO recommendation_feedback(recommendation_id,account_id,relation,note,labels_json,revision,updated_at) VALUES(?,?,?,?,?,?,unixepoch()) ON CONFLICT(recommendation_id) DO UPDATE SET relation=excluded.relation,note=excluded.note,labels_json=excluded.labels_json,revision=excluded.revision,updated_at=excluded.updated_at`, id, account, in.Relation, in.Note, string(labels), row.Revision+1); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_recommendations SET revision=revision+1 WHERE id=?", id); e != nil {
		return e
	}
	return tx.Commit()
}
func validRecommendation(ctx context.Context, tx *sql.Tx, row recommendationRow) error {
	if row.QueryID == nil || row.TargetID == nil {
		return rec.ErrStale
	}
	if e := managementGuard(ctx, tx, *row.QueryID); e != nil {
		return rec.ErrStale
	}
	ed, w, e := recommendationCurrent(ctx, tx, *row.QueryID, *row.TargetID)
	if e != nil {
		return e
	}
	if ed.Fingerprint != row.QueryFingerprint || w.Fingerprint != row.TargetFingerprint || ed.WorkID == w.ID {
		return rec.ErrStale
	}
	return nil
}
func bindRecommendation(row recommendationRow, c model.WorkCommand) error {
	if c.Action != "edition_move" || c.EditionID != row.Result.QueryID || c.TargetWorkID != row.Result.WorkID || c.ID != "" || len(c.EditionIDs) > 0 || len(c.WorkIDs) > 0 || c.LibraryID != "" || c.TargetSeriesID != "" || c.NewWorkID != "" || len(c.NewWorkIDs) > 0 || c.Name != "" || c.NameOp != "" || c.Value != nil || c.Labels != nil || c.PreferredEditionID != "" || c.KeepSeries || c.Position != 0 || len(c.LegacyItemIDs) > 0 || c.LegacyItemIDsSet || c.Snapshot != "" {
		return model.ErrInput
	}
	return nil
}
func (s *Store) RecommendationPreview(ctx context.Context, account, id string, c model.WorkCommand, plan organization.WorkPlanner) (rec.Preview, error) {
	out := rec.Preview{RecommendationID: id}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	row, e := scanRecommendation(scopeRow(ctx, tx, "SELECT "+recommendationColumns+" FROM "+recommendationFrom+" WHERE r.id=?", id))
	if e != nil {
		return out, e
	}
	if row.Accepted {
		return out, rec.ErrAlreadyAccepted
	}
	if e = bindRecommendation(row, c); e != nil {
		return out, e
	}
	if e = validRecommendation(ctx, tx, row); e != nil {
		return out, e
	}
	if len(c.Expected) == 0 {
		g, e := loadWorkContext(ctx, tx, account, []string{c.EditionID}, []string{c.TargetWorkID}, nil, true)
		if e != nil {
			return out, e
		}
		c.Expected = g.Expected
	}
	out.Revision = row.Revision
	out.Organization, e = changeWorkTx(ctx, tx, account, c, true, plan)
	return out, e
}
func (s *Store) AcceptRecommendation(ctx context.Context, account, id string, in rec.AcceptInput, plan organization.WorkPlanner) (model.WorkResult, error) {
	var out model.WorkResult
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementWork); e != nil {
		return model.WorkResult{}, e
	}
	row, e := scanRecommendation(scopeRow(ctx, tx, "SELECT "+recommendationColumns+" FROM "+recommendationFrom+" WHERE r.id=?", id))
	if e != nil {
		return out, e
	}
	if e = bindRecommendation(row, in.Command); e != nil {
		return out, e
	}
	if row.AcceptedJSON != nil {
		if e = json.Unmarshal([]byte(*row.AcceptedJSON), &out); e != nil {
			return out, e
		}
		if rec.Fingerprint(out.Command) != rec.Fingerprint(in.Command) {
			return out, model.ErrConflict
		}
		return out, nil
	}
	if row.Revision != in.Revision {
		return out, model.ErrConflict
	}
	if e = validRecommendation(ctx, tx, row); e != nil {
		return out, e
	}
	if len(in.Command.Expected) == 0 {
		return out, model.ErrInput
	}
	out, e = changeWorkTx(ctx, tx, account, in.Command, false, plan)
	if e != nil {
		return out, e
	}
	raw, _ := json.Marshal(out)
	if _, e = tx.ExecContext(ctx, `INSERT INTO recommendation_feedback(recommendation_id,account_id,relation,note,labels_json,revision,accepted_result_json,updated_at) VALUES(?,?,'same_work',?,'{}',?,?,unixepoch()) ON CONFLICT(recommendation_id) DO UPDATE SET relation='same_work',note=excluded.note,revision=excluded.revision,accepted_result_json=excluded.accepted_result_json,updated_at=excluded.updated_at`, id, account, in.Note, row.Revision+1, string(raw)); e != nil {
		return out, e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_recommendations SET revision=revision+1 WHERE id=?", id); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) RecommendationMiss(ctx context.Context, account, job string, in rec.MissInput) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var n int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM job_items WHERE job_id=? AND edition_id=?", job, in.QueryID).Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return model.ErrInput
	}
	query, target, e := recommendationCurrent(ctx, tx, in.QueryID, in.TargetID)
	if e != nil {
		return e
	}
	if query.WorkID == target.ID {
		return model.ErrInput
	}
	var poolRaw string
	var frozen rec.Pool
	if e = scopeRow(ctx, tx, "SELECT pool_json FROM recommendation_batches WHERE job_id=?", job).Scan(&poolRaw); e != nil {
		return e
	}
	if e = json.Unmarshal([]byte(poolRaw), &frozen); e != nil {
		return e
	}
	queryOK, targetOK := false, false
	for _, v := range frozen.Editions {
		if v.Book.ID == in.QueryID && v.Fingerprint == query.Fingerprint {
			queryOK = true
		}
	}
	for _, v := range frozen.Works {
		if v.ID == in.TargetID && v.Fingerprint == target.Fingerprint {
			targetOK = true
		}
	}
	if !queryOK || !targetOK {
		return rec.ErrStale
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM work_recommendations WHERE job_id=? AND query_edition_id=? AND target_work_id=?", job, in.QueryID, in.TargetID).Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return model.ErrInput
	}
	id, e := model.NewID()
	if e != nil {
		return e
	}
	snapshot, _ := json.Marshal(map[string]any{"query": map[string]any{"edition_id": query.Book.ID, "work_id": query.WorkID, "title": query.Book.Title, "title_original": query.Book.TitleOriginal, "content_revision": query.ContentRevision, "input_fingerprint": query.Fingerprint}, "target": map[string]any{"work_id": target.ID, "edition_ids": target.EditionIDs, "preferred_edition_id": target.PreferredEditionID, "revision": target.Revision, "input_fingerprint": target.Fingerprint}})
	if _, e = tx.ExecContext(ctx, `INSERT INTO recommendation_misses(id,job_id,query_edition_id,target_work_id,account_id,note,input_snapshot_json,created_at) VALUES(?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(job_id,query_edition_id,target_work_id,account_id) DO UPDATE SET note=excluded.note`, id, job, in.QueryID, in.TargetID, account, in.Note, string(snapshot)); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) RecommendationProgress(ctx context.Context, j model.Job, prepared int, delta rec.Resources, diagnostics map[string]string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var raw string
	var total rec.Resources
	if e = scopeRow(ctx, tx, "SELECT resources_json FROM recommendation_batches WHERE job_id=?", j.ID).Scan(&raw); e != nil {
		return e
	}
	if e = json.Unmarshal([]byte(raw), &total); e != nil {
		return e
	}
	total.CacheHits += delta.CacheHits
	total.ComputedInputs += delta.ComputedInputs
	total.ModelLoads += delta.ModelLoads
	total.InferenceSeconds += delta.InferenceSeconds
	total.OperationSeconds += delta.OperationSeconds
	total.PeakRSS = max(total.PeakRSS, delta.PeakRSS)
	data, _ := json.Marshal(total)
	if _, e = tx.ExecContext(ctx, "UPDATE recommendation_batches SET prepared_count=max(prepared_count,?),resources_json=? WHERE job_id=?", prepared, string(data), j.ID); e != nil {
		return e
	}
	if len(diagnostics) > 0 {
		var raw string
		var errors map[string]string
		if e = scopeRow(ctx, tx, "SELECT input_errors_json FROM recommendation_batches WHERE job_id=?", j.ID).Scan(&raw); e != nil {
			return e
		}
		if e = json.Unmarshal([]byte(raw), &errors); e != nil {
			return e
		}
		for id, code := range diagnostics {
			errors[id] = code
		}
		data, _ := json.Marshal(errors)
		if _, e = tx.ExecContext(ctx, "UPDATE recommendation_batches SET input_errors_json=? WHERE job_id=?", string(data), j.ID); e != nil {
			return e
		}
	}
	return tx.Commit()
}

func recommendationSummary(ctx context.Context, q queryer, job string) (*rec.QualitySummary, error) {
	out := &rec.QualitySummary{Layers: []rec.LayerSummary{}, Scope: "observed_reviewed_candidates_only_unreviewed_unknown_no_population_recall"}
	rows, e := scopeRows(ctx, q, `SELECT r.tier,sum(f.relation='same_work'),sum(f.relation='same_series'),sum(f.relation='unrelated'),sum(f.relation='uncertain'),sum(f.relation IS NULL) FROM work_recommendations r LEFT JOIN recommendation_feedback f ON f.recommendation_id=r.id WHERE r.job_id=? GROUP BY r.tier ORDER BY r.tier`, job)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var row rec.LayerSummary
		var sw, ss, ur, uc sql.NullInt64
		if e = rows.Scan(&row.Tier, &sw, &ss, &ur, &uc, &row.Unreviewed); e != nil {
			rows.Close()
			return out, e
		}
		row.SameWork = int(sw.Int64)
		row.SameSeries = int(ss.Int64)
		row.Unrelated = int(ur.Int64)
		row.Uncertain = int(uc.Int64)
		known := row.SameWork + row.SameSeries + row.Unrelated
		if known > 0 {
			p := float64(row.SameWork) / float64(known)
			row.ObservedPrecision = &p
		}
		out.Layers = append(out.Layers, row)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	e = scopeRow(ctx, q, "SELECT count(*) FROM recommendation_misses WHERE job_id=?", job).Scan(&out.Misses)
	return out, e
}
func (s *Store) RecommendationMisses(ctx context.Context, job string, page, size int) (rec.MissPage, error) {
	out := rec.MissPage{Items: []rec.Miss{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var n int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM recommendation_batches WHERE job_id=?", job).Scan(&n); e != nil {
		return out, e
	}
	if n != 1 {
		return out, model.ErrNotFound
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM recommendation_misses WHERE job_id=?", job).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT id,query_edition_id,target_work_id,note,input_snapshot_json,created_at FROM recommendation_misses WHERE job_id=? ORDER BY created_at,id LIMIT ? OFFSET ?", job, size, (page-1)*size)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var row rec.Miss
		var raw string
		if e = rows.Scan(&row.ID, &row.QueryID, &row.TargetID, &row.Note, &raw, &row.Created); e != nil {
			rows.Close()
			return out, e
		}
		row.Snapshot = json.RawMessage(raw)
		out.Items = append(out.Items, row)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

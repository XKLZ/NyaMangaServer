package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"nyamangaserver.local/server/internal/model"
)

// The reservation row, not a timestamp or a sorted random job ID, determines
// which attempt owns the stage. A retry supersedes failure without rewriting it.
func importAttempt(ctx context.Context, q queryer, batch, stage string) (id, state, code string, err error) {
	err = scopeRow(ctx, q, `SELECT r.job_id,COALESCE(j.state,'reserved'),COALESCE(j.error_code,'')
 FROM import_child_requests r LEFT JOIN jobs j ON j.id=r.job_id
 WHERE r.batch_id=? AND r.stage=? ORDER BY r.rowid DESC LIMIT 1`, batch, stage).Scan(&id, &state, &code)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func importDecisions(ctx context.Context, q queryer, batch string) (map[string]model.ImportStageDecision, error) {
	var raw string
	if e := scopeRow(ctx, q, "SELECT stage_decisions_json FROM import_batches WHERE id=?", batch).Scan(&raw); e != nil {
		return nil, e
	}
	values := map[string]model.ImportStageDecision{}
	if e := json.Unmarshal([]byte(raw), &values); e != nil {
		return nil, e
	}
	return values, nil
}

func importRecommendationOutcome(ctx context.Context, q queryer, batch, stage string) (*model.ImportTaskFailure, bool, error) {
	id, state, code, e := importAttempt(ctx, q, batch, stage)
	if e != nil {
		return nil, false, e
	}
	decisions, e := importDecisions(ctx, q, batch)
	if e != nil {
		return nil, false, e
	}
	decision := decisions[stage]
	if decision.Decision == "skipped_disabled" {
		return nil, true, nil
	}
	skipped := id != "" && decision.JobID == id && decision.Decision == "skipped"
	if state != "failed" && state != "cancelled" {
		return nil, false, nil
	}
	return &model.ImportTaskFailure{JobID: id, Stage: stage, Code: code}, skipped, nil
}

func enrichImportOutcome(ctx context.Context, tx *sql.Tx, b *model.ImportBatch) error {
	var e error
	b.StageDecisions, e = importDecisions(ctx, tx, b.ID)
	if e != nil {
		return e
	}
	flags, err := importEnhancementFlags(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	b.EnhancementStages = map[string]model.ImportEnhancementStage{}
	for _, stage := range []string{"versions", "series"} {
		status := model.ImportEnhancementStage{Enabled: flags[stage], State: "required"}
		if !flags[stage] {
			status.State = "disabled_skipped"
			status.Reason = "not_enabled_at_creation"
		}
		if flags[stage] && b.State != "completed" && b.State != "cancelled" {
			if enabled, err := enhancementEnabled(ctx, tx, importStageFeature(stage)); err != nil {
				return err
			} else if !enabled {
				status.State = "blocked"
				status.Reason = "enhancement_disabled"
			}
		}
		if flags[stage] && (b.StageDecisions[stage].Decision == "skipped_disabled" || b.StageDecisions[stage].Decision == "skipped") {
			status.State = "skipped"
			status.Reason = "user_skipped"
		}
		b.EnhancementStages[stage] = status
	}
	b.CanRetry = b.State == "failed"
	b.TaskFailures = []model.ImportTaskFailure{}
	b.SkippedStages = []string{}
	for _, stage := range []string{"versions", "series"} {
		if !flags[stage] {
			b.SkippedStages = append(b.SkippedStages, stage)
		}
	}
	rows, e := scopeRows(ctx, tx, `SELECT j.id,c.stage,j.error_code,COALESCE(s.diagnostics_json,'{}')
 FROM import_children c JOIN jobs j ON j.id=c.job_id LEFT JOIN series_discovery_runs s ON s.job_id=j.id
 WHERE c.batch_id=? AND j.state IN('failed','cancelled') ORDER BY j.created_at,j.id`, b.ID)
	if e != nil {
		return e
	}
	for rows.Next() {
		var f model.ImportTaskFailure
		var raw string
		if e = rows.Scan(&f.JobID, &f.Stage, &f.Code, &raw); e != nil {
			rows.Close()
			return e
		}
		var diagnostic struct {
			Phase  string `json:"phase"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal([]byte(raw), &diagnostic) == nil {
			f.Phase, f.Reason = diagnostic.Phase, diagnostic.Reason
		}
		b.TaskFailures = append(b.TaskFailures, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, stage := range []string{"versions", "series"} {
		failure, skipped, err := importRecommendationOutcome(ctx, tx, b.ID, stage)
		if err != nil {
			return err
		}
		if skipped {
			b.SkippedStages = append(b.SkippedStages, stage)
		}
		if failure != nil && !skipped && b.Stage == stage {
			for _, f := range b.TaskFailures {
				if f.JobID == failure.JobID {
					copy := f
					b.CurrentFailure = &copy
				}
			}
			b.CanRetry = b.State == "failed"
		}
	}
	return nil
}

func skipImportRecommendation(ctx context.Context, tx *sql.Tx, b *model.ImportBatch) error {
	if b.State == "paused" && b.Error == "enhancement_disabled" && importStageFeature(b.Stage) != "" {
		if _, e := tx.ExecContext(ctx, "UPDATE jobs SET state='cancelled',generation=generation+1,error_code='import_stage_skipped',updated_at=unixepoch() WHERE id IN(SELECT job_id FROM import_children WHERE batch_id=? AND stage=?) AND state='paused'", b.ID, b.Stage); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM job_resources WHERE job_id IN(SELECT job_id FROM import_children WHERE batch_id=? AND stage=?)", b.ID, b.Stage); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "UPDATE import_batches SET stage_decisions_json=json_set(stage_decisions_json,?,json_object('decision','skipped_disabled','job_id','','created_at',unixepoch())),state='review',error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", "$."+b.Stage, b.ID); e != nil {
			return e
		}
		table := "work_proposals"
		if b.Stage == "series" {
			table = "series_proposals"
		}
		_, e := tx.ExecContext(ctx, "UPDATE "+table+" SET state='superseded',revision=revision+1 WHERE state='pending' AND id IN(SELECT proposal_id FROM import_proposals WHERE batch_id=? AND stage=?)", b.ID, b.Stage)
		return e
	}

	if b.State != "failed" || (b.Stage != "versions" && b.Stage != "series") {
		return model.ErrConflict
	}
	failure, skipped, e := importRecommendationOutcome(ctx, tx, b.ID, b.Stage)
	if e != nil {
		return e
	}
	if failure == nil || skipped {
		return model.ErrConflict
	}
	var active int
	if e = scopeRow(ctx, tx, `SELECT count(*) FROM import_child_requests r LEFT JOIN jobs j ON j.id=r.job_id
 WHERE r.batch_id=? AND r.stage=? AND r.rowid=(SELECT max(rowid) FROM import_child_requests WHERE batch_id=r.batch_id AND stage=r.stage) AND (j.id IS NULL OR j.state IN('queued','running','paused'))`, b.ID, b.Stage).Scan(&active); e != nil {
		return e
	}
	if active != 0 {
		return model.ErrBusy
	}
	decision := model.ImportStageDecision{Decision: "skipped", JobID: failure.JobID, CreatedAt: time.Now().Unix()}
	raw, _ := json.Marshal(decision)
	if _, e = tx.ExecContext(ctx, "UPDATE import_batches SET stage_decisions_json=json_set(stage_decisions_json,?,json(?)),state='review',error_code='',revision=revision+1,updated_at=unixepoch() WHERE id=?", "$."+b.Stage, string(raw), b.ID); e != nil {
		return e
	}
	table := "work_proposals"
	if b.Stage == "series" {
		table = "series_proposals"
	}
	_, e = tx.ExecContext(ctx, "UPDATE "+table+" SET state='superseded',revision=revision+1 WHERE state='pending' AND id IN(SELECT proposal_id FROM import_proposals WHERE batch_id=? AND stage=?)", b.ID, b.Stage)
	return e
}

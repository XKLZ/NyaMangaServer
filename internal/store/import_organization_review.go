package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func organizationReviewTx(ctx context.Context, tx *sql.Tx, id string) (model.OrganizationReview, error) {
	var out model.OrganizationReview
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", id))
	if e != nil {
		return out, e
	}
	if b.Stage != "versions" && b.Stage != "series" {
		return out, model.ErrConflict
	}
	out.Stage, out.State, out.Revision = b.Stage, b.State, b.Revision
	out.Failure, out.RecommendationSkipped, e = importRecommendationOutcome(ctx, tx, id, b.Stage)
	if e != nil {
		return out, e
	}
	out.CanRetry = b.State == "failed" && out.Failure != nil && !out.RecommendationSkipped
	if enabled, err := enhancementEnabled(ctx, tx, importStageFeature(b.Stage)); err != nil {
		return out, err
	} else if !enabled {
		out.CanRetry = false
	}
	table, runs := "work_proposals", "work_discovery_runs"
	if b.Stage == "series" {
		table, runs = "series_proposals", "series_discovery_runs"
	}
	// Hash all scoped proposals (including derived selections), not just this page.
	rows, e := scopeRows(ctx, tx, "SELECT p.id,p.state,p.revision,p.primary_plan FROM "+table+" p JOIN import_proposals ip ON ip.proposal_id=p.id WHERE ip.batch_id=? AND ip.stage=? ORDER BY p.id", id, b.Stage)
	if e != nil {
		return out, e
	}
	snapshots := []any{b.ID, b.Stage, b.State, b.Revision, out.Failure, out.RecommendationSkipped}
	for rows.Next() {
		var pid, state string
		var revision int64
		var primary bool
		if e = rows.Scan(&pid, &state, &revision, &primary); e != nil {
			rows.Close()
			return out, e
		}
		snapshots = append(snapshots, []any{pid, state, revision, primary})
		if state == "pending" && primary {
			out.Pending++
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	var active, refresh int
	if e = scopeRow(ctx, tx, `SELECT count(*) FROM import_child_requests r LEFT JOIN jobs j ON j.id=r.job_id WHERE r.batch_id=? AND r.stage=? AND r.rowid=(SELECT max(rowid) FROM import_child_requests WHERE batch_id=r.batch_id AND stage=r.stage) AND (j.id IS NULL OR j.state IN('queued','running','paused'))`, id, b.Stage).Scan(&active); e != nil {
		return out, e
	}
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM "+runs+" WHERE library_id=? AND refresh_required<>0", b.LibraryID).Scan(&refresh); e != nil {
		return out, e
	}
	out.Active, out.Revalidating = active > 0 && !out.RecommendationSkipped, refresh > 0 && !out.RecommendationSkipped
	snapshots = append(snapshots, active, refresh)
	// Include both saved and live fingerprints. An external edit between read and
	// completion invalidates the token, even if no import batch revision changed.
	rows, e = scopeRows(ctx, tx, `SELECT m.edition_id,m.metadata_token,m.version_decision,m.version_token,m.revision,e.revision FROM import_members m JOIN editions e ON e.id=m.edition_id WHERE m.batch_id=? ORDER BY m.edition_id`, id)
	if e != nil {
		return out, e
	}
	type member struct {
		editionRevision             int64
		id, meta, decision, version string
		revision                    int64
	}
	members := []member{}
	for rows.Next() {
		var m member
		if e = rows.Scan(&m.id, &m.meta, &m.decision, &m.version, &m.revision, &m.editionRevision); e != nil {
			rows.Close()
			return out, e
		}
		members = append(members, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, m := range members {
		meta, err := importMetadataToken(ctx, tx, m.id)
		if err != nil {
			return out, err
		}
		org, err := importOrganizationToken(ctx, tx, m.id, false)
		if err != nil {
			return out, err
		}
		snapshots = append(snapshots, []any{m.id, m.meta, m.decision, m.version, m.revision, m.editionRevision, meta, org})
	}
	if b.Stage == "series" {
		rows, e = scopeRows(ctx, tx, "SELECT work_id,decision,input_token,revision FROM import_works WHERE batch_id=? ORDER BY work_id", id)
		if e != nil {
			return out, e
		}
		works := []member{}
		for rows.Next() {
			var m member
			if e = rows.Scan(&m.id, &m.decision, &m.version, &m.revision); e != nil {
				rows.Close()
				return out, e
			}
			works = append(works, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		for _, m := range works {
			org, err := importOrganizationToken(ctx, tx, m.id, true)
			if err != nil {
				return out, err
			}
			snapshots = append(snapshots, []any{m.id, m.decision, m.version, m.revision, org})
		}
	}
	out.CanComplete = b.State == "review" && !out.Active && !out.Revalidating && out.Pending == 0 && (out.Failure == nil || out.RecommendationSkipped)
	out.Token = rec.Fingerprint(snapshots)
	return out, nil
}
func (s *Store) OrganizationReview(ctx context.Context, id string) (model.OrganizationReview, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.OrganizationReview{}, e
	}
	defer tx.Rollback()
	return organizationReviewTx(ctx, tx, id)
}
func (s *Store) CompleteOrganizationReview(ctx context.Context, id string, in model.CompleteOrganizationReview) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	b, e := importGuard(ctx, tx, id, in.Revision)
	if e != nil {
		return e
	}
	out, e := organizationReviewTx(ctx, tx, id)
	if e != nil {
		return e
	}
	if in.Stage != out.Stage || in.Token != out.Token {
		return model.ErrConflict
	}
	if !out.CanComplete {
		return model.ErrBusy
	}
	table, key, decision, token := "import_members", "edition_id", "version_decision", "version_token"
	if b.Stage == "series" {
		table, key, decision, token = "import_works", "work_id", "decision", "input_token"
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+key+" FROM "+table+" WHERE batch_id=? AND "+decision+"='' ORDER BY "+key, id)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		var v string
		if e = rows.Scan(&v); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range ids {
		current, err := importOrganizationToken(ctx, tx, v, b.Stage == "series")
		if err != nil {
			return err
		}
		if _, e = tx.ExecContext(ctx, "UPDATE "+table+" SET "+decision+"='confirm',"+token+"=?,revision=revision+1 WHERE batch_id=? AND "+key+"=?", current, id, v); e != nil {
			return e
		}
	}
	// Old explicit per-member reviews are deliberately not overwritten. The shared
	// gate verifies their saved tokens and all upstream metadata before any commit.
	if e = advanceImportTx(ctx, tx, b); e != nil {
		return e
	}
	proposals := "work_proposals"
	if in.Stage == "series" {
		proposals = "series_proposals"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE "+proposals+" SET state='superseded',revision=revision+1 WHERE state='pending' AND id IN(SELECT proposal_id FROM import_proposals WHERE batch_id=? AND stage=?)", id, in.Stage); e != nil {
		return e
	}
	return tx.Commit()
}

// Scope does not change scoring/order; false shows only primary suggestions.
func importAlternatives(values []bool) bool { return len(values) == 0 || values[0] }

func importOnlyAlternatives(values []bool) string {
	if len(values) > 1 && values[1] {
		return "alternatives"
	}
	return ""
}

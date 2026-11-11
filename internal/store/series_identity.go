package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func seriesScope(batch string) string {
	if batch == "" {
		return "global"
	}
	return "import:" + batch
}

// Match keys describe evidence; storage keys own one scope's history. Legacy
// storage keys must not change: existing decisions and undo snapshots use them.
func identifySeriesPlan(ctx context.Context, tx *sql.Tx, p rec.SeriesProposal) (rec.SeriesProposal, error) {
	batch, e := importJobScope(ctx, tx, p.JobID)
	if e != nil {
		return p, e
	}
	p.Scope = seriesScope(batch)
	var storage, match, scope string
	e = scopeRow(ctx, tx, "SELECT proposal_key,match_key,scope_key FROM series_proposals WHERE id=?", p.ID).Scan(&storage, &match, &scope)
	if e == nil {
		if match == "" {
			match = storage
		}
		if match != p.Key || scope != p.Scope {
			return p, model.ErrConflict
		}
		p.StorageKey = storage
	} else if errors.Is(e, sql.ErrNoRows) {
		p.StorageKey = rec.Fingerprint([]string{"series-storage-v1", p.Scope, p.Key})
	} else {
		return p, e
	}
	return p, nil
}

func seriesFeedback(ctx context.Context, tx *sql.Tx, p rec.SeriesProposal) (decision string, legacy bool, err error) {
	err = scopeRow(ctx, tx, "SELECT decision,legacy FROM series_match_feedback WHERE library_id=? AND semantic_key=?", p.LibraryID, rec.SeriesSemanticKey(p)).Scan(&decision, &legacy)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}

func saveSeriesFeedback(ctx context.Context, tx *sql.Tx, p rec.SeriesProposal, account, decision string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO series_match_feedback(library_id,semantic_key,decision,proposal_id,account_id,legacy,updated_at) VALUES(?,?,?,?,?,0,unixepoch())
 ON CONFLICT(library_id,semantic_key) DO UPDATE SET decision=excluded.decision,proposal_id=excluded.proposal_id,account_id=excluded.account_id,legacy=0,revision=series_match_feedback.revision+1,updated_at=excluded.updated_at`, p.LibraryID, rec.SeriesSemanticKey(p), decision, p.ID, account)
	return e
}

// A decision and its snapshots can only belong to their original proposal.
func saveSeriesDecision(ctx context.Context, tx *sql.Tx, p rec.SeriesProposal, account, decision string, before, after any) error {
	result, e := tx.ExecContext(ctx, `INSERT INTO series_proposal_decisions(proposal_key,proposal_id,account_id,decision,before_json,after_json,updated_at) VALUES(?,?,?,?,?,?,unixepoch())
 ON CONFLICT(proposal_key) DO UPDATE SET account_id=excluded.account_id,decision=excluded.decision,before_json=COALESCE(excluded.before_json,series_proposal_decisions.before_json),after_json=COALESCE(excluded.after_json,series_proposal_decisions.after_json),updated_at=excluded.updated_at
 WHERE series_proposal_decisions.proposal_id=excluded.proposal_id`, p.StorageKey, p.ID, account, decision, before, after)
	if e != nil {
		return e
	}
	n, e := result.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrConflict
	}
	return nil
}

// Runs in the migration transaction, after 015. It only materializes verifiable
// legacy dismissals; existing proposals, decisions and restored states stay intact.
func migrateSeriesFeedback(ctx context.Context, tx *sql.Tx) error {
	var conflicts int
	if e := scopeRow(ctx, tx, `SELECT count(*) FROM (SELECT proposal_id FROM import_proposals WHERE stage='series' GROUP BY proposal_id HAVING count(DISTINCT batch_id)>1)`).Scan(&conflicts); e != nil {
		return e
	}
	if conflicts != 0 {
		return errors.New("ambiguous legacy series proposal scope")
	}
	rows, e := scopeRows(ctx, tx, `SELECT p.plan_json,p.id,p.library_id,d.account_id,d.updated_at FROM series_proposals p JOIN series_proposal_decisions d ON d.proposal_id=p.id WHERE p.state='dismissed' AND d.decision='dismissed' ORDER BY d.updated_at,p.id`)
	if e != nil {
		return e
	}
	type value struct {
		p       rec.SeriesProposal
		account string
		at      int64
	}
	values := []value{}
	for rows.Next() {
		var v value
		var raw string
		if e = rows.Scan(&raw, &v.p.ID, &v.p.LibraryID, &v.account, &v.at); e != nil {
			rows.Close()
			return e
		}
		var stored rec.StoredSeriesProposal
		if e = json.Unmarshal([]byte(raw), &stored); e != nil {
			rows.Close()
			return errors.New("invalid legacy series proposal")
		}
		id, library := v.p.ID, v.p.LibraryID
		v.p = stored.Proposal
		v.p.ID, v.p.LibraryID = id, library
		v.p.Inputs = stored.Inputs
		values = append(values, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range values {
		if _, e = tx.ExecContext(ctx, `INSERT INTO series_match_feedback(library_id,semantic_key,decision,proposal_id,account_id,legacy,updated_at) VALUES(?,?,'dismissed',?,?,1,?)
 ON CONFLICT(library_id,semantic_key) DO UPDATE SET proposal_id=excluded.proposal_id,account_id=excluded.account_id,updated_at=excluded.updated_at`, v.p.LibraryID, rec.SeriesSemanticKey(v.p), v.p.ID, v.account, v.at); e != nil {
			return e
		}
	}
	return nil
}

func seriesStorageError(e error) error {
	if e == nil || errors.Is(e, model.ErrConflict) || errors.Is(e, model.ErrStaleJob) || errors.Is(e, rec.ErrStale) || errors.Is(e, model.ErrCapacity) {
		return e
	}
	reason := "storage_unavailable"
	if errors.Is(e, model.ErrInput) || errors.Is(e, model.ErrLibraryConflict) || errors.Is(e, model.ErrSeriesConflict) || errors.Is(e, model.ErrNotFound) {
		reason = "proposal_invalid"
	}
	var coded interface{ Code() int }
	if errors.As(e, &coded) {
		switch coded.Code() & 255 {
		case 19:
			reason = "database_constraint"
		case 13:
			reason = "storage_capacity"
		}
	}
	return &rec.SeriesPublicationError{Reason: reason, Err: e}
}

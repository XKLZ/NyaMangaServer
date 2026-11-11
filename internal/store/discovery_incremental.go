package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

func (s *Store) CachedDiscoveryFeatures(ctx context.Context, id, key string) (rec.FeatureResult, bool, error) {
	var v rec.FeatureResult
	var raw string
	if s.Version < 11 || key == "" {
		return v, false, nil
	}
	e := scopeRow(ctx, s.Read, "SELECT result_json FROM discovery_feature_inputs WHERE edition_id=? AND input_key=?", id, key).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return v, false, nil
	}
	if e != nil {
		return v, false, e
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return v, false, nil
	}
	return v, true, nil
}
func (s *Store) SaveDiscoveryFeatures(ctx context.Context, j model.Job, input rec.InputEdition, key string, result rec.FeatureResult, reused bool) error {
	if s.Version < 11 {
		return nil
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	current, _, e := recommendationCurrent(ctx, tx, input.Book.ID, "")
	if e != nil {
		return e
	}
	if rec.EvidenceFingerprint(input) != rec.EvidenceFingerprint(current) {
		return rec.ErrStale
	}
	raw, _ := json.Marshal(result)
	if key != "" && !result.MissingCover {
		if _, e = tx.ExecContext(ctx, `INSERT INTO discovery_feature_inputs VALUES(?,?,?) ON CONFLICT(edition_id) DO UPDATE SET input_key=excluded.input_key,result_json=excluded.result_json`, input.Book.ID, key, string(raw)); e != nil {
			return e
		}
	}
	var prior string
	err := scopeRow(ctx, tx, "SELECT input_key FROM discovery_run_features WHERE job_id=? AND edition_id=?", j.ID, input.Book.ID).Scan(&prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if prior != "" && prior != rec.PairInputFingerprint(input, result) {
		for _, q := range []string{"DELETE FROM work_pair_evidence WHERE job_id=?", "DELETE FROM work_pair_blocks WHERE job_id=?", "UPDATE job_items SET state='pending' WHERE job_id=?", "UPDATE jobs SET done=0,stage='preparing' WHERE id=?", "UPDATE work_discovery_runs SET next_row=0,phase='features',edge_count=0,evidence_bytes=0,reused_pairs=0,computed_pairs=0 WHERE job_id=?"} {
			if _, e = tx.ExecContext(ctx, q, j.ID); e != nil {
				return e
			}
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO discovery_run_features VALUES(?,?,?) ON CONFLICT(job_id,edition_id) DO UPDATE SET input_key=excluded.input_key`, j.ID, input.Book.ID, rec.PairInputFingerprint(input, result)); e != nil {
		return e
	}
	// Counting unique completed feature inputs is idempotent across pause/restart.
	if prior != "" {
		return tx.Commit()
	}
	column := "prepared_features"
	if reused {
		column = "reused_features"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE work_discovery_runs SET "+column+"="+column+"+1 WHERE job_id=?", j.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) DiscoveryReuse(ctx context.Context, job string) (*rec.PairReuse, error) {
	r := &rec.PairReuse{Graph: rec.PairGraph{Hard: map[int][]byte{}}, Matching: map[string]string{}, Current: map[string]string{}}
	if s.Version < 11 {
		r.Prepare()
		return r, nil
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var base, version string
	if e = scopeRow(ctx, tx, "SELECT COALESCE(base_job_id,''),execution_version FROM work_discovery_runs WHERE job_id=?", job).Scan(&base, &version); e != nil {
		return nil, e
	}
	if version != "" && version != rec.DiscoveryExecutionVersion {
		return nil, rec.ErrStale
	}
	if base != "" {
		pool, err := frozenDiscoveryPool(ctx, tx, base)
		if err != nil {
			return nil, err
		}
		r.Graph, err = discoveryGraph(ctx, tx, base, pool)
		if err != nil {
			return nil, err
		}
	}
	rows, e := scopeRows(ctx, tx, "SELECT job_id,edition_id,input_key FROM discovery_run_features WHERE job_id IN(?,?)", job, base)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var j, id, k string
		if e = rows.Scan(&j, &id, &k); e != nil {
			rows.Close()
			return nil, e
		}
		if j == job {
			r.Current[id] = k
		} else {
			r.Matching[id] = k
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	r.Prepare()
	return r, tx.Commit()
}

func (s *Store) SaveDiscoveryPreprocessing(ctx context.Context, j model.Job, keys map[string]string, version string) error {
	if s.Version < 13 {
		return nil
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	raw, _ := json.Marshal(keys)
	_, e = tx.ExecContext(ctx, `INSERT INTO discovery_preprocessing VALUES(?,?,?) ON CONFLICT(job_id) DO UPDATE SET fingerprints_json=excluded.fingerprints_json,version=excluded.version`, j.ID, string(raw), version)
	if e != nil {
		return e
	}
	return tx.Commit()
}

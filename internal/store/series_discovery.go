package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
	"strconv"
	"strings"
)

func (s *Store) CreateSeriesDiscovery(ctx context.Context, id, library, parent string) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementSeries); e != nil {
		return nil, e
	}
	l, e := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=?", library))
	if e != nil {
		return nil, e
	}
	if l.Disabled {
		return nil, model.ErrDisabled
	}
	if l.TrashedAt != nil {
		return nil, model.ErrTrashed
	}
	pool, e := recommendationPool(ctx, tx, []string{library})
	if e != nil {
		return nil, e
	}
	n := len(pool.Works)
	if n < 2 {
		return nil, model.ErrInput
	}
	if n > 6000 || int64(n)*int64(n-1)/2 > rec.MaxDiscoveryComparisons {
		return nil, model.ErrCapacity
	}
	c, e := discoveryContext(ctx, tx, library)
	if e != nil {
		return nil, e
	}
	token, e := discoveryToken(ctx, tx, library)
	if e != nil {
		return nil, e
	}
	raw, _ := json.Marshal(pool)
	contextRaw, _ := json.Marshal(struct {
		model.WorkContext
		Presentation string `json:"series_presentation_version"`
	}{c, rec.SeriesPresentationVersion})
	if len(raw) > 16<<20 || len(raw)+len(pool.Editions)*2100 > rec.MaxMessage-1<<20 {
		return nil, model.ErrCapacity
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,type,mode,library_id,state,stage,generation,total,parent_job_id,created_at,updated_at) VALUES(?,'series_recommendation','library',?,'queued','preparing',1,?,NULLIF(?,''),unixepoch(),unixepoch())`, id, library, n, parent)
	if e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return nil, model.ErrJobActive
		}
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO series_discovery_runs(job_id,library_id,pool_json,context_json,input_token,evidence_version,created_at) VALUES(?,?,?,?,?,?,unixepoch())`, id, library, string(raw), string(contextRaw), token, rec.SeriesAlgorithm); e != nil {
		return nil, e
	}
	for i, w := range pool.Works {
		if _, e = tx.ExecContext(ctx, `INSERT INTO job_items(job_id,ordinal,state,edition_id,input_json) VALUES(?,?,'pending',?,?)`, id, i+1, w.PreferredEditionID, `{}`); e != nil {
			return nil, e
		}
	}
	if e = armEnhancement(ctx, tx, model.EnhancementSeries); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}

func (s *Store) SeriesRun(ctx context.Context, id string, current bool) (rec.SeriesRun, error) {
	out := rec.SeriesRun{JobID: id, Features: map[string]rec.PackedVector{}, Edges: [][2]string{}}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var pool, contextRaw, version string
	e = scopeRow(ctx, tx, `SELECT library_id,pool_json,context_json,input_token,features_done,next_row,phase,evidence_version FROM series_discovery_runs WHERE job_id=?`, id).Scan(&out.LibraryID, &pool, &contextRaw, &out.Token, &out.FeaturesDone, &out.NextRow, &out.Phase, &version)
	if errors.Is(e, sql.ErrNoRows) {
		return out, model.ErrNotFound
	}
	if e != nil {
		return out, e
	}
	if version != rec.SeriesAlgorithm {
		return out, rec.ErrStale
	}
	if json.Unmarshal([]byte(pool), &out.Pool) != nil || json.Unmarshal([]byte(contextRaw), &out.Context) != nil {
		return out, model.ErrInput
	}
	var display struct {
		Version string `json:"series_presentation_version"`
	}
	if json.Unmarshal([]byte(contextRaw), &display) != nil {
		return out, model.ErrInput
	}
	if display.Version != "" && display.Version != rec.SeriesPresentationVersion {
		return out, rec.ErrStale
	}
	out.Presentation = display.Version
	// Feature checkpoints live on bounded Work items (at most 100 Editions).
	// Avoid rewriting one growing, library-sized JSON value for every Edition.
	featureRows, e := scopeRows(ctx, tx, "SELECT COALESCE(json_extract(input_json,'$.features'),'{}') FROM job_items WHERE job_id=? ORDER BY ordinal", id)
	if e != nil {
		return out, e
	}
	for featureRows.Next() {
		var raw string
		var values map[string]rec.PackedVector
		if e = featureRows.Scan(&raw); e != nil {
			break
		}
		if e = json.Unmarshal([]byte(raw), &values); e != nil {
			break
		}
		for key, value := range values {
			out.Features[key] = value
		}
	}
	if e == nil {
		e = featureRows.Err()
	}
	featureRows.Close()
	if e != nil {
		return out, e
	}
	if current {
		actual, err := recommendationPool(ctx, tx, []string{out.LibraryID})
		if err != nil {
			return out, err
		}
		old := map[string]string{}
		for _, ed := range out.Pool.Editions {
			old[ed.Book.ID] = rec.SeriesInput(ed)
		}
		if len(actual.Editions) != len(old) {
			return out, rec.ErrStale
		}
		for _, ed := range actual.Editions {
			if old[ed.Book.ID] != rec.SeriesInput(ed) {
				return out, rec.ErrStale
			}
		}
		out.Pool = actual
		out.Context, e = discoveryContext(ctx, tx, out.LibraryID)
		if e != nil {
			return out, e
		}
		out.Token, e = discoveryToken(ctx, tx, out.LibraryID)
		if e != nil {
			return out, e
		}
	}
	rows, e := scopeRows(ctx, tx, "SELECT edges_json FROM series_pair_rows WHERE job_id=? ORDER BY row_index", id)
	if e != nil {
		return out, e
	}
	seen := map[[2]string]bool{}
	for rows.Next() {
		var raw string
		var edges [][2]string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(raw), &edges); e != nil {
			rows.Close()
			return out, e
		}
		for _, edge := range edges {
			if !seen[edge] {
				out.Edges = append(out.Edges, edge)
				seen[edge] = true
			}
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	// Series membership may change after an accepted proposal. Evidence remains
	// keyed to the original Work partition; reject regrouping rather than reuse it.
	if current {
		var original rec.Pool
		_ = json.Unmarshal([]byte(pool), &original)
		groups := map[string]string{}
		for _, w := range original.Works {
			groups[w.ID] = rec.Fingerprint(w.EditionIDs)
		}
		if len(groups) != len(out.Pool.Works) {
			return out, rec.ErrStale
		}
		for _, w := range out.Pool.Works {
			if groups[w.ID] != rec.Fingerprint(w.EditionIDs) {
				return out, rec.ErrStale
			}
		}
	}
	return out, tx.Commit()
}

func (s *Store) SaveSeriesFeature(ctx context.Context, j model.Job, at int, input rec.InputEdition, value *rec.PackedVector) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var next int
	var raw string
	if e = scopeRow(ctx, tx, "SELECT features_done,pool_json FROM series_discovery_runs WHERE job_id=?", j.ID).Scan(&next, &raw); e != nil {
		return e
	}
	var pool rec.Pool
	if json.Unmarshal([]byte(raw), &pool) != nil || at != next || at >= len(pool.Editions) || pool.Editions[at].Book.ID != input.Book.ID {
		return model.ErrInput
	}
	current, _, e := recommendationCurrent(ctx, tx, input.Book.ID, "")
	if e != nil {
		return e
	}
	if rec.SeriesInput(current) != rec.SeriesInput(input) {
		return rec.ErrStale
	}
	if e = managementGuard(ctx, tx, input.Book.ID); e != nil {
		return e
	}
	if value != nil {
		ordinal := 0
		for i, w := range pool.Works {
			if w.ID == pool.Editions[at].WorkID {
				ordinal = i + 1
				break
			}
		}
		if ordinal == 0 {
			return model.ErrInput
		}
		v, _ := json.Marshal(value)
		if _, e = tx.ExecContext(ctx, "UPDATE job_items SET input_json=json_set(input_json,?,json(?)) WHERE job_id=? AND ordinal=?", "$.features."+strconv.Quote(input.Book.ID), string(v), j.ID, ordinal); e != nil {
			return e
		}
	}
	phase := "features"
	if at+1 == len(pool.Editions) {
		phase = "pairs"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_discovery_runs SET features_done=?,phase=? WHERE job_id=?", at+1, phase, j.ID); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) SaveSeriesBlock(ctx context.Context, j model.Job, b rec.SeriesBlock) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var next, done, count int
	var raw string
	if e = scopeRow(ctx, tx, "SELECT next_row,features_done,edge_count,pool_json FROM series_discovery_runs WHERE job_id=?", j.ID).Scan(&next, &done, &count, &raw); e != nil {
		return e
	}
	var pool rec.Pool
	if json.Unmarshal([]byte(raw), &pool) != nil {
		return model.ErrInput
	}
	if done != len(pool.Editions) || b.Row != next || b.NextRow != next+1 || next >= len(pool.Works) || len(b.Edges) > len(pool.Works) {
		return model.ErrInput
	}
	ids := map[string]bool{}
	for _, w := range pool.Works {
		ids[w.ID] = true
	}
	seen := map[[2]string]bool{}
	query := pool.Works[next].ID
	for _, edge := range b.Edges {
		if edge[0] >= edge[1] || !ids[edge[0]] || !ids[edge[1]] || seen[edge] || (edge[0] != query && edge[1] != query) {
			return model.ErrInput
		}
		seen[edge] = true
	}
	if count+len(b.Edges) > 2*rec.MaxDiscoveryEdges {
		return model.ErrCapacity
	}
	edges, _ := json.Marshal(b.Edges)
	if len(edges) > 2<<20 {
		return model.ErrCapacity
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO series_pair_rows VALUES(?,?,?)", j.ID, b.Row, string(edges)); e != nil {
		return e
	}
	phase := "pairs"
	if b.NextRow == len(pool.Works) {
		phase = "proposals"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_discovery_runs SET next_row=?,edge_count=edge_count+?,phase=? WHERE job_id=?", b.NextRow, len(b.Edges), phase, j.ID); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE job_items SET state='completed' WHERE job_id=? AND ordinal=?", j.ID, b.NextRow); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE jobs SET done=?,stage='comparing',updated_at=unixepoch() WHERE id=?", b.NextRow, j.ID); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) SeriesRefreshJob(ctx context.Context) (string, error) {
	var id string
	e := scopeRow(ctx, s.Read, `SELECT job_id FROM series_discovery_runs r WHERE refresh_required=1 AND phase='finished' AND rowid=(SELECT rowid FROM series_discovery_runs n WHERE n.library_id=r.library_id AND n.phase='finished' ORDER BY created_at DESC,rowid DESC LIMIT 1) ORDER BY created_at,rowid LIMIT 1`).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return id, e
}
func (s *Store) StopSeriesRefresh(ctx context.Context, id, reason string) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementSeries); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_discovery_runs SET refresh_required=0 WHERE job_id=?", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE series_proposals SET state='superseded',revision=revision+1 WHERE job_id=? AND state='pending'", id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE jobs SET error_code=?,updated_at=unixepoch() WHERE id=?", reason, id); e != nil {
		return e
	}
	return tx.Commit()
}

func seriesJSON(v any) string               { b, _ := json.Marshal(v); return string(b) }
func sortedSeriesIDs(ids []string) []string { out := slices.Clone(ids); slices.Sort(out); return out }

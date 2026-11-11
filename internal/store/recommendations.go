package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
	"slices"
	"strings"
	"time"
)

func recommendationPool(ctx context.Context, q queryer, libraries []string) (rec.Pool, error) {
	return recommendationPoolSubset(ctx, q, libraries, "", "")
}
func recommendationPoolSubset(ctx context.Context, q queryer, libraries []string, edition, work string, selectedIDs ...[]string) (rec.Pool, error) {
	out := rec.Pool{Editions: []rec.InputEdition{}, Works: []rec.InputWork{}}
	if len(libraries) == 0 {
		return out, nil
	}
	args := stringArgs(libraries)
	subset := ""
	if edition != "" {
		subset = " AND (e.id=? OR e.work_id=?)"
		args = append(args, edition, work)
	}
	if len(selectedIDs) > 0 {
		raw, _ := json.Marshal(selectedIDs[0])
		subset += " AND e.id IN(SELECT value FROM json_each(?))"
		args = append(args, string(raw))
	}
	parseFields := ",0,'',''"
	encodingField := "l.legacy_zip_encoding"
	if hasParsing(ctx, q) {
		parseFields = ",f.parsing_revision,f.directory_digest,f.parser_version"
		encodingField = "COALESCE(f.encoding_override,l.legacy_zip_encoding)"
	}
	rows, e := scopeRows(ctx, q, `SELECT e.id,e.library_id,e.work_id,e.revision,e.organization_revision,f.content_revision,COALESCE(cs.revision,0),f.indexed_at,f.file_state,f.page_count,f.filename_title,l.root_identity,`+encodingField+`,COALESCE(eb.fields_json,'{}'),COALESCE(p.properties_json,'{}'),COALESCE((SELECT json_group_array(label) FROM (SELECT label FROM edition_version_labels vl WHERE vl.edition_id=e.id ORDER BY position)),'[]'),a.id,a.storage_key,a.digest,a.size`+parseFields+`
 FROM editions e JOIN files f ON f.edition_id=e.id JOIN libraries l ON l.id=e.library_id LEFT JOIN cover_selections cs ON cs.edition_id=e.id LEFT JOIN effective_editions eb ON eb.edition_id=e.id LEFT JOIN effective_edition_properties p ON p.edition_id=e.id LEFT JOIN recommendation_materials rm ON rm.edition_id=e.id LEFT JOIN assets a ON a.id=rm.asset_id
 WHERE e.trashed_at IS NULL AND l.trashed_at IS NULL AND l.disabled=0 AND e.library_id IN(`+placeholders(len(libraries))+`)`+subset+` ORDER BY e.id`, args...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v rec.InputEdition
		var fields, properties, labels, filename string
		var aid, key, digest *string
		var size *int64
		if e = rows.Scan(&v.Book.ID, &v.Book.LibraryID, &v.WorkID, &v.Revision, &v.OrganizationRevision, &v.ContentRevision, &v.CoverRevision, &v.IndexedAt, &v.FileState, &v.Book.Pages, &filename, &v.LibraryIdentity, &v.Encoding, &fields, &properties, &labels, &aid, &key, &digest, &size, &v.ParsingRevision, &v.DirectoryDigest, &v.ParserVersion); e != nil {
			rows.Close()
			return out, e
		}
		var metadata model.Metadata
		var props model.BookProperties
		if e = json.Unmarshal([]byte(fields), &metadata); e != nil {
			rows.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(properties), &props); e != nil {
			rows.Close()
			return out, e
		}
		v.DisplayProperties = &props
		v.Book.Title = filename
		if metadata.Title != nil {
			v.Book.Title = *metadata.Title
		}
		if metadata.TitleOriginal != nil {
			v.Book.TitleOriginal = *metadata.TitleOriginal
		}
		v.Book.Tags = [][2]string{}
		for _, t := range metadata.Tags {
			v.Book.Tags = append(v.Book.Tags, [2]string{t.Namespace, t.Name})
		}
		slices.SortFunc(v.Book.Tags, func(a, b [2]string) int {
			if a[0] != b[0] {
				return strings.Compare(a[0], b[0])
			}
			return strings.Compare(a[1], b[1])
		})
		v.Book.Languages = model.Languages(props.Languages)
		if v.Book.Languages == nil {
			v.Book.Languages = []string{}
		}
		if e = json.Unmarshal([]byte(labels), &v.Book.CustomLabels); e != nil {
			rows.Close()
			return out, e
		}
		v.Book.ManualProperties = map[string]map[string]any{}
		for name, p := range map[string]model.Property{"translation": props.Translated, "color": props.ColorMode, "censorship": props.Censorship} {
			if p.Origin != "manual" {
				continue
			}
			var value any
			if len(p.Value) > 0 {
				if e = json.Unmarshal(p.Value, &value); e != nil {
					rows.Close()
					return out, e
				}
			}
			if b, ok := value.(bool); ok {
				if b {
					value = "translated"
				} else {
					value = "original"
				}
			}
			if name == "censorship" && (value == "mosaic" || value == "full") {
				value = "censored"
			}
			v.Book.ManualProperties[name] = map[string]any{"value": value}
		}
		if aid != nil && key != nil && digest != nil && size != nil {
			v.Material = &model.Asset{ID: *aid, Key: *key, Digest: *digest, Size: *size}
		}
		v.Fingerprint = rec.EditionFingerprint(v)
		out.Editions = append(out.Editions, v)
		if len(out.Editions) > 50000 {
			rows.Close()
			return out, model.ErrCapacity
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	by := map[string]rec.InputEdition{}
	members := map[string][]string{}
	for _, ed := range out.Editions {
		by[ed.Book.ID] = ed
		members[ed.WorkID] = append(members[ed.WorkID], ed.Book.ID)
	}
	workIDs := []string{}
	for id := range members {
		workIDs = append(workIDs, id)
	}
	slices.Sort(workIDs)
	if len(workIDs) == 0 {
		return out, nil
	}
	rows, e = scopeRows(ctx, q, "SELECT id,preferred_edition_id,revision,(SELECT count(*) FROM editions e WHERE e.work_id=works.id) FROM works WHERE id IN("+placeholders(len(workIDs))+") ORDER BY id", stringArgs(workIDs)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var w rec.InputWork
		if e = rows.Scan(&w.ID, &w.PreferredEditionID, &w.Revision, &w.TotalEditionCount); e != nil {
			rows.Close()
			return out, e
		}
		w.EditionIDs = members[w.ID]
		if len(w.EditionIDs) == 0 {
			continue
		}
		if !slices.Contains(w.EditionIDs, w.PreferredEditionID) {
			w.PreferredEditionID = w.EditionIDs[0]
		}
		w.Fingerprint = rec.WorkFingerprint(w, by)
		out.Works = append(out.Works, w)
	}
	e = rows.Err()
	rows.Close()
	return out, e
}
func (s *Store) CreateRecommendationJob(ctx context.Context, id, parent string, ids []string) (*model.Job, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = enhancementGate(ctx, tx, model.EnhancementWork); e != nil {
		return nil, e
	}
	libraries := []string{}
	for _, id := range ids {
		if e = managementGuard(ctx, tx, id); e != nil {
			return nil, e
		}
		var lib string
		if e = scopeRow(ctx, tx, "SELECT library_id FROM editions WHERE id=?", id).Scan(&lib); e != nil {
			return nil, e
		}
		if !slices.Contains(libraries, lib) {
			libraries = append(libraries, lib)
		}
	}
	pool, e := recommendationPool(ctx, tx, libraries)
	if e != nil {
		return nil, e
	}
	by := map[string]rec.InputEdition{}
	for _, v := range pool.Editions {
		by[v.Book.ID] = v
	}
	var parentID any
	if parent != "" {
		parentID = parent
	}
	now := time.Now().Unix()
	library, e := jobLibrary(ctx, tx, ids)
	if e != nil {
		return nil, e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO jobs(id,library_id,type,state,stage,generation,total,parent_job_id,created_at,updated_at) VALUES(?,?,'work_recommendation','queued','processing',1,?,?,?,?)", id, library, len(ids), parentID, now, now); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(pool)
	if e != nil {
		return nil, e
	}
	if len(raw) > 16<<20 || len(raw)+len(pool.Editions)*5300 > rec.MaxMessage-1 {
		return nil, model.ErrCapacity
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO recommendation_batches(job_id,pool_json,algorithm_version,created_at) VALUES(?,?,?,?)", id, string(raw), rec.Algorithm, now); e != nil {
		return nil, e
	}
	for i, bid := range ids {
		v, ok := by[bid]
		if !ok {
			return nil, rec.ErrStale
		}
		input, _ := json.Marshal(v)
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_resources(resource_key,job_id,generation) VALUES(?,?,1)", "recommendation:edition:"+bid, id); e != nil {
			if strings.Contains(e.Error(), "UNIQUE") {
				return nil, model.ErrJobActive
			}
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO job_items(job_id,ordinal,state,edition_id,content_revision,input_json) VALUES(?,?,'pending',?,?,?)", id, i+1, bid, v.ContentRevision, string(input)); e != nil {
			return nil, e
		}
	}
	if e = armEnhancement(ctx, tx, model.EnhancementWork); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.Job(ctx, id)
}
func (s *Store) RecommendationJobs(ctx context.Context) ([]model.Job, error) {
	work, err := enhancementEnabled(ctx, s.Read, model.EnhancementWork)
	if err != nil {
		return nil, err
	}
	series, err := enhancementEnabled(ctx, s.Read, model.EnhancementSeries)
	if err != nil {
		return nil, err
	}
	rows, e := scopeRows(ctx, s.Read, "SELECT "+s.jobColumns()+" FROM jobs WHERE type IN('work_recommendation','series_recommendation') AND state IN('queued','running') AND ((type='work_recommendation' AND ?) OR (type='series_recommendation' AND ?)) ORDER BY created_at,id", work, series)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Job{}
	for rows.Next() {
		j, e := scanJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
func (s *Store) RecommendationPool(ctx context.Context, id string) (rec.Pool, error) {
	var pool rec.Pool
	var raw, algorithm string
	e := scopeRow(ctx, s.Read, "SELECT pool_json,algorithm_version FROM recommendation_batches WHERE job_id=?", id).Scan(&raw, &algorithm)
	if errors.Is(e, sql.ErrNoRows) {
		return pool, model.ErrNotFound
	}
	if e != nil {
		return pool, e
	}
	if algorithm != rec.Algorithm {
		return pool, rec.ErrStale
	}
	e = json.Unmarshal([]byte(raw), &pool)
	return pool, e
}
func recommendationResource(ctx context.Context, tx *sql.Tx, j model.Job, id string) error {
	if e := guard(ctx, tx, j); e != nil {
		return e
	}
	var n int
	e := scopeRow(ctx, tx, "SELECT count(*) FROM job_resources WHERE resource_key=? AND job_id=? AND generation=?", "recommendation:edition:"+id, j.ID, j.Generation).Scan(&n)
	if e != nil {
		return e
	}
	if n != 1 {
		return model.ErrStaleJob
	}
	return managementGuard(ctx, tx, id)
}
func recommendationCurrent(ctx context.Context, q queryer, query string, target string) (rec.InputEdition, rec.InputWork, error) {
	var lib string
	e := scopeRow(ctx, q, "SELECT library_id FROM editions WHERE id=?", query).Scan(&lib)
	if errors.Is(e, sql.ErrNoRows) {
		return rec.InputEdition{}, rec.InputWork{}, rec.ErrStale
	}
	if e != nil {
		return rec.InputEdition{}, rec.InputWork{}, e
	}
	pool, e := recommendationPoolSubset(ctx, q, []string{lib}, query, target)
	if e != nil {
		return rec.InputEdition{}, rec.InputWork{}, e
	}
	var ed rec.InputEdition
	var work rec.InputWork
	for _, v := range pool.Editions {
		if v.Book.ID == query {
			ed = v
			break
		}
	}
	for _, v := range pool.Works {
		if v.ID == target {
			work = v
			break
		}
	}
	if ed.Book.ID == "" || (target != "" && work.ID == "") {
		return ed, work, rec.ErrStale
	}
	return ed, work, nil
}
func (s *Store) SaveRecommendationFeatures(ctx context.Context, j model.Job, input rec.InputEdition, result rec.FeatureResult) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	if e = managementGuard(ctx, tx, input.Book.ID); e != nil {
		return e
	}
	current, _, e := recommendationCurrent(ctx, tx, input.Book.ID, "")
	if e != nil {
		return e
	}
	registryFingerprint := input.Fingerprint
	if j.Mode == "library" {
		if rec.EvidenceFingerprint(current) != rec.EvidenceFingerprint(input) {
			return rec.ErrStale
		}
		registryFingerprint = current.Fingerprint
	} else if current.Fingerprint != input.Fingerprint {
		return rec.ErrStale
	}
	for a, f := range result.Features {
		if _, e = tx.ExecContext(ctx, `INSERT INTO recommendation_features VALUES(?,?,?,?,?,?,?,unixepoch()) ON CONFLICT(edition_id,algorithm) DO UPDATE SET input_fingerprint=excluded.input_fingerprint,feature_fingerprint=excluded.feature_fingerprint,model_sha256=excluded.model_sha256,algorithm_version=excluded.algorithm_version,feature_digest=excluded.feature_digest,updated_at=excluded.updated_at`, input.Book.ID, a, registryFingerprint, f.Fingerprint, f.ModelSHA, f.AlgorithmVersion, f.Digest); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) PublishRecommendations(ctx context.Context, j model.Job, item model.Item, result rec.Result) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if item.BookID == nil {
		return model.ErrInput
	}
	if e = recommendationResource(ctx, tx, j, *item.BookID); e != nil {
		return e
	}
	var state string
	if e = scopeRow(ctx, tx, "SELECT state FROM job_items WHERE job_id=? AND ordinal=?", j.ID, item.Ordinal).Scan(&state); e != nil {
		return e
	}
	if state != "pending" {
		return nil
	}
	var frozen rec.InputEdition
	if e = json.Unmarshal([]byte(item.Input), &frozen); e != nil {
		return e
	}
	var pool rec.Pool
	var raw string
	if e = scopeRow(ctx, tx, "SELECT pool_json FROM recommendation_batches WHERE job_id=?", j.ID).Scan(&raw); e != nil {
		return e
	}
	if e = json.Unmarshal([]byte(raw), &pool); e != nil {
		return e
	}
	targets := map[string]rec.InputWork{}
	for _, w := range pool.Works {
		targets[w.ID] = w
	}
	current, currentWork, e := recommendationCurrent(ctx, tx, *item.BookID, "")
	_ = currentWork
	if e != nil {
		return e
	}
	if current.Fingerprint != frozen.Fingerprint {
		return rec.ErrStale
	}
	live, e := recommendationPool(ctx, tx, []string{current.Book.LibraryID})
	if e != nil {
		return e
	}
	liveWork := map[string]rec.InputWork{}
	for _, w := range live.Works {
		liveWork[w.ID] = w
	}
	if len(result.Items) > 20 || result.Algorithm != rec.Algorithm {
		return model.ErrInput
	}
	for i, c := range result.Items {
		target, ok := targets[c.WorkID]
		if !ok || c.QueryID != *item.BookID || c.WorkID == current.WorkID || liveWork[c.WorkID].Fingerprint != target.Fingerprint {
			return rec.ErrStale
		}
		id, e := model.NewID()
		if e != nil {
			return e
		}
		data, _ := json.Marshal(c)
		if _, e = tx.ExecContext(ctx, "INSERT INTO work_recommendations(id,job_id,query_edition_id,target_work_id,query_fingerprint,target_fingerprint,tier,rank,result_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,unixepoch())", id, j.ID, *item.BookID, c.WorkID, frozen.Fingerprint, target.Fingerprint, c.Tier, i+1, string(data)); e != nil {
			return e
		}
	}
	if e = finishItem(ctx, tx, j, item, "completed", "", nil); e != nil {
		return e
	}
	var summary map[string]any
	var summaryRaw string
	if e = scopeRow(ctx, tx, "SELECT summary_json FROM recommendation_batches WHERE job_id=?", j.ID).Scan(&summaryRaw); e != nil {
		return e
	}
	if e = json.Unmarshal([]byte(summaryRaw), &summary); e != nil {
		return e
	}
	summary[*item.BookID] = map[string]any{"candidate_count": result.CandidateCount, "shown": len(result.Items), "truncated": result.Truncated, "max_results": 20}
	encoded, _ := json.Marshal(summary)
	if _, e = tx.ExecContext(ctx, "UPDATE recommendation_batches SET summary_json=? WHERE job_id=?", string(encoded), j.ID); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *Store) RecommendationIndexState(ctx context.Context) (map[string]any, error) {
	var features, editions int
	e := scopeRow(ctx, s.Read, "SELECT count(*),count(DISTINCT edition_id) FROM recommendation_features").Scan(&features, &editions)
	return map[string]any{"registered_features": features, "registered_editions": editions, "storage": "rebuildable_private_files", "validation": "checked_when_used"}, e
}

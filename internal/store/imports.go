package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path"
	"strings"

	"nyamangaserver.local/server/internal/model"
	rec "nyamangaserver.local/server/internal/recommendation"
)

const importColumns = "id,library_id,stage,state,revision,error_code,created_at,updated_at"

func scanImport(row scanner) (*model.ImportBatch, error) {
	var b model.ImportBatch
	e := row.Scan(&b.ID, &b.LibraryID, &b.Stage, &b.State, &b.Revision, &b.Error, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	return &b, e
}
func reserveImport(ctx context.Context, tx *sql.Tx, batch, stage string, ids []string, url string) (string, error) {
	if feature := importStageFeature(stage); feature != "" {
		flags, e := importEnhancementFlags(ctx, tx, batch)
		if e != nil {
			return "", e
		}
		if !flags[stage] {
			return "", model.ErrEnhancementDisabled
		}
		if e = enhancementGate(ctx, tx, feature); e != nil {
			return "", e
		}
	}
	if e := armEnhancement(ctx, tx, importStageFeature(stage)); e != nil {
		return "", e
	}
	id, e := model.NewID()
	if e != nil {
		return "", e
	}
	raw, _ := json.Marshal(map[string]any{"edition_ids": ids, "gallery_url": url})
	_, e = tx.ExecContext(ctx, "INSERT INTO import_child_requests(job_id,batch_id,stage,input_json) VALUES(?,?,?,?)", id, batch, stage, string(raw))
	return id, e
}
func (s *Store) CreateImport(ctx context.Context, id, library, key string) (*model.ImportBatch, error) {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if old, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE request_key=?", key)); e == nil {
		if old.LibraryID != library {
			return nil, model.ErrConflict
		}
		if e = tx.Rollback(); e != nil {
			return nil, e
		}
		return s.ImportBatch(ctx, old.ID)
	} else if !errors.Is(e, model.ErrNotFound) {
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
	var active int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE library_id=? AND state IN('queued','running','paused')", library).Scan(&active); e != nil {
		return nil, e
	}
	if active > 0 {
		return nil, model.ErrBusy
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO import_batches(id,library_id,request_key) VALUES(?,?,?)", id, library, key); e != nil {
		if strings.Contains(e.Error(), "UNIQUE") {
			return nil, model.ErrBusy
		}
		return nil, e
	}
	if s.Version >= 19 {
		versions, err := enhancementEnabled(ctx, tx, model.EnhancementWork)
		if err != nil {
			return nil, err
		}
		series, err := enhancementEnabled(ctx, tx, model.EnhancementSeries)
		if err != nil {
			return nil, err
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO import_enhancements VALUES(?,?,?)", id, versions, series); e != nil {
			return nil, e
		}
	}
	if _, e = reserveImport(ctx, tx, id, "scan", nil, ""); e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return s.ImportBatch(ctx, id)
}
func (s *Store) ImportBatch(ctx context.Context, id string) (*model.ImportBatch, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", id))
	if e != nil {
		return nil, e
	}
	table, col := "import_members", "metadata_decision"
	if b.Stage == "versions" {
		col = "version_decision"
	}
	if b.Stage == "series" {
		table, col = "import_works", "decision"
	}
	if e = scopeRow(ctx, tx, "SELECT count(*),COALESCE(sum(CASE WHEN "+col+"='' THEN 1 ELSE 0 END),0) FROM "+table+" WHERE batch_id=?", id).Scan(&b.Total, &b.Pending); e != nil {
		return nil, e
	}
	jobQuery := "SELECT " + s.jobColumns() + " FROM jobs WHERE id IN(SELECT job_id FROM import_children WHERE batch_id=?)"
	jobArgs := []any{id}
	if s.Version >= 16 {
		jobQuery += " OR (type='metadata_search' AND state IN('queued','running','paused') AND id IN(SELECT job_id FROM source_searches WHERE batch_id=?))"
		jobArgs = append(jobArgs, id)
	}
	rows, e := scopeRows(ctx, tx, jobQuery+" ORDER BY created_at,id", jobArgs...)
	if e != nil {
		return nil, e
	}
	b.Jobs = []model.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		b.Jobs = append(b.Jobs, *j)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if b.Stage == "metadata" {
		b.Counts, e = importReviewCounts(ctx, tx, id)
		if e != nil {
			return nil, e
		}
	}
	if e = enrichImportOutcome(ctx, tx, b); e != nil {
		return nil, e
	}
	return b, tx.Commit()
}
func (s *Store) ImportBatches(ctx context.Context, page, size int, filters ...model.ImportFilter) (model.ImportPage[model.ImportBatch], error) {
	out := model.ImportPage[model.ImportBatch]{Items: []model.ImportBatch{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	filter := model.ImportFilter{}
	if len(filters) > 0 {
		filter = filters[0]
	}
	where := " FROM import_batches WHERE (?='' OR library_id=?) AND (?='' OR state=?)"
	args := []any{filter.LibraryID, filter.LibraryID, filter.State, filter.State}
	if e = scopeRow(ctx, tx, "SELECT count(*)"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+importColumns+where+" ORDER BY created_at DESC,id LIMIT ? OFFSET ?", append(args, size, (page-1)*size)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		b, err := scanImport(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		out.Items = append(out.Items, *b)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for i := range out.Items {
		if e = enrichImportOutcome(ctx, tx, &out.Items[i]); e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}
func (s *Store) ImportRequests(ctx context.Context) ([]model.ImportRequest, error) {
	rows, e := scopeRows(ctx, s.Read, `SELECT r.job_id,r.batch_id,b.library_id,r.stage,r.input_json FROM import_child_requests r JOIN import_batches b ON b.id=r.batch_id WHERE b.state='processing' AND b.stage=r.stage AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=r.job_id) AND (r.stage='metadata' OR r.rowid=(SELECT max(rowid) FROM import_child_requests WHERE batch_id=r.batch_id AND stage=r.stage)) ORDER BY r.created_at,r.job_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.ImportRequest{}
	for rows.Next() {
		var r model.ImportRequest
		var raw string
		if e = rows.Scan(&r.JobID, &r.BatchID, &r.LibraryID, &r.Stage, &raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &r); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Existing worker creators commit their inputs and import association atomically
// through import_link_job. A reserved identity is never changed on recovery.
func (s *Store) MaterializeImport(ctx context.Context, r model.ImportRequest) error {
	var batch string
	if e := s.Read.QueryRowContext(ctx, "SELECT batch_id FROM import_child_requests WHERE job_id=?", r.JobID).Scan(&batch); e != nil {
		return e
	}
	var e error
	ctx, e = s.importContext(ctx, batch)
	if e != nil {
		return e
	}
	switch r.Stage {
	case "scan":
		_, e = s.StartScan(ctx, r.JobID, r.LibraryID)
	case "metadata":
		inputs := map[string]model.MetadataInput{}
		for _, id := range r.IDs {
			inputs[id] = model.MetadataInput{URL: r.URL, Force: r.URL != ""}
		}
		_, e = s.CreateMetadataJob(ctx, r.JobID, "", r.IDs, inputs)
	case "versions":
		_, e = s.CreateDiscoveryJob(ctx, r.JobID, r.LibraryID)
	case "series":
		_, e = s.CreateSeriesDiscovery(ctx, r.JobID, r.LibraryID, "")
	default:
		return model.ErrInput
	}
	return e
}
func importTouch(ctx context.Context, tx *sql.Tx, id string) error {
	_, e := tx.ExecContext(ctx, "UPDATE import_batches SET revision=revision+1,updated_at=unixepoch() WHERE id=?", id)
	return e
}
func importScanMember(ctx context.Context, tx *sql.Tx, j model.Job, b model.Edition) error {
	var batch string
	e := scopeRow(ctx, tx, "SELECT c.batch_id FROM import_children c JOIN import_batches b ON b.id=c.batch_id WHERE c.job_id=? AND c.stage='scan' AND b.state='processing'", j.ID).Scan(&batch)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	var imported int64
	err := scopeRow(ctx, tx, "SELECT content_revision FROM file_import_state WHERE edition_id=?", b.ID).Scan(&imported)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && imported == b.ContentRevision {
		_, e = tx.ExecContext(ctx, "UPDATE import_members SET scan_error=?,content_revision=? WHERE batch_id=? AND edition_id=?", b.Error, b.ContentRevision, batch, b.ID)
		return e
	}
	kind := "new"
	if err == nil {
		kind = "changed"
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision,scan_error) VALUES(?,?,?,?,?) ON CONFLICT(batch_id,edition_id) DO UPDATE SET content_revision=excluded.content_revision,scan_error=excluded.scan_error`, batch, b.ID, kind, b.ContentRevision, b.Error)
	return e
}
func importMetadataToken(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var content, selection int64
	var sha, algorithm, source, normalized string
	e := scopeRow(ctx, tx, `SELECT f.content_revision,COALESCE(m.selection_revision,1),COALESCE(f.representative_sha1,''),f.representative_algorithm,COALESCE(s.metadata_id,''),COALESCE(x.normalized_json,'') FROM files f LEFT JOIN edition_metadata_state m ON m.edition_id=f.edition_id LEFT JOIN edition_sources s ON s.edition_id=f.edition_id LEFT JOIN external_metadata x ON x.id=s.metadata_id WHERE f.edition_id=?`, id).Scan(&content, &selection, &sha, &algorithm, &source, &normalized)
	return rec.Fingerprint([]any{content, selection, sha, algorithm, source, normalized}), e
}
func importOrganizationToken(ctx context.Context, tx *sql.Tx, id string, workOnly bool) (string, error) {
	var raw string
	var e error
	if workOnly {
		e = scopeRow(ctx, tx, `SELECT json_array(w.id,w.revision,COALESCE(sm.series_id,''),COALESCE(s.revision,0), (SELECT json_group_array(id) FROM (SELECT id FROM editions WHERE work_id=w.id ORDER BY id))) FROM works w LEFT JOIN series_members sm ON sm.work_id=w.id LEFT JOIN series s ON s.id=sm.series_id WHERE w.id=?`, id).Scan(&raw)
	} else {
		e = scopeRow(ctx, tx, `SELECT json_array(e.id,e.work_id,w.preferred_edition_id,(SELECT json_group_array(id) FROM (SELECT id FROM editions WHERE work_id=w.id ORDER BY id))) FROM editions e JOIN works w ON w.id=e.work_id LEFT JOIN series_members sm ON sm.work_id=w.id LEFT JOIN series s ON s.id=sm.series_id WHERE e.id=?`, id).Scan(&raw)
	}
	return rec.Fingerprint(raw), e
}
func (s *Store) ImportMembers(ctx context.Context, batch, filter string, page, size int, stages ...string) (model.ImportPage[model.ImportMember], error) {
	out := model.ImportPage[model.ImportMember]{Items: []model.ImportMember{}, Page: page, PageSize: size}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	b, e := scanImport(scopeRow(ctx, tx, "SELECT "+importColumns+" FROM import_batches WHERE id=?", batch))
	if e != nil {
		return out, e
	}
	if len(stages) > 0 && stages[0] != "" {
		switch stages[0] {
		case "scan", "metadata", "versions", "series", "finished":
			b.Stage = stages[0]
		default:
			return out, model.ErrInput
		}
	}
	col := "metadata_decision"
	if b.Stage == "versions" {
		col = "version_decision"
	}
	from := ` FROM import_members m JOIN editions e ON e.id=m.edition_id JOIN works w ON w.id=e.work_id JOIN files f ON f.edition_id=e.id LEFT JOIN effective_editions p ON p.edition_id=e.id LEFT JOIN series_members sm ON sm.work_id=e.work_id WHERE m.batch_id=?`
	if b.Stage == "series" {
		from = ` FROM import_works m JOIN works w ON w.id=m.work_id JOIN editions e ON e.id=w.preferred_edition_id JOIN files f ON f.edition_id=e.id LEFT JOIN effective_editions p ON p.edition_id=e.id LEFT JOIN series_members sm ON sm.work_id=w.id WHERE m.batch_id=?`
		col = "decision"
	}
	from = strings.Replace(from, " WHERE m.batch_id=?", " LEFT JOIN files wf ON wf.edition_id=w.preferred_edition_id LEFT JOIN effective_editions wp ON wp.edition_id=w.preferred_edition_id WHERE m.batch_id=?", 1)
	switch filter {
	case "pending":
		from += " AND m." + col + "=''"
	case "errors":
		if b.Stage != "series" {
			from += " AND (m.scan_error!='' OR m.fetch_error!='')"
		}
	case "", "all":
	default:
		return out, model.ErrInput
	}
	if e = scopeRow(ctx, tx, "SELECT count(*)"+from, batch).Scan(&out.Total); e != nil {
		return out, e
	}
	columns := "m.edition_id,f.relative_path,COALESCE(p.title,f.filename_title),m.change_kind,CASE WHEN m.scan_error!='' THEN m.scan_error ELSE m.fetch_error END,m." + col + ",m.revision,f.content_revision,e.work_id,COALESCE(sm.series_id,''),m.scan_error"
	if b.Stage == "series" {
		columns = "m.work_id,f.relative_path,COALESCE(w.name_override,p.title,f.filename_title),'','',m.decision,m.revision,f.content_revision,w.id,COALESCE(sm.series_id,''),''"
	}
	columns += " ,e.id,e.revision,COALESCE((SELECT revision FROM cover_selections cs WHERE cs.edition_id=e.id),0),EXISTS(SELECT 1 FROM edition_sources es WHERE es.edition_id=e.id) OR EXISTS(SELECT 1 FROM field_overrides fo WHERE fo.edition_id=e.id)"
	if b.Stage == "series" {
		columns += ",''"
	} else {
		columns += ",COALESCE((SELECT json_extract(request.input_json,'$.gallery_url') FROM import_child_requests request WHERE request.job_id=m.active_job_id),'')"
	}
	columns += ",COALESCE(json_extract(p.fields_json,'$.title_original'),''),COALESCE(w.name_override,wp.title,wf.filename_title),COALESCE((SELECT name FROM series WHERE id=sm.series_id),'')"
	rows, e := scopeRows(ctx, tx, "SELECT "+columns+from+" ORDER BY f.relative_path,e.id LIMIT ? OFFSET ?", batch, size, (page-1)*size)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v model.ImportMember
		if e = rows.Scan(&v.ID, &v.Filename, &v.Title, &v.Change, &v.Error, &v.Decision, &v.Revision, &v.ContentRevision, &v.WorkID, &v.SeriesID, &v.ScanError, &v.EditionID, &v.EditionRevision, &v.CoverRevision, &v.HasMetadata, &v.RequestedURL, &v.TitleOriginal, &v.WorkName, &v.SeriesName); e != nil {
			rows.Close()
			return out, e
		}
		v.Filename = path.Base(v.Filename)
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if b.Stage == "metadata" {
		if e = enrichImportReview(ctx, tx, batch, out.Items); e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}

func (s *Store) ImportJobBatch(ctx context.Context, job string) (string, error) {
	if s.Version < 12 {
		return "", nil
	}
	var id string
	e := scopeRow(ctx, s.Read, "SELECT batch_id FROM import_children WHERE job_id=?", job).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) && s.Version >= 16 {
		e = scopeRow(ctx, s.Read, "SELECT s.batch_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE r.job_id=?", job).Scan(&id)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	return id, e
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"strings"
	"time"
	"unicode/utf8"
)

const sourceSearchColumns = `s.id,s.batch_id,s.edition_id,s.mode,s.query,s.site,s.revision,COALESCE(s.job_id,''),CASE WHEN j.failed>0 AND j.state='completed' THEN 'failed' ELSE COALESCE(j.state,'completed') END,COALESCE(NULLIF(j.error_code,''),(SELECT NULLIF(error_code,'') FROM job_items WHERE job_id=j.id AND error_code!='' LIMIT 1),''),s.continuation,s.input_json,s.content_revision,s.selection_revision,s.edition_revision,s.member_revision,s.next_cursor,COALESCE((SELECT page FROM source_search_requests WHERE job_id=s.job_id),1),(SELECT count(*) FROM source_search_results WHERE search_id=s.id)`

func scanSourceSearch(row interface{ Scan(...any) error }) (model.SourceSearch, error) {
	var v model.SourceSearch
	var raw string
	e := row.Scan(&v.ID, &v.BatchID, &v.EditionID, &v.Mode, &v.Query, &v.Site, &v.Revision, &v.JobID, &v.State, &v.Error, &v.Continuation, &raw, &v.ContentRevision, &v.SelectionRevision, &v.EditionRevision, &v.MemberRevision, &v.Next, &v.ActivePage, &v.Loaded)
	if e != nil {
		return v, importNotFound(e)
	}
	e = json.Unmarshal([]byte(raw), &v.Input)
	return v, e
}
func sourceSearchTx(ctx context.Context, tx *sql.Tx, batch, id, search string) (model.SourceSearch, error) {
	return scanSourceSearch(scopeRow(ctx, tx, `SELECT `+sourceSearchColumns+` FROM source_searches s LEFT JOIN jobs j ON j.id=s.job_id WHERE s.id=? AND s.batch_id=? AND s.edition_id=?`, search, batch, id))
}

// Guards all publication/selection against the frozen file and source identity,
// without forcing a whole member revision change for each compared gallery.
func searchGuard(ctx context.Context, tx *sql.Tx, v model.SourceSearch) error {
	var stage, state, decision string
	var content, selection int64
	var active *string
	e := scopeRow(ctx, tx, `SELECT b.stage,b.state,m.metadata_decision,f.content_revision,COALESCE(d.selection_revision,1),m.active_job_id FROM import_members m JOIN import_batches b ON b.id=m.batch_id JOIN files f ON f.edition_id=m.edition_id LEFT JOIN edition_metadata_state d ON d.edition_id=m.edition_id WHERE m.batch_id=? AND m.edition_id=?`, v.BatchID, v.EditionID).Scan(&stage, &state, &decision, &content, &selection, &active)
	if e != nil {
		return importNotFound(e)
	}
	if stage != "metadata" || (state != "processing" && state != "review") || decision != "" || content != v.ContentRevision || selection != v.SelectionRevision {
		return model.ErrConflict
	}
	if e = managementGuard(ctx, tx, v.EditionID); e != nil {
		return e
	}
	var input model.MetadataInput
	input = v.Input
	itemRaw, _ := json.Marshal(input)
	ok, e := lookupMatches(ctx, tx, model.Item{BookID: &v.EditionID, Input: string(itemRaw)})
	if e != nil {
		return e
	}
	if !ok {
		return model.ErrConflict
	}
	return nil
}
func (s *Store) CreateSourceSearch(ctx context.Context, batch, id, site string, in model.SourceSearchInput) (model.SourceSearch, error) {
	var out model.SourceSearch
	in.Query = strings.TrimSpace(in.Query)
	if (in.Mode != "matches" && in.Mode != "title") || (site != "eh" && site != "ex") || len(in.RequestKey) < 1 || len(in.RequestKey) > 128 || in.ExpectedRevision < 1 || len(in.Query) > 1024 || !utf8.ValidString(in.Query) || utf8.RuneCountInString(in.Query) > 256 || in.Mode == "title" && in.Query == "" {
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out, e = scanSourceSearch(scopeRow(ctx, tx, `SELECT `+sourceSearchColumns+` FROM source_searches s LEFT JOIN jobs j ON j.id=s.job_id WHERE s.batch_id=? AND s.edition_id=? AND s.request_key=?`, batch, id, in.RequestKey))
	if e == nil {
		if out.Mode != in.Mode || out.Mode == "title" && out.Query != in.Query || out.Site != site || out.MemberRevision != in.ExpectedRevision {
			return out, model.ErrConflict
		}
		return out, tx.Commit()
	}
	if !errors.Is(e, model.ErrNotFound) {
		return out, e
	}
	out = model.SourceSearch{BatchID: batch, EditionID: id, Mode: in.Mode, Query: in.Query, Site: site, MemberRevision: in.ExpectedRevision}
	var sha *string
	var algorithm string
	var actual int64
	e = scopeRow(ctx, tx, `SELECT f.content_revision,COALESCE(d.selection_revision,1),ed.revision,m.revision,f.representative_sha1,f.representative_algorithm FROM import_members m JOIN files f ON f.edition_id=m.edition_id JOIN editions ed ON ed.id=m.edition_id LEFT JOIN edition_metadata_state d ON d.edition_id=m.edition_id WHERE m.batch_id=? AND m.edition_id=?`, batch, id).Scan(&out.ContentRevision, &out.SelectionRevision, &out.EditionRevision, &actual, &sha, &algorithm)
	if e != nil {
		return out, importNotFound(e)
	}
	if actual != in.ExpectedRevision {
		return out, model.ErrConflict
	}
	out.Input = model.MetadataInput{LookupSHA1: sha, LookupAlgorithm: algorithm}
	if e = searchGuard(ctx, tx, out); e != nil {
		return out, e
	}
	if in.Mode == "matches" {
		if sha == nil {
			return out, &model.RemoteError{Code: "no_sha1"}
		}
		out.Query = *sha
	}
	out.ID, e = model.NewID()
	if e != nil {
		return out, e
	}
	raw, _ := json.Marshal(out.Input)
	if _, e = tx.ExecContext(ctx, `INSERT INTO source_searches(id,batch_id,edition_id,mode,query,site,request_key,input_json,content_revision,selection_revision,edition_revision,member_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, out.ID, batch, id, out.Mode, out.Query, site, in.RequestKey, string(raw), out.ContentRevision, out.SelectionRevision, out.EditionRevision, out.MemberRevision); e != nil {
		return out, e
	}
	if in.Mode == "matches" {
		var saved, savedInput string
		var savedContent, savedSelection int64
		e = scopeRow(ctx, tx, `SELECT i.candidates_json,i.input_json,i.content_revision,i.selection_revision FROM import_members m JOIN job_items i ON i.job_id=m.active_job_id AND i.edition_id=m.edition_id WHERE m.batch_id=? AND m.edition_id=?`, batch, id).Scan(&saved, &savedInput, &savedContent, &savedSelection)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		var seeds []model.Candidate
		if saved != "" {
			var prior model.MetadataInput
			if json.Unmarshal([]byte(savedInput), &prior) != nil {
				return out, model.ErrInput
			}
			if savedContent != out.ContentRevision || savedSelection != out.SelectionRevision || prior.LookupAlgorithm != out.Input.LookupAlgorithm || prior.LookupSHA1 == nil || out.Input.LookupSHA1 == nil || *prior.LookupSHA1 != *out.Input.LookupSHA1 {
				saved = ""
			}
		}
		if saved != "" {
			if json.Unmarshal([]byte(saved), &seeds) != nil {
				return out, model.ErrInput
			}
		}
		if _, e = insertSearchResults(ctx, tx, out.ID, seeds); e != nil {
			return out, e
		}
	}
	if _, e = queueSearch(ctx, tx, out, 1, "initial"); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return s.SourceSearch(ctx, batch, id, out.ID)
}
func insertSearchResults(ctx context.Context, tx *sql.Tx, search string, items []model.Candidate) (int, error) {
	var count int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM source_search_results WHERE search_id=?", search).Scan(&count); e != nil {
		return 0, e
	}
	for _, v := range items {
		url, gid, _, e := provider.Gallery(v.URL)
		if e != nil || len(v.Title) > 2048 {
			return count, model.ErrInput
		}
		var exists int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM source_search_results WHERE search_id=? AND gallery_id=?", search, gid).Scan(&exists); e != nil {
			return count, e
		}
		if exists > 0 {
			continue
		}
		if count >= 200 {
			return count, nil
		}
		id, e := model.NewID()
		if e != nil {
			return count, e
		}
		count++
		if _, e = tx.ExecContext(ctx, "INSERT INTO source_search_results(id,search_id,url,gallery_id,title,position) VALUES(?,?,?,?,?,?)", id, search, url, gid, v.Title, count); e != nil {
			return count, e
		}
	}
	return count, nil
}
func queueSearch(ctx context.Context, tx *sql.Tx, v model.SourceSearch, page int, key string) (string, error) {
	var old string
	var oldPage int
	e := scopeRow(ctx, tx, `SELECT job_id,page FROM source_search_requests WHERE search_id=? AND request_key=?`, v.ID, key).Scan(&old, &oldPage)
	if e == nil {
		if page != oldPage {
			return "", model.ErrConflict
		}
		return old, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return "", e
	}
	// A new query supersedes any old search IO for this member, but never ordinary
	// metadata tasks. SQL generations prevent an in-flight response from publishing.
	if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=unixepoch() WHERE type='metadata_search' AND id IN(SELECT r.job_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE s.batch_id=? AND s.edition_id=?) AND state IN('queued','running','paused')`, v.BatchID, v.EditionID); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM job_resources WHERE resource_key=?`, "source-search:"+v.BatchID+":"+v.EditionID); e != nil {
		return "", e
	}
	job, e := model.NewID()
	if e != nil {
		return "", e
	}
	now := time.Now().Unix()
	library, e := jobLibrary(ctx, tx, []string{v.EditionID})
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,library_id,type,state,stage,generation,total,created_at,updated_at) VALUES(?,?,'metadata_search','queued','processing',1,1,?,?)`, job, library, now, now); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO job_resources VALUES(?,?,1)", "source-search:"+v.BatchID+":"+v.EditionID, job); e != nil {
		return "", e
	}
	raw, _ := json.Marshal(model.SearchOperation{SearchID: v.ID, Page: page, SeedOnly: key == "initial"})
	if _, e = tx.ExecContext(ctx, `INSERT INTO job_items(job_id,ordinal,state,edition_id,input_json) VALUES(?,1,'pending',?,?)`, job, v.EditionID, string(raw)); e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, "INSERT INTO source_search_requests(search_id,request_key,page,job_id) VALUES(?,?,?,?)", v.ID, key, page, job); e != nil {
		return "", e
	}
	_, e = tx.ExecContext(ctx, "UPDATE source_searches SET job_id=?,revision=revision+1 WHERE id=?", job, v.ID)
	return job, e
}
func (s *Store) SourceSearch(ctx context.Context, batch, id, search string) (model.SourceSearch, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.SourceSearch{}, e
	}
	defer tx.Rollback()
	v, e := sourceSearchTx(ctx, tx, batch, id, search)
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func scanSearchResult(row interface{ Scan(...any) error }) (model.SourceSearchResult, error) {
	v := model.SourceSearchResult{Tags: []model.Tag{}}
	var raw *string
	e := row.Scan(&v.ID, &v.URL, &v.Title, &v.Position, &v.Revision, &v.State, &v.Error, &raw)
	if e != nil {
		return v, importNotFound(e)
	}
	if raw != nil {
		var r model.RemoteMetadata
		if e = json.Unmarshal([]byte(*raw), &r); e != nil {
			return v, e
		}
		v.Result = &r
		v.Fields = &r.Fields
		var extra struct {
			Count json.RawMessage `json:"filecount"`
		}
		if json.Unmarshal(r.Raw, &extra) == nil {
			var str string
			var n int
			if json.Unmarshal(extra.Count, &str) == nil {
				if json.Unmarshal([]byte(str), &n) == nil && n >= 0 {
					v.PageCount = &n
				}
			} else if json.Unmarshal(extra.Count, &n) == nil && n >= 0 {
				v.PageCount = &n
			}
		}
	}
	return v, nil
}

const searchResultColumns = "id,url,title,position,revision,state,error_code,result_json"

func (s *Store) SourceSearchResults(ctx context.Context, batch, id, search string, page int) (model.SourceSearchPage, error) {
	out := model.SourceSearchPage{Items: []model.SourceSearchResult{}, Page: page, PageSize: 10}
	if page < 1 || page > 20 {
		return out, model.ErrInput
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Search, e = sourceSearchTx(ctx, tx, batch, id, search)
	if e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, "SELECT "+searchResultColumns+" FROM source_search_results WHERE search_id=? ORDER BY position LIMIT 10 OFFSET ?", search, (page-1)*10)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		v, e := scanSearchResult(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	rows.Close()
	if e = enrichSearchTags(ctx, tx, out.Items); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) SourceSearchResult(ctx context.Context, batch, id, search, result string) (model.SourceSearchResult, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.SourceSearchResult{}, e
	}
	defer tx.Rollback()
	if _, e = sourceSearchTx(ctx, tx, batch, id, search); e != nil {
		return model.SourceSearchResult{}, e
	}
	v, e := scanSearchResult(scopeRow(ctx, tx, "SELECT "+searchResultColumns+" FROM source_search_results WHERE search_id=? AND id=?", search, result))
	if e != nil {
		return v, e
	}
	rows := []model.SourceSearchResult{v}
	if e = enrichSearchTags(ctx, tx, rows); e != nil {
		return v, e
	}
	return rows[0], tx.Commit()
}
func enrichSearchTags(ctx context.Context, tx *sql.Tx, rows []model.SourceSearchResult) error {
	names := []model.TagName{}
	seen := map[model.TagName]bool{}
	for _, r := range rows {
		if r.Fields != nil {
			for _, t := range r.Fields.Tags {
				n := model.CanonicalTag(t)
				if !seen[n] {
					seen[n] = true
					names = append(names, n)
				}
			}
		}
	}
	tags, e := importDisplayTags(ctx, tx, names)
	if e != nil {
		return e
	}
	known := map[model.TagName]model.Tag{}
	for _, t := range tags {
		known[model.TagName{Namespace: t.Namespace, Name: t.Name}] = t
	}
	for i := range rows {
		if rows[i].Fields != nil {
			for _, t := range rows[i].Fields.Tags {
				rows[i].Tags = append(rows[i].Tags, known[model.CanonicalTag(t)])
			}
		}
	}
	return nil
}

func (s *Store) LoadSourceSearch(ctx context.Context, batch, id, search string, in model.SearchLoad) (model.SourceSearch, error) {
	var out model.SourceSearch
	if in.Page < 1 || in.Page > 20 || in.ExpectedRevision < 1 || len(in.RequestKey) < 1 || len(in.RequestKey) > 128 {
		return out, model.ErrInput
	}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out, e = sourceSearchTx(ctx, tx, batch, id, search)
	if e != nil {
		return out, e
	}
	var previous string
	var page int
	e = scopeRow(ctx, tx, "SELECT job_id,page FROM source_search_requests WHERE search_id=? AND request_key=?", search, in.RequestKey).Scan(&previous, &page)
	if e == nil {
		if page != in.Page {
			return out, model.ErrConflict
		}
		if out.JobID != previous {
			return out, model.ErrConflict
		}
		return out, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if out.Revision != in.ExpectedRevision {
		return out, model.ErrConflict
	}
	if e = searchGuard(ctx, tx, out); e != nil {
		return out, e
	}
	if out.State == "queued" || out.State == "running" || out.State == "paused" {
		return out, model.ErrBusy
	}
	if (out.Continuation == "end" || out.Continuation == "limited") && in.Page > (out.Loaded+9)/10 {
		return out, model.ErrInput
	}
	if in.Page > (out.Loaded+9)/10+1 {
		return out, model.ErrInput
	}
	if _, e = queueSearch(ctx, tx, out, in.Page, in.RequestKey); e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return s.SourceSearch(ctx, batch, id, search)
}
func (s *Store) ValidateSearchJob(ctx context.Context, j model.Job, op model.SearchOperation) (model.SourceSearch, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return model.SourceSearch{}, e
	}
	defer tx.Rollback()
	var batch, id string
	e = scopeRow(ctx, tx, "SELECT batch_id,edition_id FROM source_searches WHERE id=? AND job_id=?", op.SearchID, j.ID).Scan(&batch, &id)
	if e != nil {
		return model.SourceSearch{}, model.ErrStaleJob
	}
	v, e := sourceSearchTx(ctx, tx, batch, id, op.SearchID)
	if e != nil {
		return v, e
	}
	if e = guard(ctx, tx, j); e != nil {
		return v, e
	}
	if e = searchGuard(ctx, tx, v); e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (s *Store) PublishSearchPage(ctx context.Context, j model.Job, op model.SearchOperation, page model.SearchPage) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var batch, id string
	if e = scopeRow(ctx, tx, "SELECT batch_id,edition_id FROM source_searches WHERE id=? AND job_id=?", op.SearchID, j.ID).Scan(&batch, &id); e != nil {
		return model.ErrStaleJob
	}
	v, e := sourceSearchTx(ctx, tx, batch, id, op.SearchID)
	if e != nil {
		return e
	}
	if e = searchGuard(ctx, tx, v); e != nil {
		return e
	}
	count, e := insertSearchResults(ctx, tx, v.ID, page.Items)
	if e != nil {
		return e
	}
	continuation := "end"
	if page.Next != "" {
		continuation = "more"
	}
	if count >= 200 {
		continuation = "limited"
	}
	if _, e = tx.ExecContext(ctx, "UPDATE source_searches SET next_cursor=?,continuation=?,revision=revision+1 WHERE id=?", page.Next, continuation, v.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) PublishSearchResults(ctx context.Context, j model.Job, op model.SearchOperation, results []model.FetchResult) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = guard(ctx, tx, j); e != nil {
		return e
	}
	var batch, id string
	if e = scopeRow(ctx, tx, "SELECT batch_id,edition_id FROM source_searches WHERE id=? AND job_id=?", op.SearchID, j.ID).Scan(&batch, &id); e != nil {
		return model.ErrStaleJob
	}
	v, e := sourceSearchTx(ctx, tx, batch, id, op.SearchID)
	if e != nil {
		return e
	}
	if e = searchGuard(ctx, tx, v); e != nil {
		return e
	}
	for _, r := range results {
		canonical, gid, _, err := provider.Gallery(r.URL)
		if err != nil || canonical != r.URL || len(r.Error) > 128 {
			return model.ErrInput
		}
		if r.Metadata != nil && r.Metadata.ExternalID != gid {
			return model.ErrInput
		}
		var raw any
		state := "failed"
		if r.Metadata != nil {
			if r.Metadata.URL != r.URL || len(r.Metadata.Raw) > 4<<20 || !json.Valid(r.Metadata.Raw) || model.ValidateMetadata(r.Metadata.Fields) != nil {
				return model.ErrInput
			}
			data, _ := json.Marshal(r.Metadata)
			raw = string(data)
			state = "ready"
		}
		var bytes int
		if e = scopeRow(ctx, tx, "SELECT COALESCE(sum(length(CAST(result_json AS BLOB))),0) FROM source_search_results WHERE search_id=? AND url!=?", v.ID, r.URL).Scan(&bytes); e != nil {
			return e
		}
		if str, ok := raw.(string); ok && bytes+len(str) > 32<<20 {
			raw = nil
			state = "failed"
			r.Error = "search_limit_reached"
			if _, e = tx.ExecContext(ctx, "UPDATE source_searches SET continuation='limited' WHERE id=?", v.ID); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, "UPDATE source_search_results SET state=?,error_code=?,result_json=?,revision=revision+1 WHERE search_id=? AND url=?", state, r.Error, raw, v.ID, r.URL); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "UPDATE source_searches SET revision=revision+1 WHERE id=?", v.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Store) SelectSourceSearch(ctx context.Context, batch, id, search string, in model.SearchSelect) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v, e := sourceSearchTx(ctx, tx, batch, id, search)
	if e != nil {
		return e
	}
	var selected string
	var savedCandidate int64
	if e = scopeRow(ctx, tx, "SELECT selected_result,selected_candidate_revision FROM source_searches WHERE id=?", search).Scan(&selected, &savedCandidate); e != nil {
		return e
	}
	// Replay is idempotent only while this exact candidate remains staged.
	if selected == in.ResultID {
		var current string
		var currentRevision int64
		if e = scopeRow(ctx, tx, "SELECT json_extract(result_json,'$.URL'),revision FROM import_candidates WHERE batch_id=? AND edition_id=?", batch, id).Scan(&current, &currentRevision); e == nil && currentRevision == savedCandidate {
			r, e := scanSearchResult(scopeRow(ctx, tx, "SELECT "+searchResultColumns+" FROM source_search_results WHERE search_id=? AND id=?", search, in.ResultID))
			if e == nil && r.URL == current && r.Revision == in.ResultRevision {
				return tx.Commit()
			}
		}
		return model.ErrConflict
	}
	var latest string
	if e = scopeRow(ctx, tx, "SELECT id FROM source_searches WHERE batch_id=? AND edition_id=? ORDER BY rowid DESC LIMIT 1", batch, id).Scan(&latest); e != nil {
		return e
	}
	if latest != search {
		return model.ErrConflict
	}
	if v.Revision != in.ExpectedRevision || in.MemberRevision < 1 || in.EditionRevision < 1 {
		return model.ErrConflict
	}
	if e = searchGuard(ctx, tx, v); e != nil {
		return e
	}
	var member, edition, generation int64
	var active *string
	e = scopeRow(ctx, tx, "SELECT m.revision,ed.revision,m.request_generation,m.active_job_id FROM import_members m JOIN editions ed ON ed.id=m.edition_id WHERE m.batch_id=? AND m.edition_id=?", batch, id).Scan(&member, &edition, &generation, &active)
	if e != nil {
		return e
	}
	if member != in.MemberRevision || edition != in.EditionRevision {
		return model.ErrConflict
	}
	var busy int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM jobs WHERE id=? AND state IN('queued','running','paused')", active).Scan(&busy); e != nil {
		return e
	}
	if busy > 0 {
		return model.ErrBusy
	}
	r, e := scanSearchResult(scopeRow(ctx, tx, "SELECT "+searchResultColumns+" FROM source_search_results WHERE search_id=? AND id=?", search, in.ResultID))
	if e != nil {
		return e
	}
	if r.Revision != in.ResultRevision || r.State != "ready" || r.Result == nil {
		return model.ErrConflict
	}
	result := *r.Result
	result.Method = "manual"
	input := v.Input
	input.URL = r.URL
	input.Force = true
	raw, _ := json.Marshal(result)
	inputRaw, _ := json.Marshal(input)
	if _, e = tx.ExecContext(ctx, `INSERT INTO import_candidates(batch_id,edition_id,request_generation,content_revision,selection_revision,input_json,result_json) VALUES(?,?,?,?,?,?,?) ON CONFLICT(batch_id,edition_id) DO UPDATE SET revision=import_candidates.revision+1,request_generation=excluded.request_generation,content_revision=excluded.content_revision,selection_revision=excluded.selection_revision,input_json=excluded.input_json,result_json=excluded.result_json,created_at=unixepoch()`, batch, id, generation+1, v.ContentRevision, v.SelectionRevision, string(inputRaw), string(raw)); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE import_members SET revision=revision+1,request_generation=request_generation+1,fetch_error='',metadata_decision='',metadata_token='' WHERE batch_id=? AND edition_id=?", batch, id); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE source_searches SET selected_result=?,selected_candidate_revision=(SELECT revision FROM import_candidates WHERE batch_id=source_searches.batch_id AND edition_id=source_searches.edition_id),revision=revision+1 WHERE id=?", r.ID, search); e != nil {
		return e
	}
	if e = importTouch(ctx, tx, batch); e != nil {
		return e
	}
	return tx.Commit()
}

func controlImportSearches(ctx context.Context, tx *sql.Tx, batch, action string) error {
	var exists int
	if e := scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='source_searches'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	switch action {
	case "pause":
		if _, e := tx.ExecContext(ctx, `UPDATE source_search_requests SET batch_paused=1 WHERE search_id IN(SELECT id FROM source_searches WHERE batch_id=?) AND job_id IN(SELECT id FROM jobs WHERE state IN('queued','running'))`, batch); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE jobs SET state='paused',generation=generation+1,updated_at=unixepoch() WHERE id IN(SELECT r.job_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE s.batch_id=? AND r.batch_paused=1) AND state IN('queued','running')`, batch)
		return e
	case "resume":
		if _, e := tx.ExecContext(ctx, `UPDATE job_items SET attempt=0 WHERE state='pending' AND job_id IN(SELECT r.job_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE s.batch_id=? AND r.batch_paused=1)`, batch); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, `UPDATE jobs SET state='queued',generation=generation+1,error_code='',updated_at=unixepoch() WHERE id IN(SELECT r.job_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE s.batch_id=? AND r.batch_paused=1) AND state='paused'`, batch); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `UPDATE source_search_requests SET batch_paused=0 WHERE search_id IN(SELECT id FROM source_searches WHERE batch_id=?)`, batch)
		return e
	case "cancel", "recheck":
		if _, e := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=unixepoch() WHERE id IN(SELECT r.job_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE s.batch_id=?) AND state IN('queued','running','paused')`, batch); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `DELETE FROM job_resources WHERE job_id IN(SELECT r.job_id FROM source_search_requests r JOIN source_searches s ON s.id=r.search_id WHERE s.batch_id=?)`, batch)
		return e
	}
	return nil
}

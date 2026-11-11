package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
)

// This CTE carries no candidate fields or raw upstream responses. All predicates
// run in the same read snapshot as the count and the frozen review identities.
const importReviewSource = `WITH review AS (
 SELECT m.edition_id AS id,m.revision,e.revision AS edition_revision,COALESCE(c.revision,0) AS candidate_revision,
 m.metadata_decision AS decision,
 EXISTS(SELECT 1 FROM edition_sources source WHERE source.edition_id=e.id) OR EXISTS(SELECT 1 FROM field_overrides overrides WHERE overrides.edition_id=e.id) AS has_metadata,
 CASE WHEN m.metadata_decision!='' THEN 'reviewed'
 WHEN EXISTS(SELECT 1 FROM import_child_requests r WHERE r.job_id=m.active_job_id AND NOT EXISTS(SELECT 1 FROM jobs j WHERE j.id=r.job_id))
 OR EXISTS(SELECT 1 FROM job_items i JOIN jobs j ON j.id=i.job_id WHERE i.job_id=m.active_job_id AND i.edition_id=m.edition_id AND i.state='pending' AND j.state IN('queued','running','paused')) THEN 'fetching'
 WHEN m.scan_error!='' OR f.file_state!='available' OR e.trashed_at IS NOT NULL OR l.disabled OR l.trashed_at IS NOT NULL THEN 'file_error'
 WHEN c.edition_id IS NOT NULL AND (c.content_revision!=f.content_revision OR c.selection_revision!=COALESCE(ms.selection_revision,1)
 OR NOT(CASE WHEN COALESCE(json_extract(c.input_json,'$.lookup_algorithm'),'')='' THEN f.representative_algorithm='first-page-v1'
 ELSE json_extract(c.input_json,'$.lookup_algorithm')=f.representative_algorithm AND json_extract(c.input_json,'$.lookup_sha1') IS f.representative_sha1 END)) THEN 'stale'
 WHEN m.fetch_error!='' THEN CASE WHEN m.fetch_error IN('no_match','no_sha1') THEN 'no_match' WHEN m.fetch_error='multiple_matches' THEN 'multiple_matches' ELSE 'fetch_error' END
 WHEN c.edition_id IS NOT NULL THEN 'ready'
 ELSE 'no_match' END AS status
 FROM import_members m JOIN editions e ON e.id=m.edition_id JOIN files f ON f.edition_id=e.id JOIN libraries l ON l.id=e.library_id
 LEFT JOIN import_candidates c ON c.batch_id=m.batch_id AND c.edition_id=m.edition_id
 LEFT JOIN edition_metadata_state ms ON ms.edition_id=e.id WHERE m.batch_id=?
) `

func importReviewCounts(ctx context.Context, q queryer, batch string) (map[string]int, error) {
	rows, e := scopeRows(ctx, q, importReviewSource+"SELECT status,count(*) FROM review GROUP BY status", batch)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if e = rows.Scan(&k, &n); e != nil {
			return nil, e
		}
		counts[k] = n
	}
	return counts, rows.Err()
}
func (s *Store) ImportSelections(ctx context.Context, batch, filter string, page, size int) (model.ImportPage[model.ImportSelection], error) {
	out := model.ImportPage[model.ImportSelection]{Items: []model.ImportSelection{}, Page: page, PageSize: size}
	if page < 1 || page > 1000000 || size < 1 || size > 1000 {
		return out, model.ErrInput
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var stage string
	if e = scopeRow(ctx, tx, "SELECT stage FROM import_batches WHERE id=?", batch).Scan(&stage); e != nil {
		return out, importNotFound(e)
	}
	if stage != "metadata" {
		return out, model.ErrConflict
	}
	where := ""
	switch filter {
	case "", "all":
	case "ready":
		where = " WHERE status='ready'"
	case "pending":
		where = " WHERE decision=''"
	default:
		return out, model.ErrInput
	}
	if e = scopeRow(ctx, tx, importReviewSource+"SELECT count(*) FROM review"+where, batch).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, importReviewSource+"SELECT id,revision,candidate_revision,status,decision,edition_revision,has_metadata FROM review"+where+" ORDER BY id LIMIT ? OFFSET ?", batch, size, (page-1)*size)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v model.ImportSelection
		if e = rows.Scan(&v.ID, &v.Revision, &v.CandidateRevision, &v.Status, &v.Decision, &v.EditionRevision, &v.HasMetadata); e != nil {
			rows.Close()
			return out, e
		}
		v.Acceptable = v.Status == "ready"
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

// Translate the whole visible set in one query. Pending candidates need not have
// been materialized as business tags, and never cause dictionary writes or IO.
func importDisplayTags(ctx context.Context, q queryer, input []model.TagName) ([]model.Tag, error) {
	var maskErr error
	q, maskErr = maskedDictionary(ctx, q)
	if maskErr != nil {
		return nil, maskErr
	}
	if len(input) == 0 {
		return []model.Tag{}, nil
	}
	for i := range input {
		input[i] = model.CanonicalTag(input[i])
	}
	raw, _ := json.Marshal(input)
	generation, state, e := activeDictionary(ctx, q)
	if e != nil {
		return nil, e
	}
	rows, e := scopeRows(ctx, q, `SELECT json_extract(j.value,'$.namespace'),json_extract(j.value,'$.name'),COALESCE(t.id,''),
 COALESCE(m.text,r.text,d.translation,json_extract(j.value,'$.name')),
 CASE WHEN m.text IS NOT NULL THEN 'manual' WHEN r.text IS NOT NULL OR d.translation IS NOT NULL THEN 'remote' ELSE 'original' END,
 COALESCE(m.revision,0),d.name IS NOT NULL FROM json_each(?) j
 LEFT JOIN tags t ON t.namespace=json_extract(j.value,'$.namespace') AND t.name=json_extract(j.value,'$.name')
 LEFT JOIN tag_translations m ON m.tag_id=t.id AND m.locale='zh-CN' AND m.origin='manual'
 LEFT JOIN translation_state v ON v.singleton=1
 LEFT JOIN tag_translations r ON r.tag_id=t.id AND r.locale='zh-CN' AND r.origin='remote' AND r.dataset=v.active_version
 LEFT JOIN dictionary_entries d ON d.generation_id=? AND d.namespace=json_extract(j.value,'$.namespace') AND d.name=json_extract(j.value,'$.name')`, string(raw), generation)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.Tag{}
	for rows.Next() {
		var t model.Tag
		var match bool
		if e = rows.Scan(&t.Namespace, &t.Name, &t.ID, &t.Translation, &t.Origin, &t.Revision, &match); e != nil {
			return nil, e
		}
		if state == "disabled" {
			t.DictionaryState = "disabled"
		}
		if state == "ready" {
			t.DictionaryMatch = &match
		}
		if t.ID == "" {
			t.ID = fmt.Sprintf("%x", sha256.Sum256([]byte(t.Namespace+"\x00"+t.Name)))
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ImportEditor(ctx context.Context, batch, id string) (model.ImportEditor, error) {
	out := model.ImportEditor{}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	var stage, decision string
	e = scopeRow(ctx, tx, `SELECT b.stage,m.revision,m.metadata_decision FROM import_members m JOIN import_batches b ON b.id=m.batch_id WHERE m.batch_id=? AND m.edition_id=?`, batch, id).Scan(&stage, &out.MemberRevision, &decision)
	if e != nil {
		return out, importNotFound(e)
	}
	if stage != "metadata" {
		return out, model.ErrConflict
	}
	edition, e := scanEdition(scopeRow(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id=?", id))
	if e != nil {
		return out, e
	}
	out.Edition = *edition
	details, e := metadataDetails(ctx, tx, id)
	if e != nil {
		return out, e
	}
	candidate, e := importCandidate(ctx, tx, batch, id)
	if e != nil && !errors.Is(e, model.ErrNotFound) {
		return out, e
	}
	if candidate != nil && decision == "" {
		out.CandidateRevision = candidate.Revision
		fields, err := model.ProjectMetadata(candidate.Fields, details.Overrides)
		if err != nil {
			return out, err
		}
		details.Fields = fields
		props := model.InferProperties(fields, candidate.Fields.Language, details.Overrides)
		details.Properties = &props
		details.Source = &model.Source{ID: "candidate", Provider: "ehentai", ExternalID: candidate.Result.ExternalID, URL: candidate.URL, Method: candidate.Method, ContentRevision: candidate.ContentRevision, Fields: candidate.Fields}
		details.Tags, err = importDisplayTags(ctx, tx, fields.Tags)
		if err != nil {
			return out, err
		}
	}
	out.Metadata = details
	return out, tx.Commit()
}
func (s *Store) ReviewImportManual(ctx context.Context, batch, id string, in model.ImportManualReview) error {
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var stage, decision string
	var member, edition int64
	e = scopeRow(ctx, tx, `SELECT b.stage,m.revision,m.metadata_decision,e.revision FROM import_members m JOIN import_batches b ON b.id=m.batch_id JOIN editions e ON e.id=m.edition_id WHERE m.batch_id=? AND m.edition_id=?`, batch, id).Scan(&stage, &member, &decision, &edition)
	if e != nil {
		return importNotFound(e)
	}
	if stage != "metadata" || decision != "" || member != in.MemberRevision || edition != in.ExpectedRevision {
		return model.ErrConflict
	}
	candidate, e := importCandidate(ctx, tx, batch, id)
	if e != nil && !errors.Is(e, model.ErrNotFound) {
		return e
	}
	if (candidate == nil && in.CandidateRevision != 0) || (candidate != nil && candidate.Revision != in.CandidateRevision) {
		return model.ErrConflict
	}
	if e = managementGuard(ctx, tx, id); e != nil {
		return e
	}
	if len(in.Fields) > 0 {
		if e = applyOverridesTx(ctx, tx, id, in.ExpectedRevision, in.Fields); e != nil {
			return e
		}
	}
	kind := "manual"
	if candidate != nil {
		kind = "accept"
	}
	if e = reviewImportTx(ctx, tx, batch, []model.ImportReview{{ID: id, ExpectedRevision: in.MemberRevision, CandidateRevision: in.CandidateRevision, Decision: kind}}); e != nil {
		return e
	}
	return tx.Commit()
}

func enrichImportReview(ctx context.Context, tx *sql.Tx, batch string, items []model.ImportMember) error {
	if len(items) == 0 {
		return nil
	}
	ids := []string{}
	byID := map[string]int{}
	for i, v := range items {
		ids = append(ids, v.ID)
		byID[v.ID] = i
	}
	args := []any{batch}
	args = append(args, stringArgs(ids)...)
	rows, e := scopeRows(ctx, tx, importReviewSource+`SELECT r.id,r.status,c.revision,c.request_generation,c.content_revision,c.selection_revision,c.input_json,
 json_extract(c.result_json,'$.Fields'),json_extract(c.result_json,'$.URL'),json_extract(c.result_json,'$.Method'),
 (SELECT count(*) FROM edition_sources es JOIN external_metadata x ON x.id=es.metadata_id WHERE x.provider='ehentai' AND x.external_id=json_extract(c.result_json,'$.ExternalID') AND es.edition_id!=r.id)
 FROM review r LEFT JOIN import_candidates c ON c.batch_id=? AND c.edition_id=r.id WHERE r.id IN(`+placeholders(len(ids))+`)`, append([]any{batch}, args...)...)
	if e != nil {
		return e
	}
	tags := map[model.TagName]bool{}
	for rows.Next() {
		var id, status string
		var revision, generation, content, selection sql.NullInt64
		var input, fields, url, method sql.NullString
		var shared int
		if e = rows.Scan(&id, &status, &revision, &generation, &content, &selection, &input, &fields, &url, &method, &shared); e != nil {
			rows.Close()
			return e
		}
		i := byID[id]
		items[i].Status = status
		items[i].Acceptable = status == "ready"
		if revision.Valid {
			c := &model.ImportCandidate{Revision: revision.Int64, Generation: generation.Int64, ContentRevision: content.Int64, SelectionRevision: selection.Int64, URL: url.String, Method: method.String, SharedEditions: shared}
			if e = json.Unmarshal([]byte(input.String), &c.Input); e != nil {
				rows.Close()
				return e
			}
			if e = json.Unmarshal([]byte(fields.String), &c.Fields); e != nil {
				rows.Close()
				return e
			}
			for _, t := range c.Fields.Tags {
				tags[model.CanonicalTag(t)] = true
			}
			c.Result.Fields = c.Fields
			items[i].Candidate = c
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// One batch query, never one request or SQL round-trip per card.
	var available int
	if e = scopeRow(ctx, tx, "SELECT count(*) FROM sqlite_master WHERE name='source_searches'").Scan(&available); e != nil {
		return e
	}
	if available > 0 {
		rows, e = scopeRows(ctx, tx, `SELECT m.edition_id,COALESCE((SELECT count(*) FROM source_search_results r WHERE r.search_id=(SELECT id FROM source_searches s WHERE s.batch_id=m.batch_id AND s.edition_id=m.edition_id ORDER BY s.rowid DESC LIMIT 1) HAVING count(*)>0),(SELECT json_array_length(i.candidates_json) FROM job_items i WHERE i.job_id=m.active_job_id AND i.edition_id=m.edition_id),0),COALESCE((SELECT id FROM source_searches s WHERE s.batch_id=m.batch_id AND s.edition_id=m.edition_id ORDER BY s.rowid DESC LIMIT 1),''),COALESCE((SELECT continuation='end' FROM source_searches s WHERE s.batch_id=m.batch_id AND s.edition_id=m.edition_id ORDER BY s.rowid DESC LIMIT 1),0) FROM import_members m WHERE m.batch_id=? AND m.edition_id IN(`+placeholders(len(ids))+`)`, append([]any{batch}, stringArgs(ids)...)...)
		if e != nil {
			return e
		}
		for rows.Next() {
			var id, search string
			var count int
			var complete bool
			if e = rows.Scan(&id, &count, &search, &complete); e != nil {
				rows.Close()
				return e
			}
			i := byID[id]
			items[i].MatchCount = count
			items[i].MatchListComplete = complete
			items[i].SourceSearchID = search
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	names := []model.TagName{}
	for t := range tags {
		names = append(names, t)
	}
	translated, e := importDisplayTags(ctx, tx, names)
	if e != nil {
		return e
	}
	known := map[model.TagName]model.Tag{}
	for _, t := range translated {
		known[model.TagName{Namespace: t.Namespace, Name: t.Name}] = t
	}
	for i := range items {
		if c := items[i].Candidate; c != nil {
			c.Tags = []model.Tag{}
			for _, t := range c.Fields.Tags {
				c.Tags = append(c.Tags, known[model.CanonicalTag(t)])
			}
		}
	}
	return nil
}

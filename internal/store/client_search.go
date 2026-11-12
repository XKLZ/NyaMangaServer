package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"

	"nyamangaserver.local/server/internal/model"
)

func clientSearchSnapshot(ctx context.Context, tx *sql.Tx, account string, v model.ClientSearchInput) (string, error) {
	revision, e := catalogRevision(ctx, tx)
	if e != nil {
		return "", e
	}
	enhancement, e := enrichmentRevision(ctx, tx)
	if e != nil {
		return "", e
	}
	identity, e := clientIdentity(ctx, tx)
	if e != nil {
		return "", e
	}
	v.Page, v.Snapshot = 0, ""
	digest := sha256.New()
	encoder := json.NewEncoder(digest)
	if e = encoder.Encode([]any{revision, enhancement, identity, v}); e != nil {
		return "", e
	}
	rows, e := scopeRows(ctx, tx, `SELECT e.id,COALESCE(u.revision,0),COALESCE(r.revision,0)
		FROM editions e LEFT JOIN edition_user_states u ON u.edition_id=e.id AND u.account_id=?
		LEFT JOIN reading_states r ON r.edition_id=e.id AND r.account_id=?
		WHERE e.library_id=? AND e.trashed_at IS NULL AND (u.revision IS NOT NULL OR r.revision IS NOT NULL) ORDER BY e.id`,
		account, account, v.LibraryID)
	if e != nil {
		return "", e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var user, reading int64
		if e = rows.Scan(&id, &user, &reading); e != nil {
			return "", e
		}
		if e = encoder.Encode([]any{id, user, reading}); e != nil {
			return "", e
		}
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), rows.Err()
}

func clientWorkSearchCTE(account string, v model.ClientSearchInput, node *model.SearchNode) (string, []any) {
	hidden := v.Scope == "hidden"
	args := []any{account, account, v.LibraryID, hidden}
	where := "1"
	if v.Scope == "favorites" {
		where += " AND b.favorite=1"
	}
	if v.FileState != "" {
		where += " AND b.file_state=?"
		args = append(args, v.FileState)
	}
	properties, values := propertyPredicate(v.Properties)
	args = append(args, values...)
	expression, values := searchExpression(node)
	args = append(args, values...)
	workFilter := ""
	if v.Scope == "continue" {
		workFilter = " WHERE m.resume_edition_id IS NOT NULL"
	}
	return `WITH visible(edition_id,display_title,cover_revision,content_revision,parsing_revision,page_count,file_state,hidden,favorite,state_revision,
		work_id,preferred_id,indexed_at,last_read_at,completed) AS MATERIALIZED (
		SELECT ` + clientEditionColumns + `,ed.work_id,w.preferred_edition_id,f.indexed_at,r.last_read_at,COALESCE(r.completed,0)
		FROM editions ed ` + clientEditionJoins + ` LEFT JOIN reading_states r ON r.edition_id=ed.id AND r.account_id=?
		WHERE ed.library_id=? AND ed.trashed_at IS NULL AND COALESCE(u.hidden,0)=?
	), eligible AS (
		SELECT b.*,d.title,d.original_title,COALESCE(nya_normalize_v1(p.display_title),'') work_title
		FROM (SELECT v.*,v.edition_id id FROM visible v) b JOIN search_documents d ON d.edition_id=b.edition_id
		LEFT JOIN visible p ON p.edition_id=b.preferred_id AND p.work_id=b.work_id
		WHERE ` + where + ` AND (` + properties + `)
	), matched AS MATERIALIZED (
		SELECT e.*,row_number() OVER(PARTITION BY e.work_id ORDER BY (e.edition_id=e.preferred_id) DESC,e.edition_id) entry_rank,
			max(e.indexed_at) OVER(PARTITION BY e.work_id) latest_index
		FROM eligible e WHERE ` + expression + `
	), members AS (
		SELECT work_id,max(CASE WHEN edition_id=preferred_id THEN edition_id END) preferred_edition_id,
			max(last_read_at) latest_read
		FROM visible GROUP BY work_id
	), resume AS (
		SELECT work_id,edition_id,row_number() OVER(PARTITION BY work_id ORDER BY last_read_at DESC,edition_id) resume_rank
		FROM visible WHERE hidden=0 AND completed=0 AND last_read_at IS NOT NULL
	), work_states AS (
		SELECT a.*,r.edition_id resume_edition_id FROM members a LEFT JOIN resume r ON r.work_id=a.work_id AND r.resume_rank=1
	), selected AS (
		SELECT e.*,m.preferred_edition_id,m.resume_edition_id,m.latest_read FROM matched e JOIN work_states m ON m.work_id=e.work_id
		` + workFilter + `
	) `, args
}

func (s *Store) ClientWorkSearch(ctx context.Context, v model.ClientSearchInput, node *model.SearchNode) (model.ClientSearchResult, error) {
	out := model.ClientSearchResult{Items: []model.ClientWorkHit{}, Page: v.Page, PageSize: v.PageSize}
	account, e := readingAccount(ctx)
	if e != nil {
		return out, e
	}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot, e = clientSearchSnapshot(ctx, tx, account, v)
	if e != nil {
		return out, e
	}
	if v.Snapshot != "" && v.Snapshot != out.Snapshot {
		return out, model.ErrSnapshot
	}
	l, e := scanLibrary(scopeRow(ctx, tx, "SELECT "+libraryColumns+" FROM libraries WHERE id=? AND trashed_at IS NULL", v.LibraryID))
	if e != nil {
		return out, e
	}
	out.SourceLibrary = *l
	cte, args := clientWorkSearchCTE(account, v, node)
	if e = scopeRow(ctx, tx, cte+"SELECT count(*) FROM selected WHERE entry_rank=1", args...).Scan(&out.Total); e != nil {
		return out, e
	}
	order := "x.latest_index"
	switch v.Sort {
	case "title":
		order = "nya_natural_v1(x.display_title)"
	case "last_read_at":
		order = "x.latest_read"
	}
	direction := " DESC"
	if v.Order == "asc" {
		direction = " ASC"
	}
	rows, e := scopeRows(ctx, tx, cte+`SELECT x.edition_id,x.display_title,x.cover_revision,x.content_revision,x.parsing_revision,x.page_count,x.file_state,x.hidden,x.favorite,x.state_revision,
		x.work_id,x.preferred_edition_id,x.resume_edition_id,COALESCE(se.id,''),COALESCE(se.name,''),COALESCE(sm.position,0),
		rs.page_index,CASE WHEN rs.revision IS NULL THEN x.page_count ELSE rs.recorded_page_count END,COALESCE(rs.content_revision,x.content_revision),
		COALESCE(rs.parsing_revision,x.parsing_revision),COALESCE(rs.completed,0),rs.completed_at,rs.last_read_at,COALESCE(rs.read_count,0),COALESCE(rs.revision,0)
		FROM selected x LEFT JOIN series_members sm ON sm.work_id=x.work_id LEFT JOIN series se ON se.id=sm.series_id
		LEFT JOIN reading_states rs ON rs.edition_id=x.edition_id AND rs.account_id=?
		WHERE x.entry_rank=1 ORDER BY (`+order+` IS NULL),`+order+direction+`,x.work_id LIMIT ? OFFSET ?`,
		append(args, account, v.PageSize, (v.Page-1)*v.PageSize)...)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		hit := model.ClientWorkHit{LibraryID: v.LibraryID}
		var series model.ClientSeries
		r := &hit.Reading
		e = rows.Scan(append(clientEditionTargets(&hit.Edition), &hit.WorkID, &hit.PreferredEditionID, &hit.ResumeEditionID,
			&series.ID, &series.Name, &series.Position, &r.PageIndex, &r.RecordedPageCount, &r.ContentRevision,
			&r.ParsingRevision, &r.Completed, &r.CompletedAt, &r.LastReadAt, &r.ReadCount, &r.Revision)...)
		if e != nil {
			return out, e
		}
		if series.ID != "" {
			hit.Series = &series
		}
		hit.EntryEditionID, hit.Title = hit.Edition.ID, hit.Edition.Title
		r.EditionID, r.CurrentContentRevision, r.CurrentParsingRevision, r.CurrentPageCount = hit.Edition.ID, hit.Edition.ContentRevision, hit.Edition.ParsingRevision, hit.Edition.PageCount
		r.ParsingStale = r.ParsingRevision != r.CurrentParsingRevision
		r.Stale = r.ContentRevision != r.CurrentContentRevision || r.ParsingStale
		out.Items = append(out.Items, hit)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

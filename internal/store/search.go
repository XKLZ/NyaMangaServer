package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

const searchDocumentSource = `(SELECT d.*,nya_normalize_v1(COALESCE(w.name_override,eb.title,f.filename_title)) work_title FROM search_documents d JOIN editions ed ON ed.id=d.edition_id JOIN works w ON w.id=ed.work_id JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id)`

func searchExpression(n *model.SearchNode) (string, []any) {
	if n == nil {
		return "1", nil
	}
	if n.Op == "not" {
		s, a := searchExpression(n.Left)
		return "NOT (" + s + ")", a
	}
	if n.Op == "and" || n.Op == "or" {
		l, a := searchExpression(n.Left)
		r, b := searchExpression(n.Right)
		return "(" + l + " " + strings.ToUpper(n.Op) + " " + r + ")", append(a, b...)
	}
	if n.Field == "" {
		return "(instr(e.title,?)>0 OR instr(e.original_title,?)>0 OR instr(e.work_title,?)>0)", []any{n.Value, n.Value, n.Value}
	}
	if n.Field == "title" {
		return "instr(e.title,?)>0", []any{n.Value}
	}
	if n.Field == "title_original" {
		return "instr(e.original_title,?)>0", []any{n.Value}
	}
	predicate := "st.name=?"
	value := n.Value
	if n.Prefix {
		predicate = "substr(st.name,1,length(?))=?"
		return "EXISTS(SELECT 1 FROM effective_edition_tags bt JOIN search_tags st ON st.tag_id=bt.tag_id WHERE bt.edition_id=e.edition_id AND st.namespace=? AND " + predicate + ")", []any{n.Field, value, value}
	}
	return "EXISTS(SELECT 1 FROM effective_edition_tags bt JOIN search_tags st ON st.tag_id=bt.tag_id WHERE bt.edition_id=e.edition_id AND st.namespace=? AND " + predicate + ")", []any{n.Field, value}
}
func searchCTE(a string, v model.SearchInput, n *model.SearchNode) (string, []any) {
	where := "b.trashed_at IS NULL AND EXISTS(SELECT 1 FROM libraries l WHERE l.id=b.library_id AND l.disabled=0 AND l.trashed_at IS NULL)"
	if v.Admin {
		where = "1"
		switch v.Visibility {
		case "active":
			where += " AND b.trashed_at IS NULL AND EXISTS(SELECT 1 FROM libraries l WHERE l.id=b.library_id AND l.trashed_at IS NULL)"
		case "trash":
			where += " AND (b.trashed_at IS NOT NULL OR EXISTS(SELECT 1 FROM libraries l WHERE l.id=b.library_id AND l.trashed_at IS NOT NULL))"
		}
		if !v.IncludeDisabled {
			where += " AND EXISTS(SELECT 1 FROM libraries l WHERE l.id=b.library_id AND l.disabled=0)"
		}
	}
	args := []any{a, a}
	if v.LibraryID != "" {
		where += " AND b.library_id=?"
		args = append(args, v.LibraryID)
	}
	if v.FileState != "" {
		where += " AND b.file_state=?"
		args = append(args, v.FileState)
	}
	if v.Scope == "hidden" {
		where += " AND COALESCE(u.hidden,0)=1"
	} else if !v.Admin {
		where += " AND COALESCE(u.hidden,0)=0"
	}
	switch v.Scope {
	case "favorites":
		where += " AND COALESCE(u.favorite,0)=1"
	case "missing_metadata":
		where += " AND NOT EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id)"
	case "missing", "corrupt":
		where += " AND b.file_state=?"
		args = append(args, v.Scope)
	}
	prop, values := propertyPredicate(v.Properties)
	where += " AND (" + prop + ")"
	args = append(args, values...)
	kind, id := "'edition'", "b.id"
	if v.Collapse != "none" {
		kind = "'work'"
		id = "b.work_id"
	}
	if v.Collapse == "series" {
		kind = "CASE WHEN sm.series_id IS NOT NULL THEN 'series' ELSE 'work' END"
		id = "COALESCE(sm.series_id,b.work_id)"
	}
	expr, more := searchExpression(n)
	args = append(args, more...)
	return `WITH all_members AS (SELECT ` + kind + ` kind,` + id + ` result_id,COALESCE(u.favorite,0) favorite FROM ` + editionRecordSQL + ` b JOIN works w ON w.id=b.work_id LEFT JOIN series_members sm ON sm.work_id=w.id LEFT JOIN edition_user_states u ON u.edition_id=b.id AND u.account_id=?),
 eligible AS MATERIALIZED (
 SELECT b.id edition_id,b.library_id,b.work_id item_id,d.title,d.original_title,d.work_title,d.sort_key,b.indexed_at,` + kind + ` kind,` + id + ` result_id,
 (w.preferred_edition_id=b.id) is_primary,COALESCE(sm.position,0) position,COALESCE(u.favorite,0) favorite,COALESCE(u.hidden,0) hidden,COALESCE(u.revision,0) state_revision
 FROM ` + editionRecordSQL + ` b JOIN ` + searchDocumentSource + ` d ON d.edition_id=b.id JOIN works w ON w.id=b.work_id

 LEFT JOIN series_members sm ON sm.work_id=b.work_id LEFT JOIN edition_user_states u ON u.edition_id=b.id AND u.account_id=? WHERE ` + where + `
 ), matched AS MATERIALIZED (SELECT e.* FROM eligible e WHERE ` + expr + `),
 keys AS (SELECT kind,result_id,count(*) match_count FROM matched GROUP BY kind,result_id),
 favorites AS (SELECT am.kind,am.result_id,sum(am.favorite) favorites,count(*) member_count FROM all_members am JOIN keys k ON k.kind=am.kind AND k.result_id=am.result_id GROUP BY am.kind,am.result_id),
 ranked AS (SELECT e.*,row_number() OVER(PARTITION BY e.kind,e.result_id ORDER BY e.position,e.is_primary DESC,e.edition_id) rn FROM eligible e JOIN keys k ON k.kind=e.kind AND k.result_id=e.result_id),
 summary AS (SELECT e.kind,e.result_id,min(CASE WHEN e.kind='work' THEN nya_natural_v1(e.work_title) ELSE e.sort_key END) sort_key,max(e.indexed_at) indexed_at,sum(e.favorite) favorites,count(*) member_count FROM eligible e JOIN keys k ON k.kind=e.kind AND k.result_id=e.result_id GROUP BY e.kind,e.result_id)
 `, args
}
func validateSearchLibrary(ctx context.Context, q queryer, id string) error {
	if id == "" {
		return nil
	}
	var n int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM libraries WHERE id=?", id).Scan(&n); e != nil {
		return e
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}
func checkSearchSnapshot(ctx context.Context, q queryer, v model.SearchInput) (string, error) {
	if e := validateSearchLibrary(ctx, q, v.LibraryID); e != nil {
		return "", e
	}
	rev, e := catalogRevision(ctx, q)
	if e == nil {
		epoch, err := enrichmentRevision(ctx, q)
		if err != nil {
			return "", err
		}
		rev = fmt.Sprintf("%s:enhancements:%d", rev, epoch)
	}
	if e == nil && v.Snapshot != "" && rev != v.Snapshot {
		return rev, model.ErrSnapshot
	}
	return rev, e
}
func (s *Store) Search(ctx context.Context, a string, v model.SearchInput, n *model.SearchNode) (model.SearchResult, error) {
	if v.Admin {
		return s.adminSearch(ctx, a, v, n)
	}
	out := model.SearchResult{Items: []model.SearchHit{}, Page: v.Page, PageSize: v.PageSize}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot, e = checkSearchSnapshot(ctx, tx, v)
	if e != nil {
		return out, e
	}
	cte, args := searchCTE(a, v, n)
	if e = scopeRow(ctx, tx, cte+"SELECT count(*) FROM keys", args...).Scan(&out.Total); e != nil {
		return out, e
	}
	// SQLite performs filtering, folding, stable sorting and LIMIT before Go reads rows.
	order := "s.indexed_at"
	if v.Sort == "title" {
		order = "s.sort_key"
	}
	direction := " DESC"
	if v.Order == "asc" {
		direction = " ASC"
	}
	query := cte + `SELECT r.kind,r.result_id,COALESCE((SELECT name FROM series WHERE id=r.result_id AND r.kind='series'),(SELECT COALESCE(w.name_override,eb.title,f.filename_title) FROM works w JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id WHERE w.id=r.result_id AND r.kind='work'),''),r.edition_id,k.match_count,
 (SELECT json_group_array(edition_id) FROM (SELECT edition_id FROM matched m WHERE m.kind=r.kind AND m.result_id=r.result_id ORDER BY edition_id LIMIT 100)),
 NOT EXISTS(SELECT 1 FROM matched m WHERE m.edition_id=r.edition_id),f.favorites,f.member_count,r.favorite,r.hidden,r.state_revision
 FROM ranked r JOIN keys k ON k.kind=r.kind AND k.result_id=r.result_id JOIN summary s ON s.kind=r.kind AND s.result_id=r.result_id
 JOIN favorites f ON f.kind=r.kind AND f.result_id=r.result_id
 WHERE r.rn=1 ORDER BY ` + order + direction + `,r.kind,r.result_id LIMIT ? OFFSET ?`
	rows, e := scopeRows(ctx, tx, query, append(args, v.PageSize, (v.Page-1)*v.PageSize)...)
	if e != nil {
		return out, e
	}
	type entry struct {
		hit    model.SearchHit
		bookID string
	}
	entries := []entry{}
	for rows.Next() {
		var x entry
		var ids string
		var fav, total int
		if e = rows.Scan(&x.hit.Kind, &x.hit.ID, &x.hit.Name, &x.bookID, &x.hit.MatchCount, &ids, &x.hit.OtherVersion, &fav, &total, &x.hit.State.Favorite, &x.hit.State.Hidden, &x.hit.State.Revision); e != nil {
			rows.Close()
			return out, e
		}
		if e = json.Unmarshal([]byte(ids), &x.hit.MatchIDs); e != nil {
			rows.Close()
			return out, e
		}
		x.hit.Favorite = "none"
		if fav == total {
			x.hit.Favorite = "all"
		} else if fav > 0 {
			x.hit.Favorite = "partial"
		}
		entries = append(entries, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	// Hydrate only this page in one query, avoiding a query per result.
	ids := make([]string, 0, len(entries))
	for _, x := range entries {
		ids = append(ids, x.bookID)
	}
	books := map[string]model.Edition{}
	if len(ids) > 0 {
		rows, e = scopeRows(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id IN ("+placeholders(len(ids))+")", stringArgs(ids)...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			b, e := scanEdition(rows)
			if e != nil {
				rows.Close()
				return out, e
			}
			books[b.ID] = *b
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	meta := map[string]model.MetadataDetails{}
	if v.Admin && len(ids) > 0 {
		rows, e = scopeRows(ctx, tx, `SELECT b.id,CASE WHEN bs.edition_id IS NULL THEN 'none' WHEN bs.matched_content_revision!=b.content_revision THEN 'needs_review' ELSE 'matched' END,COALESCE(json_extract(eb.fields_json,'$.title_original'),'') FROM `+editionRecordSQL+` b LEFT JOIN edition_sources bs ON bs.edition_id=b.id LEFT JOIN effective_editions eb ON eb.edition_id=b.id WHERE b.id IN (`+placeholders(len(ids))+`)`, stringArgs(ids)...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var id, state, original string
			if e = rows.Scan(&id, &state, &original); e != nil {
				rows.Close()
				return out, e
			}
			d := model.MetadataDetails{State: state, Fields: model.Metadata{Tags: []model.TagName{}}, Tags: []model.Tag{}, Overrides: map[string]model.Override{}, SelectionRevision: 1}
			if original != "" {
				d.Fields.TitleOriginal = &original
			}
			meta[id] = d
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	if s.Version >= 26 {
		if e = readingForMap(ctx, tx, books); e != nil {
			return out, e
		}
	}
	for _, x := range entries {
		x.hit.Metadata = meta[x.bookID]
		x.hit.Representative = books[x.bookID]
		out.Items = append(out.Items, x.hit)
	}
	return out, tx.Commit()
}
func (s *Store) SearchMatches(ctx context.Context, a string, v model.SearchInput, n *model.SearchNode, kind, id string) (model.MatchResult, error) {
	out := model.MatchResult{Items: []model.EditionMatch{}, Page: v.Page, PageSize: v.PageSize}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot, e = checkSearchSnapshot(ctx, tx, v)
	if e != nil {
		return out, e
	}
	cte, args := searchCTE(a, v, n)
	args = append(args, kind, id)
	if e = scopeRow(ctx, tx, cte+"SELECT count(*) FROM matched WHERE kind=? AND result_id=?", args...).Scan(&out.Total); e != nil {
		return out, e
	}
	if out.Total == 0 {
		return out, model.ErrNotFound
	}
	rows, e := scopeRows(ctx, tx, cte+`SELECT m.edition_id,COALESCE(eb.title,b.filename_title),COALESCE(json_extract(eb.fields_json,'$.title_original'),''),m.library_id,m.item_id,b.content_revision,m.favorite,m.hidden,m.state_revision FROM matched m JOIN `+editionRecordSQL+` b ON b.id=m.edition_id LEFT JOIN effective_editions eb ON eb.edition_id=b.id WHERE m.kind=? AND m.result_id=? ORDER BY m.edition_id LIMIT ? OFFSET ?`, append(args, v.PageSize, (v.Page-1)*v.PageSize)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var b model.EditionMatch
		if e = rows.Scan(&b.ID, &b.Title, &b.TitleOriginal, &b.LibraryID, &b.WorkID, &b.ContentRevision, &b.State.Favorite, &b.State.Hidden, &b.State.Revision); e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, b)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) SavedSearches(ctx context.Context, a string) ([]model.SavedSearch, error) {
	rows, e := scopeRows(ctx, s.Read, "SELECT id,name,query_version,conditions_json,revision FROM saved_searches WHERE account_id=? ORDER BY name,id", a)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []model.SavedSearch{}
	for rows.Next() {
		var v model.SavedSearch
		var raw string
		if e = rows.Scan(&v.ID, &v.Name, &v.QueryVersion, &raw, &v.Revision); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(raw), &v.Conditions); e != nil {
			return nil, e
		}
		if v.Conditions.Collapse == "versions" {
			v.Conditions.Collapse = "works"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SaveSearch(ctx context.Context, a, id, name string, v model.SearchInput, rev int64) (model.SavedSearch, error) {
	if v.Collapse == "versions" {
		v.Collapse = "works"
	}
	out := model.SavedSearch{ID: id, Name: name, QueryVersion: 1, Conditions: v, Revision: rev + 1}
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = validateSearchLibrary(ctx, tx, v.LibraryID); e != nil {
		return out, e
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return out, e
	}
	if id == "" {
		if rev != 0 {
			return out, model.ErrConflict
		}
		out.ID, e = model.NewID()
		if e != nil {
			return out, e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO saved_searches VALUES(?,?,?,?,?,?)", out.ID, a, name, 1, string(raw), out.Revision)
	} else {
		var result sql.Result
		result, e = tx.ExecContext(ctx, "UPDATE saved_searches SET name=?,query_version=1,conditions_json=?,revision=revision+1 WHERE id=? AND account_id=? AND revision=?", name, string(raw), id, a, rev)
		if e == nil {
			n, _ := result.RowsAffected()
			if n == 0 {
				var existing int
				e = scopeRow(ctx, tx, "SELECT count(*) FROM saved_searches WHERE id=? AND account_id=?", id, a).Scan(&existing)
				if e == nil {
					if existing == 0 {
						e = model.ErrNotFound
					} else {
						e = model.ErrConflict
					}
				}
			}
		}
	}
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (s *Store) DeleteSearch(ctx context.Context, a, id string, rev int64) error {
	result, e := s.Write.ExecContext(ctx, "DELETE FROM saved_searches WHERE id=? AND account_id=? AND revision=?", id, a, rev)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		var existing int
		if e = scopeRow(ctx, s.Read, "SELECT count(*) FROM saved_searches WHERE id=? AND account_id=?", id, a).Scan(&existing); e != nil {
			return e
		}
		if existing == 0 {
			return model.ErrNotFound
		}
		return model.ErrConflict
	}
	return nil
}

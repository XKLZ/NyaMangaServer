package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
)

// adminSearch filters/counts physical books directly, without the folding CTEs.
func (s *Store) adminSearch(ctx context.Context, account string, v model.SearchInput, node *model.SearchNode) (model.SearchResult, error) {
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
	where := "1"
	args := []any{account}
	switch v.Visibility {
	case "active":
		where += " AND b.trashed_at IS NULL AND l.trashed_at IS NULL"
	case "trash":
		where += " AND (b.trashed_at IS NOT NULL OR l.trashed_at IS NOT NULL)"
	}
	if !v.IncludeDisabled {
		where += " AND l.disabled=0"
	}
	if v.LibraryID != "" {
		where += " AND b.library_id=?"
		args = append(args, v.LibraryID)
	}
	if v.FileState != "" {
		where += " AND b.file_state=?"
		args = append(args, v.FileState)
	}
	switch v.Scope {
	case "favorites":
		where += " AND COALESCE(u.favorite,0)=1"
	case "hidden":
		where += " AND COALESCE(u.hidden,0)=1"
	case "missing_metadata":
		where += " AND NOT EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id)"
	case "missing", "corrupt":
		where += " AND b.file_state=?"
		args = append(args, v.Scope)
	}
	switch v.MetadataState {
	case "none":
		where += " AND NOT EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id)"
	case "matched":
		where += " AND EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id AND bs.matched_content_revision=b.content_revision)"
	case "needs_review":
		where += " AND EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id AND bs.matched_content_revision!=b.content_revision)"
	}
	prop, values := propertyPredicate(v.Properties)
	where += " AND (" + prop + ")"
	args = append(args, values...)
	expr, params := searchExpression(node)
	where += " AND (" + expr + ")"
	args = append(args, params...)
	from := ` FROM ` + editionRecordSQL + ` b JOIN libraries l ON l.id=b.library_id JOIN ` + searchDocumentSource + ` e ON e.edition_id=b.id LEFT JOIN edition_user_states u ON u.edition_id=b.id AND u.account_id=? WHERE ` + where
	if e = scopeRow(ctx, tx, "SELECT count(*)"+from, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	order := "b.indexed_at"
	if v.Sort == "title" {
		order = "e.sort_key"
	}
	direction := " DESC"
	if v.Order == "asc" {
		direction = " ASC"
	}
	rows, e := scopeRows(ctx, tx, `SELECT b.id,COALESCE(u.favorite,0),COALESCE(u.hidden,0),COALESCE(u.revision,0),CASE WHEN NOT EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id) THEN 'none' WHEN EXISTS(SELECT 1 FROM edition_sources bs WHERE bs.edition_id=b.id AND bs.matched_content_revision!=b.content_revision) THEN 'needs_review' ELSE 'matched' END,COALESCE((SELECT json_extract(fields_json,'$.title_original') FROM effective_editions WHERE edition_id=b.id),'')`+from+" ORDER BY "+order+direction+",b.id LIMIT ? OFFSET ?", append(args, v.PageSize, (v.Page-1)*v.PageSize)...)
	if e != nil {
		return out, e
	}
	ids := []string{}
	for rows.Next() {
		hit := model.SearchHit{Kind: "edition", MatchCount: 1, Metadata: model.MetadataDetails{Fields: model.Metadata{Tags: []model.TagName{}}, Tags: []model.Tag{}, Overrides: map[string]model.Override{}, SelectionRevision: 1}}
		var original string
		if e = rows.Scan(&hit.ID, &hit.State.Favorite, &hit.State.Hidden, &hit.State.Revision, &hit.Metadata.State, &original); e != nil {
			rows.Close()
			return out, e
		}
		if original != "" {
			hit.Metadata.Fields.TitleOriginal = &original
		}
		hit.MatchIDs = []string{hit.ID}
		ids = append(ids, hit.ID)
		out.Items = append(out.Items, hit)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > 0 {
		rows, e = scopeRows(ctx, tx, "SELECT "+s.editionColumns()+" FROM "+editionRecordSQL+" editions WHERE id IN("+placeholders(len(ids))+")", stringArgs(ids)...)
		if e != nil {
			return out, e
		}
		books := map[string]model.Edition{}
		for rows.Next() {
			book, e := scanEdition(rows)
			if e != nil {
				rows.Close()
				return out, e
			}
			books[book.ID] = *book
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		for i := range out.Items {
			out.Items[i].Representative = books[out.Items[i].ID]
		}
	}
	if s.Version >= 26 {
		page := make([]model.Edition, len(out.Items))
		for i := range out.Items {
			page[i] = out.Items[i].Representative
		}
		if e = readingForEditions(ctx, tx, page); e != nil {
			return out, e
		}
		for i := range out.Items {
			out.Items[i].Representative = page[i]
		}
	}
	return out, tx.Commit()
}

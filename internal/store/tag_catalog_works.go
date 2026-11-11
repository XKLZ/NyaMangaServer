package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"strings"
)

// The tag filter precedes Work identity grouping. Representatives come from all
// eligible members, not only the matching Editions. Names retain the existing
// preferred-inheritance rule even when a different cover must be displayed.
const tagWorksCTE = `WITH eligible AS MATERIALIZED (
 SELECT e.id,e.work_id,COALESCE(eb.title,f.filename_title) title,f.content_revision,COALESCE(cs.revision,0) cover_revision
 FROM editions e JOIN files f ON f.edition_id=e.id JOIN libraries l ON l.id=e.library_id
 LEFT JOIN effective_editions eb ON eb.edition_id=e.id LEFT JOIN cover_selections cs ON cs.edition_id=e.id
 WHERE e.library_id=? AND e.trashed_at IS NULL AND l.trashed_at IS NULL
 ), tagged AS MATERIALIZED(SELECT e.* FROM eligible e JOIN effective_edition_tags et ON et.edition_id=e.id WHERE et.tag_id=?),
 named AS MATERIALIZED(SELECT w.id,w.library_id,w.revision,w.preferred_edition_id,COALESCE(json_extract(eb.fields_json,'$.title_original'),'') title_original,COALESCE(w.name_override,eb.title,f.filename_title) name
 FROM works w JOIN files f ON f.edition_id=w.preferred_edition_id LEFT JOIN effective_editions eb ON eb.edition_id=w.preferred_edition_id
 WHERE w.id IN(SELECT work_id FROM tagged)),
 matched AS MATERIALIZED(SELECT DISTINCT w.* FROM tagged t JOIN named w ON w.id=t.work_id WHERE ?='' OR instr(nya_normalize_v1(w.name),?)>0 OR instr(nya_normalize_v1(t.title),?)>0),
 representatives AS(SELECT e.*,row_number() OVER(PARTITION BY e.work_id ORDER BY (e.id=w.preferred_edition_id) DESC,e.id) rn
 FROM eligible e JOIN matched w ON w.id=e.work_id)
`

func (s *Store) TagCatalogWorks(ctx context.Context, id string, in model.TagCatalogInput) (model.TagWorkPage, error) {
	out := model.TagWorkPage{Items: []model.TagCatalogWork{}, Page: in.Page, PageSize: in.PageSize}
	requestSnapshot := in.Snapshot
	in.Snapshot = ""
	tx, base, _, e := s.tagCatalogRead(ctx, in, "works:"+id)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, e = tagAnchor(ctx, tx, id); e != nil {
		return out, e
	}
	// Cover selection revisions do not update catalog_state. An SQL scalar
	// aggregate adds their monotonic library epoch without exporting member rows.
	// Deleting an Edition/selection goes through catalog-changing permanent purge.
	var covers int64
	if e = scopeRow(ctx, tx, `SELECT COALESCE(sum(cs.revision),0) FROM cover_selections cs JOIN editions e ON e.id=cs.edition_id WHERE e.library_id=?`, in.LibraryID).Scan(&covers); e != nil {
		return out, e
	}
	out.Snapshot = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:covers:%d", base, covers))))
	if requestSnapshot != "" && requestSnapshot != out.Snapshot {
		return out, model.ErrSnapshot
	}
	q := textkey.Normalize(in.Query)
	args := []any{in.LibraryID, id, q, q, q}
	if e = scopeRow(ctx, tx, tagWorksCTE+"SELECT count(*) FROM matched", args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, tagWorksCTE+`SELECT w.id,w.library_id,w.name,w.title_original,w.revision,s.id,s.name,sm.position,s.revision,r.id,r.content_revision,r.cover_revision
 FROM matched w JOIN representatives r ON r.work_id=w.id AND r.rn=1
 LEFT JOIN series_members sm ON sm.work_id=w.id LEFT JOIN series s ON s.id=sm.series_id
 ORDER BY nya_natural_v1(w.name) `+strings.ToUpper(in.Order)+`,w.id LIMIT ? OFFSET ?`, append(args, in.PageSize, (in.Page-1)*in.PageSize)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var w model.TagCatalogWork
		var sid, sname sql.NullString
		var position, revision sql.NullInt64
		if e = rows.Scan(&w.ID, &w.LibraryID, &w.Name, &w.TitleOriginal, &w.Revision, &sid, &sname, &position, &revision, &w.Cover.EditionID, &w.Cover.ContentRevision, &w.Cover.CoverRevision); e != nil {
			rows.Close()
			return out, e
		}
		w.Cover.WorkID = w.ID
		if sid.Valid {
			w.Series = &model.TagWorkSeries{ID: sid.String, Name: sname.String, Position: int(position.Int64), Revision: revision.Int64}
		}
		out.Items = append(out.Items, w)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

package store

import (
	"context"
	"database/sql"
	"nyamangaserver.local/server/internal/model"
	"strings"
)

func (s *Store) TagCatalogEditions(ctx context.Context, id string, in model.TagCatalogInput) (model.TagEditionPage, error) {
	out := model.TagEditionPage{Items: []model.TagCatalogEdition{}, Page: in.Page, PageSize: in.PageSize}
	tx, snapshot, args, e := s.tagCatalogRead(ctx, in, "editions:"+id)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot = snapshot
	ns, e := tagAnchor(ctx, tx, id)
	if e != nil {
		return out, e
	}
	where := ` WHERE EXISTS(SELECT 1 FROM scoped_tags st WHERE st.edition_id=s.id AND st.tag_id=?)`
	values := append(append([]any{}, args...), id)
	if in.RelatedTagID != "" {
		other, e := tagAnchor(ctx, tx, in.RelatedTagID)
		if e != nil {
			return out, e
		}
		if !((ns == "parody" && other == "character") || (ns == "character" && other == "parody")) {
			return out, model.ErrInput
		}
		if in.Partition != "suppressed" {
			where += ` AND EXISTS(SELECT 1 FROM scoped_tags st WHERE st.edition_id=s.id AND st.tag_id=?)`
			values = append(values, in.RelatedTagID)
		}
	}
	if in.Partition == "suppressed" {
		where = " WHERE 1"
		values = append([]any{}, args...)
	}
	if in.Query != "" {
		where += " AND instr(nya_normalize_v1(s.title),nya_normalize_v1(?))>0"
		values = append(values, in.Query)
	}
	switch in.Partition {
	case "unclassified":
		if ns != "parody" && ns != "character" {
			return out, model.ErrInput
		}
		where += " AND " + unclassifiedPredicate
		values = append(values, id, id)
	case "related", "inferred", "suppressed", "competing", "evidence":
		table := "assignments"
		state := "x.state IN('single_parody','evidence','confirmed','inferred')"
		switch in.Partition {
		case "inferred":
			state = "x.state='inferred'"
		case "suppressed":
			table = "edition_character_suppressions"
			state = "x.suppressed=1"
		case "competing":
			state = "x.state='competing_parodies'"
		case "evidence":
			table = "raw_evidence"
			state = "1"
		}
		where += " AND EXISTS(SELECT 1 FROM " + table + " x WHERE x.edition_id=s.id AND ((x.parody_id=? AND x.character_id=?) OR (x.parody_id=? AND x.character_id=?)) AND " + state + ")"
		values = append(values, id, in.RelatedTagID, in.RelatedTagID, id)
	}
	if e = scopeRow(ctx, tx, tagRelationsCTE+"SELECT count(*),count(DISTINCT s.work_id) FROM scope s"+where, values...).Scan(&out.Total, &out.WorkCount); e != nil {
		return out, e
	}
	// Edition names use the same deterministic normalized natural key as the tag list.
	order := "nya_natural_v1(s.title) " + strings.ToUpper(in.Order) + ",s.id"
	rows, e := scopeRows(ctx, tx, tagRelationsCTE+"SELECT s.id,s.library_id,s.work_id,s.title,s.title_original,s.file_state,s.revision,s.organization_revision FROM scope s"+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(values, in.PageSize, (in.Page-1)*in.PageSize)...)
	if e != nil {
		return out, e
	}
	positions := map[string]int{}
	ids := []any{}
	for rows.Next() {
		var v model.TagCatalogEdition
		v.Relations = []model.EditionCharacterRelation{}
		if e = rows.Scan(&v.ID, &v.LibraryID, &v.WorkID, &v.Title, &v.TitleOriginal, &v.FileState, &v.Revision, &v.OrganizationRevision); e != nil {
			rows.Close()
			return out, e
		}
		positions[v.ID] = len(out.Items)
		ids = append(ids, v.ID)
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(ids) > 0 {
		if e = editionCharacterSummaries(ctx, tx, args, ids, positions, out.Items); e != nil {
			return out, e
		}
	}
	return out, tx.Commit()
}
func editionCharacterSummaries(ctx context.Context, tx *sql.Tx, args, ids []any, positions map[string]int, items []model.TagCatalogEdition) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	query := tagRelationsCTE + `SELECT a.edition_id,a.parody_id,a.character_id,a.state,a.decision,a.decision_revision,a.suppressed,a.suppression_revision,p.name,p.translation,c.name,c.translation FROM assignments a JOIN labels p ON p.id=a.parody_id JOIN labels c ON c.id=a.character_id WHERE a.edition_id IN (` + placeholders + `)
 UNION ALL SELECT c.edition_id,'',c.character_id,CASE WHEN COALESCE(k.n,0)>0 THEN 'known_elsewhere' ELSE 'unclassified' END,'auto',0,0,0,'','',t.name,t.translation FROM characters c JOIN labels t ON t.id=c.character_id LEFT JOIN known_counts k ON k.character_id=c.character_id
 WHERE c.edition_id IN (` + placeholders + `) AND NOT EXISTS(SELECT 1 FROM assignments a WHERE a.edition_id=c.edition_id AND a.character_id=c.character_id) UNION ALL SELECT x.edition_id,x.parody_id,x.character_id,'suppressed',COALESCE(d.decision,'auto'),COALESCE(d.revision,0),x.suppressed,x.revision,p.name,p.translation,c.name,c.translation FROM edition_character_suppressions x JOIN labels p ON p.id=x.parody_id JOIN labels c ON c.id=x.character_id LEFT JOIN parody_character_decisions d ON d.parody_id=x.parody_id AND d.character_id=x.character_id
 WHERE x.edition_id IN (` + placeholders + `) AND x.suppressed=1 AND NOT EXISTS(SELECT 1 FROM assignments a WHERE a.edition_id=x.edition_id AND a.parody_id=x.parody_id AND a.character_id=x.character_id) ORDER BY 1,2,3`
	values := append(append(append([]any{}, args...), ids...), ids...)
	values = append(values, ids...)
	rows, e := scopeRows(ctx, tx, query, values...)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var r model.EditionCharacterRelation
		if e = rows.Scan(&id, &r.ParodyID, &r.CharacterID, &r.State, &r.Decision, &r.DecisionRevision, &r.Suppressed, &r.SuppressionRevision, &r.ParodyName, &r.ParodyTranslation, &r.CharacterName, &r.CharacterTranslation); e != nil {
			return e
		}
		r.StateLabel = relationLabel(r.State)
		i := positions[id]
		items[i].Relations = append(items[i].Relations, r)
	}
	return rows.Err()
}

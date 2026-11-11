package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"strings"
)

func (s *Store) tagCatalogRead(ctx context.Context, in model.TagCatalogInput, kind string) (*sql.Tx, string, []any, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, "", nil, e
	}
	fail := func(e error) (*sql.Tx, string, []any, error) { tx.Rollback(); return nil, "", nil, e }
	if e = tagProjectionReady(ctx, tx); e != nil {
		return fail(e)
	}
	var disabled bool
	var trash sql.NullInt64
	if e = scopeRow(ctx, tx, "SELECT disabled,trashed_at FROM libraries WHERE id=?", in.LibraryID).Scan(&disabled, &trash); e != nil {
		if e == sql.ErrNoRows {
			return fail(model.ErrNotFound)
		}
		return fail(e)
	}
	if trash.Valid {
		return fail(model.ErrNotFound)
	}
	generation, state, e := activeDictionary(ctx, tx)
	if e != nil {
		return fail(e)
	}
	catalog, e := catalogRevision(ctx, tx)
	if e != nil {
		return fail(e)
	}
	var manual, relations int64
	var version sql.NullString
	if e = scopeRow(ctx, tx, "SELECT manual_revision FROM dictionary_state WHERE singleton=1").Scan(&manual); e != nil {
		return fail(e)
	}
	if e = scopeRow(ctx, tx, "SELECT active_version FROM translation_state WHERE singleton=1").Scan(&version); e != nil {
		return fail(e)
	}
	if e = scopeRow(ctx, tx, "SELECT revision FROM tag_relation_projection WHERE singleton=1").Scan(&relations); e != nil {
		return fail(e)
	}
	frozen := in
	if frozen.View == "all" {
		frozen.View = ""
	}
	frozen.Page = 0
	frozen.Snapshot = ""
	frozen.Query = textkey.Normalize(frozen.Query)
	key, _ := json.Marshal(frozen)
	snapshot := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%s:%s:%d:%d:%s:%s", kind, catalog, generation, state, version.String, manual, relations, in.LibraryID, key))))
	// Hash exact per-role generations with bounded memory rather than summing
	// int64 revisions (overflow or floating-point epochs can miss a change).
	imageRows, e := scopeRows(ctx, tx, "SELECT tag_id,role,revision FROM tag_images ORDER BY tag_id,role")
	if e != nil {
		return fail(e)
	}
	imageHash := sha256.New()
	hasImages := false
	for imageRows.Next() {
		var id, role string
		var rev int64
		if e = imageRows.Scan(&id, &role, &rev); e != nil {
			imageRows.Close()
			return fail(e)
		}
		hasImages = true
		fmt.Fprintf(imageHash, "%d:%s:%s:%d\n", len(id), id, role, rev)
	}
	e = imageRows.Err()
	imageRows.Close()
	if e != nil {
		return fail(e)
	}
	if hasImages {
		snapshot = fmt.Sprintf("%x", sha256.Sum256(append([]byte(snapshot+":tag-images:"), imageHash.Sum(nil)...)))
	}

	epoch, err := enrichmentRevision(ctx, tx)
	if err != nil {
		return fail(err)
	}
	if epoch > 0 {
		snapshot = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:enhancements:%d", snapshot, epoch))))
	}
	snapshot, e = tagFollowSnapshot(ctx, tx, snapshot)
	if e != nil {
		return fail(e)
	}
	if in.Snapshot != "" && in.Snapshot != snapshot {
		return fail(model.ErrSnapshot)
	}
	return tx, snapshot, []any{in.LibraryID, generation, state != "disabled"}, nil
}
func scanCatalogTag(row scanner) (model.CatalogTag, error) {
	var t model.CatalogTag
	e := row.Scan(&t.ID, &t.Namespace, &t.Name, &t.Translation, &t.TranslationOrigin, &t.TranslationRevision, &t.Category, &t.Placeholder, &t.EditionCount, &t.WorkCount)
	if e == sql.ErrNoRows {
		e = model.ErrNotFound
	}
	return t, e
}
func tagAnchor(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var ns string
	e := scopeRow(ctx, tx, "SELECT namespace FROM tags WHERE id=?", id).Scan(&ns)
	if e == sql.ErrNoRows {
		return "", model.ErrNotFound
	}
	return ns, e
}
func (s *Store) TagCatalog(ctx context.Context, in model.TagCatalogInput) (model.TagCatalogPage, error) {
	out := model.TagCatalogPage{Items: []model.CatalogTag{}, Page: in.Page, PageSize: in.PageSize}
	tx, snapshot, args, e := s.tagCatalogRead(ctx, in, "catalog")
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot = snapshot
	where := ` WHERE (edition_count>0 OR ?) AND (?='' OR category=?) AND (?='' OR namespace=?) AND (instr(nya_normalize_v1(name),?)>0 OR instr(nya_normalize_v1(translation),?)>0)`
	q := textkey.Normalize(in.Query)
	if in.FollowedOnly {
		where = ` WHERE EXISTS(SELECT 1 FROM tag_follows f WHERE f.tag_id=labels.id AND f.account_id=? AND f.followed=1) AND (?='' OR category=?) AND (?='' OR namespace=?) AND (instr(nya_normalize_v1(name),?)>0 OR instr(nya_normalize_v1(translation),?)>0)`
		args = append(args, model.TagAccount(ctx))
	} else {
		args = append(args, in.IncludeUnused)
	}
	args = append(args, in.Category, in.Category, in.Namespace, in.Namespace, q, q)
	if e = scopeRow(ctx, tx, tagRelationsCTE+"SELECT count(*) FROM labels"+where, args...).Scan(&out.Total); e != nil {
		return out, e
	}
	order := "nya_natural_v1(translation)"
	if in.Sort == "usage" {
		order = "edition_count"
	}
	order += " " + strings.ToUpper(in.Order) + ",namespace,name,id"
	rows, e := scopeRows(ctx, tx, tagRelationsCTE+"SELECT "+catalogTagColumns+" FROM labels"+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(args, in.PageSize, (in.Page-1)*in.PageSize)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		v, e := scanCatalogTag(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	tags := make([]*model.CatalogTag, 0, len(out.Items))
	for i := range out.Items {
		tags = append(tags, &out.Items[i])
	}
	if e = enrichTagFollows(ctx, tx, tags...); e != nil {
		return out, e
	}
	if e = enrichTagImages(ctx, tx, tags...); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func countUnclassified(ctx context.Context, tx *sql.Tx, args []any, id, ns string) (int, error) {
	if ns != "parody" && ns != "character" {
		return 0, nil
	}
	var n int
	values := append(append([]any{}, args...), id, id, id)
	e := scopeRow(ctx, tx, tagRelationsCTE+`SELECT count(*) FROM scope s WHERE EXISTS(SELECT 1 FROM scoped_tags st WHERE st.edition_id=s.id AND st.tag_id=?) AND `+unclassifiedPredicate, values...).Scan(&n)
	return n, e
}
func (s *Store) TagCatalogDetail(ctx context.Context, id string, in model.TagCatalogInput) (model.TagCatalogDetail, error) {
	out := model.TagCatalogDetail{LibraryID: in.LibraryID}
	tx, snapshot, args, e := s.tagCatalogRead(ctx, in, "detail:"+id)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot = snapshot
	out.CatalogTag, e = scanCatalogTag(scopeRow(ctx, tx, tagRelationsCTE+"SELECT "+catalogTagColumns+" FROM labels WHERE id=?", append(args, id)...))
	if e != nil {
		return out, e
	}
	if e = scopeRow(ctx, tx, "SELECT disabled FROM libraries WHERE id=?", in.LibraryID).Scan(&out.LibraryDisabled); e != nil {
		return out, e
	}
	out.UnclassifiedEditionCount, e = countUnclassified(ctx, tx, args, id, out.Namespace)
	if e != nil {
		return out, e
	}
	if e = enrichTagFollows(ctx, tx, &out.CatalogTag); e != nil {
		return out, e
	}
	if e = enrichTagImages(ctx, tx, &out.CatalogTag); e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func relationLabel(state string) string {
	switch state {
	case "confirmed":
		return "人工确认"
	case "excluded":
		return "全局排除"
	case "single_parody":
		return "单原作证据"
	case "evidence":
		return "本库可靠证据"
	case "inferred":
		return "本书唯一剩余推断"
	case "suppressed":
		return "本书已否决自动推断"
	case "competing_parodies":
		return "原作归属竞争"
	case "known_elsewhere":
		return "可靠归属与本书矛盾"
	case "unclassified":
		return "未归类"
	default:
		return "自动（无当前证据）"
	}
}
func (s *Store) TagCatalogRelations(ctx context.Context, id string, in model.TagCatalogInput) (model.TagRelationPage, error) {
	out := model.TagRelationPage{Items: []model.CharacterRelation{}, Page: in.Page, PageSize: in.PageSize}
	tx, snapshot, args, e := s.tagCatalogRead(ctx, in, "relations:"+id)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Snapshot = snapshot
	ns, e := tagAnchor(ctx, tx, id)
	if e != nil {
		return out, e
	}
	if ns != "parody" && ns != "character" {
		return out, model.ErrInput
	}
	out.UnclassifiedEditionCount, e = countUnclassified(ctx, tx, args, id, ns)
	if e != nil {
		return out, e
	}
	where := " WHERE (pc.parody_id=? OR pc.character_id=?) AND (?='' OR pc.parody_id=? OR pc.character_id=?) AND (instr(nya_normalize_v1(pt.name||' '||pt.translation||' '||ct.name||' '||ct.translation),?)>0)"
	if in.View == "display" {
		where += " AND (pc.decision='confirmed' OR (pc.decision='auto' AND pc.edition_count>0))"
	}
	q := textkey.Normalize(in.Query)
	values := append(append([]any{}, args...), id, id, in.RelatedTagID, in.RelatedTagID, in.RelatedTagID, q)
	from := " FROM pair_counts pc JOIN labels pt ON pt.id=pc.parody_id JOIN labels ct ON ct.id=pc.character_id"
	if e = scopeRow(ctx, tx, tagRelationsCTE+"SELECT count(*)"+from+where, values...).Scan(&out.Total); e != nil {
		return out, e
	}
	cols := func(alias string) string {
		parts := strings.Split(catalogTagColumns, ",")
		for i := range parts {
			parts[i] = alias + "." + parts[i]
		}
		return strings.Join(parts, ",")
	}
	order := "nya_natural_v1(CASE WHEN pt.id=? THEN ct.translation ELSE pt.translation END)"
	if in.Sort == "usage" {
		order = "pc.edition_count"
	} else {
		values = append(values, id)
	}
	order += " " + strings.ToUpper(in.Order) + ",pc.parody_id,pc.character_id"
	rows, e := scopeRows(ctx, tx, tagRelationsCTE+"SELECT "+cols("pt")+","+cols("ct")+",pc.decision,pc.revision,pc.evidence_count,pc.seed_count,pc.edition_count,pc.work_count,pc.inferred_count,pc.suppressed_count,pc.competing_count"+from+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", append(values, in.PageSize, (in.Page-1)*in.PageSize)...)
	if e != nil {
		return out, e
	}
	fields := func(t *model.CatalogTag) []any {
		return []any{&t.ID, &t.Namespace, &t.Name, &t.Translation, &t.TranslationOrigin, &t.TranslationRevision, &t.Category, &t.Placeholder, &t.EditionCount, &t.WorkCount}
	}
	for rows.Next() {
		var r model.CharacterRelation
		dest := append(fields(&r.Parody), fields(&r.Character)...)
		dest = append(dest, &r.Decision, &r.Revision, &r.EvidenceEditionCount, &r.SeedEditionCount, &r.EditionCount, &r.WorkCount, &r.InferredEditionCount, &r.SuppressedEditionCount, &r.CompetingEditionCount)
		if e = rows.Scan(dest...); e != nil {
			rows.Close()
			return out, e
		}
		r.State = r.Decision
		if r.Decision == "auto" {
			switch {
			case r.CompetingEditionCount > 0:
				r.State = "competing_parodies"
			case r.EditionCount > 0 && r.InferredEditionCount == r.EditionCount && r.SeedEditionCount == 0:
				r.State = "inferred"
			case r.EditionCount > 0:
				r.State = "evidence"
			case r.SuppressedEditionCount > 0:
				r.State = "suppressed"
			}
		}
		r.StateLabel = relationLabel(r.State)
		out.Items = append(out.Items, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	tags := make([]*model.CatalogTag, 0, len(out.Items)*2)
	for i := range out.Items {
		tags = append(tags, &out.Items[i].Parody, &out.Items[i].Character)
	}
	if e = enrichTagFollows(ctx, tx, tags...); e != nil {
		return out, e
	}
	if e = enrichTagImages(ctx, tx, tags...); e != nil {
		return out, e
	}
	return out, tx.Commit()
}

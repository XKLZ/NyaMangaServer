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

func activeDictionary(ctx context.Context, q queryer) (string, string, error) {
	enabled, err := enhancementEnabled(ctx, q, model.EnhancementDictionary)
	if err != nil {
		return "", "unavailable", err
	}
	if !enabled {
		return "", "disabled", nil
	}
	var id sql.NullString
	e := scopeRow(ctx, q, `SELECT g.id FROM dictionary_state s JOIN dictionary_generations g ON g.id=s.active_generation JOIN translation_state t ON t.singleton=1 JOIN translation_versions v ON v.id=t.active_version WHERE g.state='complete' AND g.version=v.id AND g.digest=v.digest AND g.checkpoint=g.expected_count`).Scan(&id)
	if e == nil && id.Valid {
		return id.String, "ready", nil
	}
	if e != nil && e != sql.ErrNoRows {
		return "", "unavailable", e
	}
	var version sql.NullString
	if e = scopeRow(ctx, q, "SELECT active_version FROM translation_state WHERE singleton=1").Scan(&version); e != nil {
		return "", "unavailable", e
	}
	if !version.Valid {
		return "", "not_downloaded", nil
	}
	return "", "unavailable", nil
}
func (s *Store) TagSuggestions(ctx context.Context, in model.TagSuggestionInput) (model.TagSuggestions, error) {
	out := model.TagSuggestions{Items: []model.TagSuggestion{}, Page: in.Page, PageSize: in.PageSize}
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	id, state, e := activeDictionary(ctx, tx)
	if e != nil {
		return out, e
	}
	out.DictionaryState = state
	catalog, e := catalogRevision(ctx, tx)
	if e != nil {
		return out, e
	}
	var manual int64
	if e = scopeRow(ctx, tx, "SELECT manual_revision FROM dictionary_state WHERE singleton=1").Scan(&manual); e != nil {
		return out, e
	}
	frozen := in
	frozen.Query = textkey.Normalize(in.Query)
	frozen.Page = 0
	frozen.Snapshot = ""
	key, _ := json.Marshal(frozen)
	out.Snapshot = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%s:%d:%s", catalog, id, state, manual, key))))
	epoch, err := enrichmentRevision(ctx, tx)
	if err != nil {
		return out, err
	}
	if epoch > 0 {
		out.Snapshot = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:enhancements:%d", out.Snapshot, epoch))))
	}
	out.Snapshot, e = tagFollowSnapshot(ctx, tx, out.Snapshot)
	if e != nil {
		return out, e
	}
	if in.Snapshot != "" && in.Snapshot != out.Snapshot {
		return out, model.ErrSnapshot
	}
	cte := `WITH used AS(SELECT bt.tag_id,count(*) usage_count FROM effective_edition_tags bt JOIN ` + editionRecordSQL + ` b ON b.id=bt.edition_id JOIN libraries l ON l.id=b.library_id WHERE b.trashed_at IS NULL AND l.trashed_at IS NULL AND l.disabled=0 GROUP BY bt.tag_id),
 dict AS(SELECT * FROM dictionary_entries WHERE generation_id=?),
 candidates AS(
 SELECT COALESCE(f.followed,0) followed,COALESCE(f.revision,0) follow_revision,t.id tag_id,t.namespace,t.name,COALESCE(u.usage_count,0) usage_count,m.text manual_translation,COALESCE(d.translation,r.text) remote_translation,d.name IS NOT NULL in_dictionary,nya_normalize_v1(t.name) normalized_name,nya_normalize_v1(COALESCE(m.text,'')) normalized_manual,COALESCE(d.normalized_translation,nya_normalize_v1(COALESCE(r.text,''))) normalized_remote
 FROM tags t LEFT JOIN tag_follows f ON f.tag_id=t.id AND f.account_id=? LEFT JOIN used u ON u.tag_id=t.id LEFT JOIN tag_translations m ON m.tag_id=t.id AND m.origin='manual' AND m.locale='zh-CN' AND m.dataset='' LEFT JOIN tag_translations r ON r.tag_id=t.id AND r.origin='remote' AND r.locale='zh-CN' AND r.dataset=(SELECT active_version FROM translation_state WHERE singleton=1) LEFT JOIN dict d ON d.namespace=t.namespace AND d.name=t.name
 UNION ALL SELECT 0,0,NULL,d.namespace,d.name,0,NULL,d.translation,1,d.normalized_name,'',d.normalized_translation FROM dict d WHERE NOT EXISTS(SELECT 1 FROM tags t WHERE t.namespace=d.namespace AND t.name=d.name)
 ), filtered AS(SELECT * FROM candidates WHERE (?='' OR namespace=?) AND (instr(normalized_name,?)>0 OR instr(normalized_manual,?)>0 OR instr(normalized_remote,?)>0)`
	if state == "disabled" {
		cte = strings.ReplaceAll(cte, "LEFT JOIN tag_translations r ON", "LEFT JOIN tag_translations r ON 0=1 AND")
	}
	query := textkey.Normalize(in.Query)
	args := []any{id, model.TagAccount(ctx), in.Namespace, in.Namespace, query, query, query}
	if len(in.Exclude) > 0 {
		exclude := []string{}
		for _, t := range in.Exclude {
			exclude = append(exclude, "(namespace=? AND name=?)")
			args = append(args, t.Namespace, t.Name)
		}
		cte += " AND NOT(" + strings.Join(exclude, " OR ") + ")"
	}
	cte += ") "
	if e = scopeRow(ctx, tx, cte+"SELECT count(*) FROM filtered", args...).Scan(&out.Total); e != nil {
		return out, e
	}
	rows, e := scopeRows(ctx, tx, cte+`SELECT namespace,name,COALESCE(manual_translation,remote_translation,name),CASE WHEN manual_translation IS NOT NULL THEN 'manual' WHEN remote_translation IS NOT NULL THEN 'remote' ELSE 'original' END,usage_count,tag_id,in_dictionary,followed,follow_revision FROM filtered ORDER BY followed DESC,usage_count DESC,(tag_id IS NOT NULL) DESC,normalized_name,namespace,name LIMIT ? OFFSET ?`, append(args, in.PageSize, (in.Page-1)*in.PageSize)...)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var v model.TagSuggestion
		var tag sql.NullString
		var match bool
		if e = rows.Scan(&v.Namespace, &v.Name, &v.Translation, &v.Origin, &v.Usage, &tag, &match, &v.Followed, &v.FollowRevision); e != nil {
			rows.Close()
			return out, e
		}
		v.LocalExists = tag.Valid
		if tag.Valid {
			value := tag.String
			v.TagID = &value
		}
		if state == "ready" {
			v.DictionaryMatch = &match
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func tagMembership(ctx context.Context, q queryer, tags []model.Tag) error {
	if len(tags) == 0 {
		return nil
	}
	// Historical-schema tests retain their old projection behavior.
	var exists int
	if e := scopeRow(ctx, q, "SELECT count(*) FROM sqlite_master WHERE name='dictionary_state'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	id, state, e := activeDictionary(ctx, q)
	if state == "disabled" {
		for i := range tags {
			tags[i].DictionaryState = "disabled"
			tags[i].DictionaryMatch = nil
		}
	}
	if e != nil || state != "ready" {
		return e
	}
	args := []any{id}
	conditions := []string{}
	for _, t := range tags {
		conditions = append(conditions, "(namespace=? AND name=?)")
		args = append(args, t.Namespace, t.Name)
	}
	rows, e := scopeRows(ctx, q, "SELECT namespace,name,translation FROM dictionary_entries WHERE generation_id=? AND ("+strings.Join(conditions, " OR ")+")", args...)
	if e != nil {
		return e
	}
	matches := map[model.TagName]bool{}
	translations := map[model.TagName]sql.NullString{}
	for rows.Next() {
		var t model.TagName
		var text sql.NullString
		if e = rows.Scan(&t.Namespace, &t.Name, &text); e != nil {
			rows.Close()
			return e
		}
		matches[t] = true
		translations[t] = text
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for i, t := range tags {
		match := matches[model.TagName{Namespace: t.Namespace, Name: t.Name}]
		tags[i].DictionaryMatch = &match
		if text := translations[model.TagName{Namespace: t.Namespace, Name: t.Name}]; text.Valid && t.Origin != "manual" {
			tags[i].Translation = text.String
			tags[i].Origin = "remote"
		}
	}
	return nil
}

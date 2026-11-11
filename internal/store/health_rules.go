package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/textkey"
	"reflect"

	"modernc.org/sqlite"
)

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("nya_health_bits_v1", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		bits, ok := args[0].([]byte)
		n, valid := args[1].(int64)
		if !ok || !valid || n < 0 || n > 8<<20 || int64(len(bits)) != (n+7)/8 {
			return int64(0), nil
		}
		if n%8 != 0 && len(bits) > 0 && bits[len(bits)-1]&^byte((1<<uint(n%8))-1) != 0 {
			return int64(0), nil
		}
		return int64(1), nil
	})

	sqlite.MustRegisterDeterministicScalarFunction("nya_health_hash_v1", 1, func(_ *sqlite.FunctionContext, v []driver.Value) (driver.Value, error) {
		s, _ := v[0].(string)
		return healthID(s), nil
	})
}
func healthStampExpr(kind, arg string, parsing ...bool) string {
	if kind == "cover_cache" && len(parsing) > 0 && parsing[0] {
		old := healthStampExpr(kind, arg)
		return fmt.Sprintf("CASE WHEN EXISTS(SELECT 1 FROM files pf WHERE pf.edition_id=%s AND (pf.parsing_revision<>1 OR pf.encoding_override IS NOT NULL OR pf.parser_version<>'natural-bmp-v2' OR pf.directory_digest<>'' OR pf.effective_encoding<>'')) THEN nya_health_hash_v1((%s)||COALESCE((SELECT json_array(pf.parsing_revision,pf.encoding_override,pf.parser_version,pf.directory_digest,pf.effective_encoding) FROM files pf WHERE pf.edition_id=%s),'')) ELSE (%s) END", arg, old, arg, old)
	}

	if kind == "tag_cache" {
		return fmt.Sprintf("COALESCE((SELECT nya_health_hash_v1(json_array(t.revision,a.digest,a.size)) FROM tag_images t JOIN assets a ON a.id=t.asset_id WHERE t.tag_id||':'||t.role=%s),'missing')", arg)
	}
	if kind == "cover_cache" {
		return fmt.Sprintf("COALESCE((SELECT nya_health_hash_v1(json_array(f.content_revision,f.relative_path,f.size,f.mtime_ns,c.mode,c.revision,c.asset_id,c.page_index,c.content_revision,c.entry_index,c.entry_name,c.directory_version,l.root_key,l.root_identity,l.legacy_zip_encoding,l.disabled,l.trashed_at,l.revision)) FROM files f JOIN libraries l ON l.id=f.library_id LEFT JOIN cover_selections c ON c.edition_id=f.edition_id WHERE f.edition_id=%s),'missing')", arg)
	}
	if kind == "series_block" {
		return fmt.Sprintf("COALESCE((SELECT nya_health_hash_v1(json_array(b.storage_key,b.sha256,b.size,b.records,g.execution_version,g.profiles_json,r.input_token,j.generation)) FROM series_evidence_blocks b JOIN series_evidence_generations g ON g.job_id=b.job_id JOIN series_discovery_runs r ON r.job_id=g.job_id JOIN jobs j ON j.id=b.job_id WHERE b.job_id||':'||b.block_key=%s),'missing')", arg)
	}
	if kind == "recommendation_index" {
		return fmt.Sprintf("COALESCE((SELECT nya_health_hash_v1(json_array(feature_fingerprint,algorithm_version,model_sha256,feature_digest)) FROM recommendation_features WHERE edition_id||':'||algorithm=%s),'missing')", arg)
	}
	table, key := "", "id"
	switch kind {
	case "edition", "source":
		table = "editions"
	case "work":
		table = "works"
	case "series":
		table = "series"
	case "asset":
		table = "assets"
	case "tag":
		table = "tags"
	case "dictionary":
		table = "translation_state"
		key = "singleton"
		arg = "1"
	default:
		return "''"
	}
	raw := fmt.Sprintf("(SELECT json_object('row',json_array(%s)) FROM %s WHERE %s=%s)", healthStampColumns(kind), table, key, arg)
	if kind == "edition" {
		raw += fmt.Sprintf(`||(SELECT json_group_array(json_array(f.relative_path,f.filename_title,f.content_revision,f.file_state,f.size,f.mtime_ns,f.representative_sha1,f.representative_algorithm,m.revision,ms.selection_revision)) FROM files f LEFT JOIN edition_sources x ON x.edition_id=f.edition_id LEFT JOIN external_metadata m ON m.id=x.metadata_id LEFT JOIN edition_metadata_state ms ON ms.edition_id=f.edition_id WHERE f.edition_id=%s)||(SELECT json_group_array(json_array(field,mode,value_json)) FROM (SELECT * FROM field_overrides WHERE edition_id=%s ORDER BY field))`, arg, arg)
	}
	if kind == "source" {
		raw += fmt.Sprintf(`||(SELECT json_array(f.relative_path,f.content_revision,f.file_state,f.size,f.mtime_ns,f.representative_sha1,f.representative_algorithm) FROM files f WHERE f.edition_id=%s)`, arg)
	}

	if kind == "dictionary" {
		raw += `||(SELECT json_array(ds.active_generation,ds.previous_generation,g.state,g.digest,g.checkpoint,g.expected_count,(SELECT count(*) FROM dictionary_entries e WHERE e.generation_id=g.id)) FROM dictionary_state ds LEFT JOIN dictionary_generations g ON g.id=ds.active_generation WHERE ds.singleton=1)||(SELECT COALESCE(json_group_array(json_array(v.id,v.digest,v.cache_key)),'[]') FROM translation_versions v JOIN translation_state t ON t.active_version=v.id)`
	}
	if kind == "asset" {
		raw += fmt.Sprintf(`||(SELECT json_group_array(json_array(tag_id,role,width,height,format,revision)) FROM(SELECT * FROM tag_images WHERE asset_id=%s ORDER BY tag_id,role))`, arg)
	}
	if kind == "source" {
		raw += fmt.Sprintf(`||(SELECT json_array(l.id,l.root_key,l.root_identity,l.legacy_zip_encoding,l.disabled,l.trashed_at,l.revision) FROM editions e JOIN libraries l ON l.id=e.library_id WHERE e.id=%s)`, arg)
	}
	if kind == "work" {
		raw += fmt.Sprintf(`||(SELECT json_group_array(json_array(id,library_id,organization_revision)) FROM(SELECT id,library_id,organization_revision FROM editions WHERE work_id=%s ORDER BY id))`, arg)
	}
	if kind == "series" {
		raw += fmt.Sprintf(`||(SELECT json_group_array(json_array(work_id,position,library_id)) FROM(SELECT sm.work_id,sm.position,w.library_id FROM series_members sm JOIN works w ON w.id=sm.work_id WHERE sm.series_id=%s ORDER BY sm.position,sm.work_id))`, arg)
	}
	return fmt.Sprintf("CASE WHEN NOT EXISTS(SELECT 1 FROM %s WHERE %s=%s) THEN 'missing' ELSE nya_health_hash_v1(%s) END", table, key, arg, raw)
}
func (s *Store) HealthRules(ctx context.Context, o model.HealthObject) ([]model.HealthFinding, error) {
	tx, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	stamp, rev, e := healthStamp(ctx, tx, o.Kind, o.ID)
	if e != nil {
		return nil, e
	}
	if o.Fingerprint != "" && stamp != o.Fingerprint {
		return []model.HealthFinding{{Category: "organization", ObjectKind: o.Kind, ObjectID: o.ID, ObjectRevision: o.Revision, Fingerprint: o.Fingerprint, Status: "stale", Code: "target_changed_since_start"}}, nil
	}
	out := []model.HealthFinding{}
	add := func(category, status, code, repair string) {
		out = append(out, model.HealthFinding{Category: category, ObjectKind: o.Kind, ObjectID: o.ID, ObjectRevision: rev, Fingerprint: stamp, Status: status, Code: code, Repair: repair})
	}
	switch o.Kind {
	case "work":
		var n, valid, cross int
		e = scopeRow(ctx, tx, `SELECT count(e.id),sum(e.id=w.preferred_edition_id),sum(e.library_id<>w.library_id) FROM works w LEFT JOIN editions e ON e.work_id=w.id WHERE w.id=? GROUP BY w.id`, o.ID).Scan(&n, &valid, &cross)
		if e != nil { // NULL sums for empty Works are confirmed structural issues.
			var count int
			if err := scopeRow(ctx, tx, "SELECT count(*) FROM editions WHERE work_id=?", o.ID).Scan(&count); err != nil {
				return out, err
			}
			if count == 0 {
				add("organization", "confirmed", "empty_work", "")
				break
			}
			return out, e
		}
		if n == 0 || n > 100 || cross > 0 {
			add("organization", "confirmed", "work_membership_invalid", "")
		} else if valid != 1 {
			add("organization", "confirmed", "preferred_invalid", "preferred")
		} else {
			add("organization", "normal", "work_valid", "")
		}

	case "series":
		var n, bad, cross, start, distinct int
		e = scopeRow(ctx, tx, `SELECT count(*),COALESCE(sum(position<>rn),0),COALESCE(sum(cross_library),0),COALESCE(min(position),1),count(DISTINCT position) FROM(SELECT sm.position,row_number() OVER(ORDER BY sm.position,sm.work_id) rn,w.library_id<>s.library_id cross_library FROM series_members sm JOIN works w ON w.id=sm.work_id JOIN series s ON s.id=sm.series_id WHERE sm.series_id=?)`, o.ID).Scan(&n, &bad, &cross, &start, &distinct)
		if e != nil {
			return out, e
		}
		var name string
		if e = scopeRow(ctx, tx, "SELECT name FROM series WHERE id=?", o.ID).Scan(&name); e != nil {
			return out, e
		}
		var end int
		if e = scopeRow(ctx, tx, "SELECT COALESCE(max(position),0) FROM series_members WHERE series_id=?", o.ID).Scan(&end); e != nil {
			return out, e
		}
		if n > 1000 || cross > 0 {
			add("organization", "confirmed", "series_membership_invalid", "")
		} else if bad > 0 {
			code := "series_positions_gap"
			if distinct == n && end-start+1 == n && start != 1 {
				code = "series_positions_start"
			}
			add("organization", "confirmed", code, "series_positions")
		} else {
			add("organization", "normal", "series_valid", "")
		}
		out[len(out)-1].Details, _ = json.Marshal(map[string]any{"name": name, "actual_start": start, "expected_start": 1, "member_count": n})

	case "edition":
		var files int
		if e = scopeRow(ctx, tx, "SELECT count(*) FROM files WHERE edition_id=?", o.ID).Scan(&files); e != nil {
			return out, e
		}
		if files != 1 {
			add("organization", "confirmed", "file_identity_missing", "")
			break
		}
		if o.Role == "quick_core" {
			add("organization", "normal", "edition_file_valid", "")
			add("projections", "not_checked", "quick_scope", "")
			break
		}
		expected, properties, e := healthExpectedProjection(ctx, tx, o.ID)
		if e != nil {
			add("projections", "manual", "projection_input_incomplete", "")
			break
		}
		expectedRaw, _ := model.MatchingMetadataJSON(expected)
		var actual, actualTitle string
		e = scopeRow(ctx, tx, "SELECT fields_json,title FROM effective_editions WHERE edition_id=?", o.ID).Scan(&actual, &actualTitle)
		good := e == nil && jsonEqual([]byte(actual), expectedRaw)
		checks := map[string]bool{"fields": good}
		var prop string
		e = scopeRow(ctx, tx, "SELECT properties_json FROM effective_edition_properties WHERE edition_id=?", o.ID).Scan(&prop)
		praw, _ := json.Marshal(properties)
		checks["properties"] = e == nil && jsonEqual([]byte(prop), praw)
		good = good && checks["properties"]
		rows, e := scopeRows(ctx, tx, "SELECT t.namespace,t.name FROM effective_edition_tags et JOIN tags t ON t.id=et.tag_id WHERE et.edition_id=? ORDER BY t.namespace,t.name", o.ID)
		if e != nil {
			return out, e
		}
		tags := map[model.TagName]bool{}
		for rows.Next() {
			var t model.TagName
			rows.Scan(&t.Namespace, &t.Name)
			tags[t] = true
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		if len(tags) != len(expected.Tags) {
			good = false
		}
		for _, tag := range expected.Tags {
			if !tags[tag] {
				good = false
			}
		}
		var langs string
		e = scopeRow(ctx, tx, "SELECT json_group_array(language) FROM(SELECT language FROM effective_edition_languages WHERE edition_id=? ORDER BY position)", o.ID).Scan(&langs)
		languages := model.Languages(properties.Languages)
		if languages == nil {
			languages = []string{}
		}
		wantLang, _ := json.Marshal(languages)
		checks["languages"] = e == nil && jsonEqual([]byte(langs), wantLang)
		good = good && checks["languages"]
		var title, original string
		var sort []byte
		e = scopeRow(ctx, tx, "SELECT title,original_title,sort_key FROM search_documents WHERE edition_id=?", o.ID).Scan(&title, &original, &sort)
		var fallback string
		scopeRow(ctx, tx, "SELECT filename_title FROM files WHERE edition_id=?", o.ID).Scan(&fallback)
		if expected.Title != nil {
			fallback = *expected.Title
		}
		orig := ""
		if expected.TitleOriginal != nil {
			orig = *expected.TitleOriginal
		}
		if actualTitle != fallback {
			good = false
		}
		if e != nil || title != textkey.Normalize(fallback) || original != textkey.Normalize(orig) {
			good = false
		}
		var natural []byte
		if e = scopeRow(ctx, tx, "SELECT nya_natural_v1(?)", fallback).Scan(&natural); e != nil {
			return out, e
		}
		if string(sort) != string(natural) {
			good = false
		}
		if good {
			add("projections", "normal", "effective_valid", "")
		} else {
			add("projections", "confirmed", "effective_mismatch", "projection")
			out[len(out)-1].Details, _ = json.Marshal(checks)
		}
		var evidenceGood bool
		e = scopeRow(ctx, tx, `WITH expected AS(SELECT p.tag_id p,c.tag_id c FROM effective_edition_tags p JOIN tags pt ON pt.id=p.tag_id AND pt.namespace='parody' AND nya_normalize_v1(pt.name) NOT IN('original','origin') JOIN effective_edition_tags c ON c.edition_id=p.edition_id JOIN tags ct ON ct.id=c.tag_id AND ct.namespace='character' WHERE p.edition_id=? AND (SELECT count(*) FROM effective_edition_tags x JOIN tags xt ON xt.id=x.tag_id WHERE x.edition_id=? AND xt.namespace='parody' AND nya_normalize_v1(xt.name) NOT IN('original','origin'))=1), actual AS(SELECT parody_id p,character_id c FROM tag_character_evidence WHERE edition_id=?) SELECT NOT EXISTS(SELECT * FROM expected EXCEPT SELECT * FROM actual) AND NOT EXISTS(SELECT * FROM actual EXCEPT SELECT * FROM expected)`, o.ID, o.ID, o.ID).Scan(&evidenceGood)
		if e != nil {
			return out, e
		}
		if evidenceGood {
			add("projections", "normal", "tag_evidence_valid", "")
		} else {
			add("projections", "confirmed", "tag_evidence_mismatch", "tag_evidence")
		}
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return out, nil
}
func jsonEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
func healthExpectedProjection(ctx context.Context, q queryer, id string) (model.Metadata, model.BookProperties, error) {
	var raw string
	source := model.Metadata{}
	e := scopeRow(ctx, q, "SELECT normalized_json FROM external_metadata m JOIN edition_sources s ON s.metadata_id=m.id WHERE s.edition_id=?", id).Scan(&raw)
	if e == nil {
		if e = json.Unmarshal([]byte(raw), &source); e != nil {
			return source, model.BookProperties{}, e
		}
	} else if e != sql.ErrNoRows {
		return source, model.BookProperties{}, e
	}
	overrides := map[string]model.Override{}
	rows, e := scopeRows(ctx, q, "SELECT field,mode,value_json FROM field_overrides WHERE edition_id=?", id)
	if e != nil {
		return source, model.BookProperties{}, e
	}
	for rows.Next() {
		var key, mode, value string
		if e = rows.Scan(&key, &mode, &value); e != nil {
			rows.Close()
			return source, model.BookProperties{}, e
		}
		op := "set"
		if mode == "empty" {
			op = "empty"
		}
		overrides[key] = model.Override{Op: op, Value: json.RawMessage(value)}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return source, model.BookProperties{}, e
	}
	result, e := model.ProjectMetadata(source, overrides)
	if e != nil {
		return result, model.BookProperties{}, e
	}
	fields := result
	if fields.Title == nil {
		var name string
		if e = scopeRow(ctx, q, "SELECT filename_title FROM files WHERE edition_id=?", id).Scan(&name); e != nil {
			return result, model.BookProperties{}, e
		}
		fields.Title = &name
	}
	return result, model.InferProperties(fields, source.Language, overrides), nil
}

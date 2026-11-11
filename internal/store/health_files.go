package store

import (
	"context"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
)

func (s *Store) HealthFiles(ctx context.Context, checkID, cursor string) ([]model.HealthObject, error) {
	out := []model.HealthObject{}
	rows, e := scopeRows(ctx, s.Read, `SELECT t.tag_id,t.role,t.revision,a.digest FROM tag_images t JOIN assets a ON a.id=t.asset_id WHERE t.tag_id||':'||t.role IN(SELECT object_id FROM health_check_objects WHERE check_id=? AND kind='tag_cache') AND 'tag_cache:'||t.tag_id||':'||t.role>? ORDER BY t.tag_id,t.role LIMIT 50`, checkID, cursor)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var id, role, digest string
		var rev int64
		rows.Scan(&id, &role, &rev, &digest)
		spec := "contain:768x768"
		if role == "background" {
			spec = "center-crop:1024x512"
		}
		key := healthID(digest+":"+role+":"+spec+":png:v1") + ".png"
		out = append(out, model.HealthObject{Kind: "tag_cache", ID: id + ":" + role, Key: "cache/tag-images/v1/" + key[:2] + "/" + key, Role: "tag_cache", Revision: rev, Extra: json.RawMessage(fmt.Sprintf(`{"tag_id":%q,"role":%q}`, id, role))})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	parsingColumns := "1,1"
	if s.Version >= 27 {
		parsingColumns = "f.parsing_revision,COALESCE(c.parsing_revision,1)"
	}
	rows, e = scopeRows(ctx, s.Read, `SELECT e.id,f.content_revision,COALESCE(c.mode,'auto'),COALESCE(c.revision,0),`+parsingColumns+` FROM editions e JOIN files f ON f.edition_id=e.id LEFT JOIN cover_selections c ON c.edition_id=e.id WHERE e.id IN(SELECT object_id FROM health_check_objects WHERE check_id=? AND kind='cover_cache') AND 'cover_cache:'||e.id>? ORDER BY e.id LIMIT 50`, checkID, cursor)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var id, mode string
		var content, rev, parsing, selectionParsing int64
		if e = rows.Scan(&id, &content, &mode, &rev, &parsing, &selectionParsing); e != nil {
			rows.Close()
			return nil, e
		}
		if mode == "auto" {
			selectionParsing = parsing
		}
		key := model.SelectedCoverCacheName(id, content, selectionParsing, rev, mode)

		out = append(out, model.HealthObject{Kind: "cover_cache", ID: id, Key: "cache/covers/v1/" + key[:2] + "/" + key, Role: "cover_cache", Revision: rev, ContentRevision: content, Extra: json.RawMessage(fmt.Sprintf(`{"requires_sources":%t}`, mode != "asset"))})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = scopeRows(ctx, s.Read, `SELECT edition_id,algorithm,feature_fingerprint,model_sha256,algorithm_version,feature_digest FROM recommendation_features WHERE edition_id||':'||algorithm IN(SELECT object_id FROM health_check_objects WHERE check_id=? AND kind='recommendation_index') AND 'recommendation_index:'||edition_id||':'||algorithm>? ORDER BY edition_id,algorithm LIMIT 50`, checkID, cursor)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var id, algorithm, fingerprint, modelHash, version, digest string
		rows.Scan(&id, &algorithm, &fingerprint, &modelHash, &version, &digest)
		raw, _ := json.Marshal(map[string]string{"fingerprint": fingerprint, "model_sha256": modelHash, "algorithm_version": version, "feature_digest": digest})
		out = append(out, model.HealthObject{Kind: "recommendation_index", ID: id + ":" + algorithm, Key: "indexes/recommendations/features/" + algorithm + "/" + fingerprint + ".json", Role: "quarantine_recommendation", Extra: raw})
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	rows, e = scopeRows(ctx, s.Read, `SELECT b.job_id||':'||b.block_key,b.storage_key,b.sha256,b.size,b.records,g.execution_version FROM series_evidence_blocks b JOIN series_evidence_generations g ON g.job_id=b.job_id WHERE b.job_id||':'||b.block_key IN(SELECT object_id FROM health_check_objects WHERE check_id=? AND kind='series_block') AND 'series_block:'||b.job_id||':'||b.block_key>? ORDER BY b.job_id,b.block_key LIMIT 50`, checkID, cursor)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var o model.HealthObject
		var records int
		var version string
		o.Kind = "series_block"
		o.Role = "quarantine_recommendation"
		if e = rows.Scan(&o.ID, &o.Key, &o.Digest, &o.Size, &records, &version); e != nil {
			rows.Close()
			return nil, e
		}
		o.Extra, _ = json.Marshal(map[string]any{"records": records, "execution_version": version})
		out = append(out, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	var digest string
	var ready bool
	e = scopeRow(ctx, s.Read, `SELECT COALESCE(v.digest,''),EXISTS(SELECT 1 FROM dictionary_state ds JOIN dictionary_generations g ON g.id=ds.active_generation WHERE g.state='complete' AND g.version=v.id AND g.digest=v.digest AND g.checkpoint=g.expected_count AND g.expected_count=(SELECT count(*) FROM dictionary_entries WHERE generation_id=g.id)) FROM translation_state t LEFT JOIN translation_versions v ON v.id=t.active_version WHERE t.singleton=1`).Scan(&digest, &ready)
	if e != nil {
		return nil, e
	}
	if digest != "" {
		raw, _ := json.Marshal(map[string]bool{"index_ready": ready})
		out = append(out, model.HealthObject{Kind: "dictionary", ID: "active", Key: "cache/tag-translations/" + digest + ".json", Digest: digest, Role: "dictionary", Extra: raw})
	} else {
		out = append(out, model.HealthObject{Kind: "dictionary", ID: "not_installed", Extra: json.RawMessage(`{"not_installed":true}`)})
	}

	targetRows, e := scopeRows(ctx, s.Read, `SELECT kind,object_id,fingerprint FROM health_check_objects WHERE check_id=? AND kind IN('cover_cache','tag_cache','recommendation_index','dictionary','series_block') AND kind||':'||object_id>? ORDER BY kind||':'||object_id LIMIT 50`, checkID, cursor)
	if e != nil {
		return nil, e
	}
	targets := []model.HealthObject{}
	for targetRows.Next() {
		var o model.HealthObject
		if e = targetRows.Scan(&o.Kind, &o.ID, &o.Fingerprint); e != nil {
			targetRows.Close()
			return nil, e
		}
		targets = append(targets, o)
	}
	e = targetRows.Err()
	targetRows.Close()
	if e != nil {
		return nil, e
	}
	actual := map[string]model.HealthObject{}
	for _, o := range out {
		actual[o.Kind+":"+o.ID] = o
	}
	filtered := []model.HealthObject{}
	for _, target := range targets {
		o, ok := actual[target.Kind+":"+target.ID]
		if !ok {
			o = target
		}
		o.Fingerprint = target.Fingerprint
		filtered = append(filtered, o)
	}
	return filtered, nil
}

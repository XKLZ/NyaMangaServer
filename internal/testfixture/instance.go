package testfixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
)

// SeedInstanceRecovery is an explicit anonymous fixture, never used by server.
// It refuses nonempty library state and seeds nonempty retention categories so
// a whole-instance comparison cannot pass merely because business tables empty.
func SeedInstanceRecovery(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}, data, source, rootKey string) error {
	var n int
	if e := db.QueryRowContext(ctx, "SELECT count(*) FROM libraries").Scan(&n); e != nil {
		return e
	}
	if n != 0 {
		return errors.New("anonymous recovery fixture requires no existing libraries")
	}
	if e := Write(source); e != nil {
		return e
	}
	info, e := os.Stat(source)
	if e != nil {
		return e
	}
	stat := info.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%v:%v", stat.Dev, stat.Ino)
	if _, e = db.ExecContext(ctx, "INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision,scan_interval_minutes,next_scan_at) VALUES('fixture-library',?,'匿名整实例书库','utf8',?,9,15,1)", rootKey, identity); e != nil {
		return e
	}
	for index, path := range []string{"合成1.cbz", "子目录/合成2.zip", "损坏.zip"} {
		id := fmt.Sprintf("fixture-edition-%d", index)
		if e = SeedEdition(ctx, db, id, "fixture-library", path, fmt.Sprintf("匿名作品 %d", index)); e != nil {
			return e
		}
		i, e := os.Stat(filepath.Join(source, filepath.FromSlash(path)))
		if e != nil {
			return e
		}
		state := "available"
		if index == 1 {
			state = "missing"
		}
		if index == 2 {
			state = "corrupt"
		}
		if _, e = db.ExecContext(ctx, "UPDATE files SET size=?,mtime_ns=?,file_state=?,content_revision=7 WHERE edition_id=?", i.Size(), i.ModTime().UnixNano(), state, id); e != nil {
			return e
		}
	}
	var pixels bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 120, G: 180, B: 220, A: 255})
	if e = png.Encode(&pixels, img); e != nil {
		return e
	}
	pixel := pixels.Bytes()
	assetKey := "assets/tags/fixture/retained.png"
	files := map[string][]byte{
		"health/storage-original-verification.json": []byte(`{"format_version":1,"archive_manifest":"/anonymous/manifest.json","manifest_sha256":"anonymous","assets":{}}`),
		assetKey: pixel, "cache/covers/v1/fixture/retained.png": pixel, "cache/tag-images/v1/fixture/retained.png": pixel,
		"cache/import-previews/v1/fixture/retained.png": pixel, "cache/tag-translations/fixture.txt": []byte("anonymous dictionary source"),
		"indexes/recommendations/features/phash/fixture.json":     []byte(`{"algorithm":"phash","feature":"0123456789abcdef","anonymous":true}`),
		"indexes/recommendations/series/fixture/retained.json.gz": []byte("anonymous retained incremental block"),
		"health/repairs/fixture-health/file-0000.bin":             []byte("anonymous previous cache bytes"),
	}
	for key, raw := range files {
		path := filepath.Join(data, filepath.FromSlash(key))
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		if e = os.WriteFile(path, raw, 0600); e != nil {
			return e
		}
	}
	sum := sha256.Sum256(pixel)
	digest := hex.EncodeToString(sum[:])
	if _, e = db.ExecContext(ctx, "INSERT INTO assets VALUES('fixture-asset',?,?,?,1)", assetKey, digest, len(pixel)); e != nil {
		return e
	}
	statements := []string{
		`UPDATE libraries SET scan_actor_id=(SELECT id FROM accounts WHERE singleton=1) WHERE id='fixture-library'`,
		`INSERT INTO accounts(singleton,id,password_hash,auth_revision,password_changed_at) SELECT 2,'fixture-viewer',password_hash,1,1 FROM accounts WHERE singleton=1`,
		`INSERT INTO accounts(singleton,id,password_hash,auth_revision,password_changed_at) SELECT 3,'fixture-editor',password_hash,1,1 FROM accounts WHERE singleton=1`,
		`UPDATE account_profiles SET must_change_password=1 WHERE account_id='fixture-editor'`,
		`INSERT INTO account_library_grants VALUES('fixture-viewer','fixture-library','viewer'),('fixture-editor','fixture-library','editor')`,
		`INSERT INTO reading_states(account_id,edition_id,page_index,recorded_page_count,content_revision,completed,completed_at,last_read_at,read_count,revision) VALUES('fixture-viewer','fixture-edition-0',0,2,7,1,1,2,3,4)`,
		`INSERT INTO reading_sessions(id,account_id,edition_id,idempotency_key,content_revision,started_at) VALUES('fixture-reading','fixture-viewer','fixture-edition-0','fixture-reading-key',7,1)`,
		`INSERT INTO reading_state_requests VALUES('fixture-viewer','fixture-edition-0','fixture-progress-key','anonymous-hash','{"revision":4}')`,
		`INSERT INTO sessions VALUES(CAST('vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv' AS BLOB),'fixture-viewer',1,9999999999)`,

		`INSERT INTO series VALUES('fixture-series','fixture-library','匿名系列',5)`,
		`INSERT INTO series_members VALUES('fixture-series','i:fixture-edition-0',1),('fixture-series','i:fixture-edition-1',2)`,
		`INSERT INTO external_metadata VALUES('fixture-source','ehentai','123456','{"title":"Anonymous original","title_original":"匿名原文","remote_page_count":12,"remote_size_bytes":34567}','{"gid":123456,"filecount":"12","filesize":34567,"anonymous_source":true}',1,1,4)`,
		`INSERT INTO edition_sources VALUES('fixture-edition-0','fixture-source','https://e-hentai.org/g/123456/abcdef1234/','manual',7)`,
		`INSERT INTO edition_metadata_state VALUES('fixture-edition-0','',6)`,
		`INSERT INTO field_overrides VALUES('fixture-edition-0','title','empty','null'),('fixture-edition-1','title','value','"Anonymous manual title"')`,
		`INSERT INTO effective_editions VALUES('fixture-edition-0','','{"title":"","title_original":"匿名原文"}'),('fixture-edition-1','Anonymous manual title','{"title":"Anonymous manual title"}'),('fixture-edition-2','Anonymous damaged source','{}')`,
		`INSERT INTO edition_label_policy VALUES('fixture-edition-0','manual',8,'',0),('fixture-edition-1','auto',4,'fixed-v1',0)`,
		`INSERT INTO edition_version_labels VALUES('fixture-edition-0',0,'匿名自定义'),('fixture-edition-0',1,'人工顺序')`,
		`INSERT INTO edition_automatic_labels VALUES('fixture-edition-1',0,'language','中文')`,
		`INSERT INTO tags VALUES('fixture-parody','parody','Anonymous origin'),('fixture-character','character','Anonymous character')`,
		`INSERT INTO effective_edition_tags VALUES('fixture-edition-0','fixture-parody'),('fixture-edition-0','fixture-character')`,
		`INSERT INTO parody_character_decisions VALUES('fixture-parody','fixture-character','excluded',7,1)`,
		`INSERT INTO edition_character_suppressions VALUES('fixture-edition-0','fixture-parody','fixture-character',1,3,1)`,
		`INSERT INTO tag_follows SELECT id,'fixture-parody',1,9 FROM accounts WHERE singleton=1`,
		`INSERT INTO tag_follow_epochs SELECT id,11 FROM accounts WHERE singleton=1`,
		`INSERT INTO tag_images VALUES('fixture-parody','foreground','fixture-asset',1,1,'png',4)`,
		`INSERT INTO recommendation_materials VALUES('fixture-edition-0','fixture-asset','treec-validation','anonymous-input','anonymous-source-id','anonymous-fingerprint')`,
		`INSERT INTO edition_user_states SELECT id,'fixture-edition-0',1,1,8 FROM accounts WHERE singleton=1`,
		`INSERT INTO saved_searches SELECT 'fixture-search',id,'匿名保存条件',1,'{"q":"anonymous"}',5 FROM accounts WHERE singleton=1`,
		`INSERT INTO jobs(id,library_id,type,state,stage,generation,created_at,updated_at) VALUES('fixture-completed','fixture-library','scan','completed','completed',6,1,2),('fixture-paused','fixture-library','metadata','paused','processing',4,1,2),('fixture-health-job',NULL,'maintenance','completed','completed',2,1,2)`,
		`INSERT INTO job_items(job_id,ordinal,relative_path,size,mtime_ns,state,edition_id,content_revision,input_json) VALUES('fixture-completed',0,'合成1.cbz',1,42,'completed','fixture-edition-0',7,'{"frozen_history":true}'),('fixture-paused',0,'合成1.cbz',1,43,'pending','fixture-edition-0',7,'{"frozen_checkpoint":true}')`,
		`INSERT INTO scan_entries VALUES('fixture-completed',0,'合成1.cbz',1,42)`,
		`INSERT INTO import_batches(id,library_id,request_key,stage,state,revision,stage_decisions_json) VALUES('fixture-import','fixture-library','fixture-import-key','metadata','review',7,'{"versions":{"decision":"skip","reason":"anonymous"}}')`,
		`INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision,metadata_decision,metadata_token,revision) VALUES('fixture-import','fixture-edition-0','new',7,'accepted','frozen-input',4)`,
		`INSERT INTO import_candidates(batch_id,edition_id,revision,request_generation,content_revision,selection_revision,input_json,result_json) VALUES('fixture-import','fixture-edition-0',4,0,7,6,'{"frozen":true}','{"anonymous_candidate":true}')`,
		`INSERT INTO work_proposals(id,job_id,library_id,proposal_key,kind,target_work_id,tier,primary_plan,revision,state,plan_json,created_at) VALUES('fixture-work-proposal','fixture-completed','fixture-library','fixture-work-key','create_work','','strong',1,7,'undone','{"member_ids":["fixture-edition-0","fixture-edition-1"]}',1)`,
		`INSERT INTO work_proposal_decisions SELECT 'fixture-work-key','fixture-work-proposal',id,'undone','{"works":["i:fixture-edition-0","i:fixture-edition-1"]}','{"merged_work":"i:fixture-edition-0"}','["fixture-edition-0","fixture-edition-1"]','{"anonymous_undo":true}',7,1 FROM accounts WHERE singleton=1`,
		`INSERT INTO work_proposal_members VALUES('fixture-work-proposal','fixture-edition-0',0,'new'),('fixture-work-proposal','fixture-edition-1',1,'new')`,
		`INSERT INTO series_proposals(id,job_id,library_id,proposal_key,kind,tier,state,plan_json,created_at,match_key,scope_key) VALUES('fixture-series-proposal','fixture-completed','fixture-library','fixture-series-key','create_series','strong','dismissed','{"work_ids":["i:fixture-edition-0","i:fixture-edition-1"]}',1,'fixture-semantic','global')`,
		`INSERT INTO series_proposal_decisions SELECT 'fixture-series-key','fixture-series-proposal',id,'dismissed','{"series":[]}','{"series":[]}',1 FROM accounts WHERE singleton=1`,
		`INSERT INTO series_match_feedback SELECT 'fixture-library','fixture-semantic','dismissed','fixture-series-proposal',id,0,6,1 FROM accounts WHERE singleton=1`,
		`INSERT INTO health_checks(id,job_id,request_key,mode,scope_json,phase,checked,created_at,finished_at) VALUES('fixture-health','fixture-health-job','fixture-health-key','application','{}',7,3,1,2)`,
		`INSERT INTO health_findings(id,check_id,category,object_kind,object_id,fingerprint,status,code,repair,private_json,details_json,checked_at) VALUES('fixture-finding','fixture-health','cache','cover_cache','fixture-edition-0','frozen','confirmed','cache_corrupt','quarantine','{"anonymous_private":true}','{"anonymous_public":true}',1)`,
		`INSERT INTO health_repair_plans(id,request_key,check_id,input_json,summary_json,created_at,expires_at) VALUES('fixture-health-plan','fixture-health-plan-key','fixture-health','{"findings":["fixture-finding"]}','{"anonymous":true}',1,2)`,
		`INSERT INTO health_repairs(id,plan_id,job_id,request_key,state,checkpoint,created_at,updated_at) VALUES('fixture-health','fixture-health-plan','fixture-health-job','fixture-health-repair-key','completed',1,1,2)`,
		`INSERT INTO health_repair_steps(repair_id,ordinal,action_json,state,before_key,after_fingerprint) VALUES('fixture-health',0,'{"kind":"cover_cache","id":"fixture-edition-0"}','completed','health/repairs/fixture-health/file-0000.bin','frozen-after')`,
		`INSERT INTO gallery_highlight_connections SELECT 'fixture-valid',id,'匿名长期',zeroblob(32),auth_revision,1,NULL,NULL,3 FROM accounts WHERE singleton=1`,
		`INSERT INTO gallery_highlight_connections SELECT 'fixture-revoked',id,'匿名撤销',CAST('01234567890123456789012345678901' AS BLOB),auth_revision,1,NULL,2,7 FROM accounts WHERE singleton=1`,
	}
	for _, statement := range statements {
		if _, e = db.ExecContext(ctx, statement); e != nil {
			return fmt.Errorf("anonymous recovery fixture: %w", e)
		}
	}
	before := files["health/repairs/fixture-health/file-0000.bin"]
	sum = sha256.Sum256(before)
	if _, e = db.ExecContext(ctx, "INSERT INTO health_artifacts VALUES('health/repairs/fixture-health/file-0000.bin','fixture-health',?,?, 'before_file',1)", hex.EncodeToString(sum[:]), len(before)); e != nil {
		return e
	}
	_, e = db.ExecContext(ctx, "UPDATE instance_state SET background_hold=1 WHERE singleton=1")
	if e != nil {
		return e
	}
	return nil
}

package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func schemaSix(t *testing.T) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	names, e := migrations.Files.ReadDir(".")
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range names {
		if strings.HasSuffix(n.Name(), ".sql") && n.Name() < "007" {
			b, e := migrations.Files.ReadFile(n.Name())
			if e != nil {
				t.Fatal(e)
			}
			out[n.Name()] = &fstest.MapFile{Data: b}
		}
	}
	return out
}
func seedWorkUpgrade(t *testing.T, s *Store) {
	t.Helper()
	seedBook(t, s)
	_, e := s.Write.Exec(`
 INSERT INTO books(id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,page_count,file_state,indexed_at,revision) SELECT 'two',library_id,'two.cbz','Second',size,mtime_ns,content_revision,representative_sha1,page_count,'missing',indexed_at,4 FROM books WHERE id='book';
 INSERT INTO books(id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_sha1,page_count,file_state,indexed_at,revision) SELECT 'solo',library_id,'solo.cbz','Standalone',size,mtime_ns,content_revision,representative_sha1,page_count,file_state,indexed_at,revision FROM books WHERE id='book';
 UPDATE logical_items SET kind='group',revision=8 WHERE id='i:book';
 INSERT INTO version_groups VALUES('legacy-g','i:book','lib',6);
 INSERT INTO version_members VALUES('legacy-g','book',1,'中文,彩色',3),('legacy-g','two',0,'日文',5);
 INSERT INTO version_member_labels VALUES('book',0,'中文,彩色'),('book',1,'修订'),('two',0,'日文');
 UPDATE book_items SET item_id='i:book' WHERE book_id='two';DELETE FROM logical_items WHERE id='i:two';
 INSERT INTO series VALUES('series','lib','Anonymous series',5);
 INSERT INTO series_members VALUES('series','i:solo',1),('series','i:book',2);
 INSERT INTO accounts VALUES(1,'account','anonymous-password-hash',2,123);
 INSERT INTO sessions VALUES(zeroblob(32),'account',10,604810);
 INSERT INTO external_metadata VALUES('source','ehentai','123','{}','{}',1,22,3);
 INSERT INTO book_sources VALUES('book','source','https://e-hentai.org/g/123/0123456789/','manual',1),('two','source','https://e-hentai.org/g/123/0123456789/','manual',1);
 INSERT INTO book_metadata_state VALUES('book','https://e-hentai.org/g/123/0123456789/',3);
 INSERT INTO field_overrides VALUES('book','title','value','"Manual"'),('two','languages','value','["中文","日文"]');
 UPDATE effective_books SET title='Manual',fields_json='{"title":"Manual"}' WHERE book_id='book';
 INSERT INTO tags VALUES('tag','artist','anonymous');INSERT INTO effective_book_tags VALUES('book','tag'),('two','tag');
 INSERT INTO book_user_states VALUES('account','book',1,0,7),('account','two',0,1,9);
 INSERT INTO assets VALUES('asset','assets/anonymous.png','digest',10,1);
 INSERT INTO cover_selections(book_id,mode,content_revision,asset_id,revision) VALUES('book','asset',1,'asset',3);
 INSERT INTO jobs(id,library_id,type,state,stage,generation,total,created_at,updated_at) VALUES('paused','lib','metadata','paused','processing',4,1,1,2);
 INSERT INTO job_items(job_id,ordinal,state,book_id,content_revision,selection_revision,input_json,attempt) VALUES('paused',1,'pending','book',1,3,'{"sha1":"0123456789012345678901234567890123456789","algorithm":"first-page-v1"}',2);
 INSERT INTO job_resources VALUES('metadata:book:book','paused',4);
 INSERT INTO jobs(id,type,state,stage,generation,total,created_at,updated_at) VALUES('maint','maintenance','paused','processing',5,1,1,2);
 INSERT INTO job_items(job_id,ordinal,state,input_json) VALUES('maint',1,'pending','{"plan_id":"plan"}');
 INSERT INTO maintenance_plans VALUES('plan','trash','{"operation":"trash","book_ids":["book"],"expected_revisions":{"book:book":1,"item:i:book":8,"group:legacy-g":6}}','{}',9999999999,'maint',1);
 INSERT INTO maintenance_plans VALUES('stale','trash','{"operation":"trash","book_ids":["book"],"expected_revisions":{"book:book":1,"item:i:book":8,"group:legacy-g":5}}','{}',9999999999,NULL,1);

 INSERT INTO source_previews VALUES('preview','book','paused','{}',1,3,1,99999,'pending');
 INSERT INTO saved_searches VALUES('saved','account','Anonymous',1,'{"collapse":"versions","query":""}',3);
 UPDATE books SET representative_algorithm='content-page8-v1',legacy_cover_sha1='legacy',trashed_at=123 WHERE id='two';
 INSERT INTO effective_book_properties SELECT id,NULL,NULL,NULL,NULL,1,'{}' FROM books;
 INSERT INTO effective_book_languages VALUES('two','中文',0),('two','日文',1);
 UPDATE projection_state SET ready=1,last_book_id='two';
 `)
	if e != nil {
		t.Fatal(e)
	}
}
func dumpRows(t *testing.T, s *Store, query string) [][]any {
	t.Helper()
	rows, e := s.Read.Query(query)
	if e != nil {
		t.Fatal(query, e)
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		t.Fatal(e)
	}
	out := [][]any{}
	for rows.Next() {
		v := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range v {
			ptr[i] = &v[i]
		}
		if e = rows.Scan(ptr...); e != nil {
			t.Fatal(e)
		}
		for i, x := range v {
			if b, ok := x.([]byte); ok {
				v[i] = fmt.Sprintf("%x", b)
			}
		}
		out = append(out, v)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestWorkUpgradePreservesIdentityAndOwnedData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	six := schemaSix(t)
	s, e := Open(ctx, dir, six)
	if e != nil {
		t.Fatal(e)
	}
	seedWorkUpgrade(t, s)
	cases := [][2]string{
		{"SELECT * FROM accounts", "SELECT * FROM accounts"}, {"SELECT * FROM sessions", "SELECT * FROM sessions"},
		{"SELECT * FROM books ORDER BY id", "SELECT e.id,e.library_id,f.relative_path,f.filename_title,f.size,f.mtime_ns,f.content_revision,f.representative_sha1,f.page_count,f.file_state,f.error_code,f.indexed_at,e.revision,e.trashed_at,f.representative_algorithm,f.legacy_cover_sha1 FROM editions e JOIN files f ON f.edition_id=e.id ORDER BY e.id"},
		{"SELECT * FROM book_sources ORDER BY book_id", "SELECT * FROM edition_sources ORDER BY edition_id"},
		{"SELECT * FROM book_metadata_state", "SELECT * FROM edition_metadata_state"},
		{"SELECT * FROM field_overrides ORDER BY book_id,field", "SELECT * FROM field_overrides ORDER BY edition_id,field"},
		{"SELECT * FROM effective_books ORDER BY book_id", "SELECT * FROM effective_editions ORDER BY edition_id"},
		{"SELECT * FROM effective_book_tags ORDER BY book_id,tag_id", "SELECT * FROM effective_edition_tags ORDER BY edition_id,tag_id"},
		{"SELECT * FROM effective_book_properties ORDER BY book_id", "SELECT * FROM effective_edition_properties ORDER BY edition_id"},
		{"SELECT * FROM effective_book_languages ORDER BY book_id,position", "SELECT * FROM effective_edition_languages ORDER BY edition_id,position"},
		{"SELECT * FROM book_user_states ORDER BY book_id", "SELECT * FROM edition_user_states ORDER BY edition_id"},
		{"SELECT * FROM assets", "SELECT * FROM assets"}, {"SELECT * FROM cover_selections", "SELECT edition_id,mode,page_index,content_revision,asset_id,revision,entry_index,entry_name,directory_version FROM cover_selections"},
		{"SELECT * FROM jobs", "SELECT id,library_id,type,state,stage,generation,total,done,failed,error_code,parent_job_id,created_at,updated_at FROM jobs"}, {"SELECT * FROM job_items", "SELECT job_id,ordinal,relative_path,size,mtime_ns,state,error_code,edition_id,content_revision,selection_revision,input_json,candidates_json,attempt,preview_id FROM job_items"}, {"SELECT * FROM job_resources", "SELECT * FROM job_resources"},
		{"SELECT * FROM source_previews", "SELECT * FROM source_previews"}, {"SELECT * FROM external_metadata", "SELECT * FROM external_metadata"},
		{"SELECT * FROM series", "SELECT * FROM series"}, {"SELECT * FROM series_members ORDER BY position", "SELECT * FROM series_members ORDER BY position"},
		{"SELECT * FROM version_member_labels ORDER BY book_id,position", "SELECT * FROM edition_version_labels ORDER BY edition_id,position"},
		{"SELECT * FROM saved_searches", "SELECT * FROM saved_searches"},
	}
	before := make([][][]any, len(cases))
	for i, c := range cases {
		before[i] = dumpRows(t, s, c[0])
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	for i, c := range cases {
		after := dumpRows(t, s, c[1])
		if !reflect.DeepEqual(before[i], after) {
			t.Fatalf("changed owned data %s\nbefore=%v\nafter=%v", c[0], before[i], after)
		}
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
	plan, e := s.MaintenancePlan(ctx, "plan")
	if e != nil {
		t.Fatal(e)
	}
	if plan.Input.Expected["work:i:book"] != 8 || len(plan.Input.Expected) != 2 {
		t.Fatal(plan.Input.Expected)
	}
	if _, e = validateMaintenance(ctx, s.Read, plan.Input); e != nil {
		t.Fatal("paused maintenance input lost", e)
	}
	stale, e := s.MaintenancePlan(ctx, "stale")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = validateMaintenance(ctx, s.Read, stale.Input); e != model.ErrConflict {
		t.Fatal("stale maintenance input revived", e)
	}

	if got := dumpRows(t, s, `SELECT id,preferred_edition_id,revision FROM works ORDER BY id`); !reflect.DeepEqual(got, [][]any{{"i:book", "book", int64(8)}, {"i:solo", "solo", int64(1)}}) {
		t.Fatal(got)
	}
	if got := dumpRows(t, s, `SELECT id,work_id,organization_revision FROM editions ORDER BY id`); !reflect.DeepEqual(got, [][]any{{"book", "i:book", int64(3)}, {"solo", "i:solo", int64(1)}, {"two", "i:book", int64(5)}}) {
		t.Fatal(got)
	}
	if got := dumpRows(t, s, `PRAGMA foreign_key_check`); len(got) != 0 {
		t.Fatal(got)
	}
	if got := dumpRows(t, s, `SELECT name FROM sqlite_master WHERE name IN('books','logical_items','book_items','version_groups','version_members','version_member_labels')`); len(got) != 0 {
		t.Fatal(got)
	}
	report := filepath.Join(dir, "upgrade-snapshots", "work-edition-v7.json")
	data, e := os.ReadFile(report)
	if e != nil {
		t.Fatal(e)
	}
	var r workMigrationReport
	if e = json.Unmarshal(data, &r); e != nil || r.State != "complete" || r.Groups["legacy-g"] != "i:book" || r.Editions != 3 || r.Works != 2 {
		t.Fatal(r, e)
	}
	st, _ := os.Stat(report)
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	for name, f := range six {
		var checksum string
		var version int
		fmt.Sscanf(name, "%03d_", &version)
		if e = s.Read.QueryRow("SELECT checksum FROM schema_migrations WHERE version=?", version).Scan(&checksum); e != nil || checksum != fmt.Sprintf("%x", sha256.Sum256(f.Data)) {
			t.Fatal(name, checksum, e)
		}
	}
	editions, total, e := s.WorkEditions(ctx, "i:book", 1, 1)
	if e != nil || total != 2 || len(editions) != 1 || editions[0].ID != "book" {
		t.Fatal(editions, total, e)
	}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if s.Version != 27 {
		t.Fatal(s.Version)
	}
}
func TestWorkMigrationFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	six := schemaSix(t)
	s, e := Open(ctx, dir, six)
	if e != nil {
		t.Fatal(e)
	}
	seedWorkUpgrade(t, s)
	before := dumpRows(t, s, "SELECT * FROM books ORDER BY id")
	s.Close()
	bad := schemaSix(t)
	sql, _ := migrations.Files.ReadFile("007_work_edition_file.sql")
	bad["007_work_edition_file.sql"] = &fstest.MapFile{Data: append(sql, []byte("\nINSERT INTO missing_table VALUES(1);")...)}
	if upgraded, e := Open(ctx, dir, bad); e == nil {
		upgraded.Close()
		t.Fatal("invalid migration accepted")
	}
	s, e = Open(ctx, dir, six)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM books ORDER BY id")) {
		t.Fatal("rollback changed files")
	}
	if got := dumpRows(t, s, "SELECT count(*) FROM version_members"); got[0][0] != int64(2) {
		t.Fatal(got)
	}
}
func TestWorkFileAndPreferredConstraints(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	seedBook(t, s)
	seedEdition(t, s, "second", "lib", "second.cbz", "Second")
	for _, query := range []string{
		`DELETE FROM files WHERE edition_id='book'`,
		`UPDATE works SET preferred_edition_id='second' WHERE id='i:book'`,
		`INSERT INTO editions(id,library_id,work_id,revision,organization_revision) VALUES('orphan','lib','i:book',1,1)`,
		`DELETE FROM editions WHERE id='book'`,
	} {
		tx, e := s.Write.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.ExecContext(ctx, query)
		if e == nil {
			e = tx.Commit()
		}
		tx.Rollback()
		if e == nil {
			t.Fatalf("constraint accepted %s", query)
		}
	}
	if e := checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
	cols := dumpRows(t, s, "PRAGMA table_info(editions)")
	for _, r := range cols {
		if r[1] == "relative_path" || r[1] == "page_count" || r[1] == "representative_sha1" {
			t.Fatal("physical field duplicated", r)
		}
	}
}

func TestEditionScanKeepsWorkAndEditorialIdentity(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	seedBook(t, s)
	if _, e := s.Write.Exec("UPDATE works SET name_override='Named work',revision=9 WHERE id='i:book';INSERT INTO edition_version_labels VALUES('book',0,'Manual label')"); e != nil {
		t.Fatal(e)
	}
	b, e := s.Edition(ctx, "book")
	if e != nil {
		t.Fatal(e)
	}
	b.Size = 2
	b.ContentRevision = 2
	b.FilenameTitle = "New filename"
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = saveEditionIndex(ctx, tx, *b); e != nil {
		t.Fatal(e)
	}
	if e = reproject(ctx, tx, b.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	got, e := s.Edition(ctx, "book")
	if e != nil || got.ID != b.ID || got.WorkID != b.WorkID || got.Revision != b.Revision+1 || got.ContentRevision != 2 {
		t.Fatal(got, e)
	}
	w, e := s.Work(ctx, b.WorkID)
	if e != nil || w.Name != "Named work" || w.Revision != 9 || w.EditionCount != 1 {
		t.Fatal(w, e)
	}
	if rows := dumpRows(t, s, "SELECT label FROM edition_version_labels WHERE edition_id='book'"); len(rows) != 1 || rows[0][0] != "Manual label" {
		t.Fatal(rows)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}

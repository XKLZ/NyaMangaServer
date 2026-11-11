package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func followAccounts(t *testing.T, s *Store) {
	t.Helper()
	if _, e := s.Write.Exec(`INSERT INTO accounts VALUES(1,'owner-a','anonymous',1,1)`); e != nil {
		t.Fatal(e)
	}
	// Production keeps its single-account model. Only this anonymous fixture
	// relaxes the singleton CHECK to exercise future identities and owner privacy.
	c, e := s.Write.Conn(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.ExecContext(context.Background(), `PRAGMA ignore_check_constraints=ON`); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ExecContext(context.Background(), `INSERT INTO accounts VALUES(2,'owner-b','anonymous',1,1)`); e != nil {
		t.Fatal(e)
	}
	if _, e = c.ExecContext(context.Background(), `PRAGMA ignore_check_constraints=OFF`); e != nil {
		t.Fatal(e)
	}
}
func followContext(account string) context.Context {
	return model.WithTagAccount(context.Background(), account)
}
func followTag(t *testing.T, s *Store, owner, id string, rev int64, on bool) model.TagFollow {
	t.Helper()
	v, e := s.SetTagFollow(context.Background(), owner, id, rev, on)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestTagFollowCASIsolationABAAndRollback(t *testing.T) {
	s, _ := openTest(t)
	followAccounts(t, s)
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "parody", "alpha", "character", "hero")
	id := tagID("character", "hero")
	a, b := followContext("owner-a"), followContext("owner-b")
	in := tagInput("a")
	before, _ := s.TagCatalog(a, in)
	other, _ := s.TagCatalog(b, in)
	if _, e := s.SetTagFollow(a, "owner-a", "missing", 0, true); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	for _, rev := range []int64{-1, math.MaxInt64} {
		if _, e := s.SetTagFollow(a, "owner-a", id, rev, true); !errors.Is(e, model.ErrInput) {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	result := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.SetTagFollow(a, "owner-a", id, 0, true); result <- e }()
	}
	wg.Wait()
	close(result)
	successes, conflicts := 0, 0
	for e := range result {
		if e == nil {
			successes++
		} else if errors.Is(e, model.ErrTagFollowConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal(successes, conflicts)
	}
	same := followTag(t, s, "owner-a", id, 1, true)
	if same.Revision != 1 {
		t.Fatal(same)
	}
	in.Snapshot = before.Snapshot
	if _, e := s.TagCatalog(a, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal(e)
	}
	in.Snapshot = other.Snapshot
	if _, e := s.TagCatalog(b, in); e != nil {
		t.Fatal("other owner invalidated", e)
	}
	if _, e := s.TagCatalog(a, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("cross-owner snapshot", e)
	}
	followTag(t, s, "owner-a", id, 1, false)
	followTag(t, s, "owner-a", id, 2, true)
	if _, e := s.SetTagFollow(a, "owner-a", id, 1, false); !errors.Is(e, model.ErrTagFollowConflict) {
		t.Fatal(e)
	}
	detail, e := s.TagCatalogDetail(a, id, tagInput("b"))
	if e != nil || !detail.Followed || detail.FollowRevision != 3 || detail.WorkCount != 0 {
		t.Fatal(detail, e)
	}
	detail, e = s.TagCatalogDetail(b, id, tagInput("a"))
	if e != nil || detail.Followed || detail.FollowRevision != 0 {
		t.Fatal(detail, e)
	}
	rel, e := s.TagCatalogRelations(a, tagID("parody", "alpha"), tagInput("a"))
	if e != nil || len(rel.Items) != 1 || !rel.Items[0].Character.Followed || rel.Items[0].Parody.Followed {
		t.Fatal(rel, e)
	}
	var oldrev int64
	s.Read.QueryRow("SELECT revision FROM tag_relation_projection").Scan(&oldrev)
	if _, e = s.Write.Exec(`CREATE TRIGGER fail_follow BEFORE INSERT ON events WHEN NEW.type='tag_follow' BEGIN SELECT RAISE(ABORT,'anonymous failure');END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetTagFollow(a, "owner-a", id, 3, false); e == nil {
		t.Fatal("failed event committed follow")
	}
	detail, _ = s.TagCatalogDetail(a, id, tagInput("a"))
	if !detail.Followed || detail.FollowRevision != 3 {
		t.Fatal(detail)
	}
	s.Write.Exec("DROP TRIGGER fail_follow")
	s.Write.Exec("UPDATE tag_follows SET revision=? WHERE account_id='owner-a' AND tag_id=?", int64(math.MaxInt64), id)
	if _, e = s.SetTagFollow(a, "owner-a", id, math.MaxInt64-1, false); !errors.Is(e, model.ErrTagFollowConflict) {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE tag_follows SET revision=3 WHERE account_id='owner-a' AND tag_id=?", id)
	s.Write.Exec("UPDATE tag_follow_epochs SET revision=? WHERE account_id='owner-a'", int64(math.MaxInt64))
	if _, e = s.SetTagFollow(a, "owner-a", id, 3, false); !errors.Is(e, model.ErrTagFollowConflict) {
		t.Fatal(e)
	}
	detail, _ = s.TagCatalogDetail(a, id, tagInput("a"))
	if !detail.Followed {
		t.Fatal("epoch overflow changed state")
	}
	var rev int64
	s.Read.QueryRow("SELECT revision FROM tag_relation_projection").Scan(&rev)
	if rev != oldrev {
		t.Fatal("follow changed relation revision")
	}
}
func TestTagFollowCatalogScopeFiltersAndSuggestions(t *testing.T) {
	s, _ := openTest(t)
	followAccounts(t, s)
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "artist", "common", "group", "team", "parody", "story", "character", "hero", "unknown", "odd")
	tagEdition(t, s, "two", "b", "artist", "rare")
	a := followContext("owner-a")
	id := tagID("artist", "rare")
	followTag(t, s, "owner-a", id, 0, true)
	in := tagInput("a")
	regular, e := s.TagCatalog(a, in)
	if e != nil || regular.Total != 5 {
		t.Fatal(regular, e)
	}
	in.FollowedOnly = true
	in.PageSize = 1
	page, e := s.TagCatalog(a, in)
	if e != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].WorkCount != 0 {
		t.Fatal(page, e)
	}
	in.Snapshot = regular.Snapshot
	if _, e = s.TagCatalog(a, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("filter snapshot reused", e)
	}
	in.Snapshot = ""
	for _, pair := range [][2]string{{"artist", "common"}, {"group", "team"}, {"parody", "story"}, {"character", "hero"}, {"unknown", "odd"}} {
		followTag(t, s, "owner-a", tagID(pair[0], pair[1]), 0, true)
	}
	in.Category = "content"
	page, e = s.TagCatalog(a, in)
	if e != nil || page.Total != 1 || page.Items[0].Namespace != "unknown" {
		t.Fatal(page, e)
	}
	in.Category = ""
	in.Namespace = "artist"
	in.Query = "rare"
	page, e = s.TagCatalog(a, in)
	if e != nil || page.Total != 1 || page.Items[0].ID != id {
		t.Fatal(page, e)
	}
	in.Namespace = ""
	in.Query = ""
	page, e = s.TagCatalog(a, in)
	if e != nil || page.Total != 6 {
		t.Fatal(page, e)
	}
	in.Page = 2
	in.Snapshot = page.Snapshot
	second, e := s.TagCatalog(a, in)
	if e != nil || len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID {
		t.Fatal(second, e)
	}
	// Hide/mark offline retains physical tag usage, disabled library remains explicit.
	s.Write.Exec("UPDATE libraries SET disabled=1 WHERE id='a'")
	s.Write.Exec("UPDATE files SET file_state='missing' WHERE edition_id='one'")
	in = tagInput("a")
	in.FollowedOnly = true
	if p, e := s.TagCatalog(a, in); e != nil || p.Total != 6 {
		t.Fatal(p, e)
	}
	s.Write.Exec("UPDATE libraries SET trashed_at=unixepoch() WHERE id='a'")
	if _, e := s.TagCatalog(a, in); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	in.LibraryID = "missing"
	if _, e := s.TagCatalog(a, in); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE libraries SET trashed_at=NULL,disabled=0 WHERE id='a'")
	// Remove all source usages: the global personal tag must survive.
	s.Write.Exec("UPDATE editions SET trashed_at=unixepoch()")
	in = tagInput("a")
	in.FollowedOnly = true
	page, e = s.TagCatalog(a, in)
	if e != nil || page.Total != 6 {
		t.Fatal(page, e)
	}
	for _, v := range page.Items {
		if v.WorkCount != 0 {
			t.Fatal(v)
		}
	}
	// Dictionary off keeps manual names and follow priority, without altering usage.
	s.Write.Exec("UPDATE editions SET trashed_at=NULL")
	s.Write.Exec("UPDATE enhancement_settings SET enabled=0 WHERE feature='chinese_dictionary'")
	manual := "人工稀有"
	if e := s.TranslateTag(context.Background(), id, "zh-CN", 0, &manual); e != nil {
		t.Fatal(e)
	}
	// Only rare followed for the priority comparison.
	for _, v := range page.Items {
		if v.ID != id {
			followTag(t, s, "owner-a", v.ID, 1, false)
		}
	}
	suggest := model.TagSuggestionInput{Page: 1, PageSize: 1}
	q, e := s.TagSuggestions(a, suggest)
	if e != nil || len(q.Items) != 1 || q.Items[0].TagID == nil || *q.Items[0].TagID != id || !q.Items[0].Followed || q.Items[0].Usage != 1 || q.Items[0].DictionaryMatch != nil || q.DictionaryState != "disabled" {
		t.Fatal(q, e)
	}
	other, e := s.TagSuggestions(followContext("owner-b"), suggest)
	if e != nil {
		t.Fatal(e)
	}
	suggest.Snapshot = q.Snapshot
	suggest.Page = 2
	next, e := s.TagSuggestions(a, suggest)
	if e != nil || len(next.Items) != 1 || next.Items[0].Followed {
		t.Fatal(next, e)
	}
	suggest = model.TagSuggestionInput{Page: 1, PageSize: 50, Exclude: []model.TagName{{Namespace: "artist", Name: "rare"}}}
	q, e = s.TagSuggestions(a, suggest)
	if e != nil || q.Total != 5 {
		t.Fatal(q, e)
	}
	suggest = model.TagSuggestionInput{Page: 1, PageSize: 50, Namespace: "artist", Query: "人工稀有"}
	q, e = s.TagSuggestions(a, suggest)
	if e != nil || q.Total != 1 || q.Items[0].Origin != "manual" {
		t.Fatal(q, e)
	}
	followTag(t, s, "owner-a", id, 1, false)
	suggest = model.TagSuggestionInput{Page: 1, PageSize: 1, Snapshot: other.Snapshot}
	if _, e = s.TagSuggestions(followContext("owner-b"), suggest); e != nil {
		t.Fatal("other suggestion invalidated", e)
	}
}

type followCountQuery struct {
	queryer
	calls int
}

func (q *followCountQuery) QueryContext(ctx context.Context, sql string, args ...any) (*sql.Rows, error) {
	q.calls++
	return q.queryer.QueryContext(ctx, sql, args...)
}
func TestTagFollowBatchHydrationAndPrivateEvents(t *testing.T) {
	s, _ := openTest(t)
	followAccounts(t, s)
	tagLibraries(t, s)
	tagEdition(t, s, "one", "a", "artist", "one")
	id := tagID("artist", "one")
	followTag(t, s, "owner-a", id, 0, true)
	q := &followCountQuery{queryer: s.Read}
	tags := []*model.CatalogTag{}
	for i := 0; i < 200; i++ {
		tags = append(tags, &model.CatalogTag{ID: id})
	}
	if e := enrichTagFollows(followContext("owner-a"), q, tags...); e != nil || q.calls != 1 {
		t.Fatal(q.calls, e)
	}
	for _, v := range tags {
		if !v.Followed {
			t.Fatal(v)
		}
	}
	var base int64
	s.Read.QueryRow("SELECT max(id) FROM events").Scan(&base)
	tx, _ := s.Write.Begin()
	for i := 0; i < 450; i++ {
		if _, e := tx.Exec("INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) VALUES('tag_follow','private-b',?,unixepoch(),'owner-b')", i+1); e != nil {
			t.Fatal(e)
		}
	}
	tx.Exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('catalog','global-after',1,unixepoch())")
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	events, high, gap, e := s.EventsForAccount(context.Background(), "owner-a", base, 200)
	if e != nil || gap || len(events) != 1 || events[0].ResourceID != "global-after" || events[0].ID != high {
		t.Fatal(events, high, gap, e)
	}
	events, _, _, e = s.EventsForAccount(context.Background(), "owner-b", base, 200)
	if e != nil || len(events) != 200 {
		t.Fatal(len(events), e)
	}
	more, _, gap, e := s.EventsForAccount(context.Background(), "owner-b", events[199].ID, 200)
	if e != nil || gap || len(more) != 200 {
		t.Fatal(len(more), gap, e)
	}
	more, _, gap, e = s.EventsForAccount(context.Background(), "owner-b", more[199].ID, 200)
	if e != nil || gap || len(more) != 51 || more[50].ResourceID != "global-after" {
		t.Fatal(len(more), gap, e)
	}
	all, _, _, e := s.Events(context.Background(), 0, 10000)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range all {
		if v.Type == "tag_follow" {
			t.Fatal("global reader leaked private event")
		}
	}
}
func TestTagFollowMigrationPreservesNineteen(t *testing.T) {
	base := fstest.MapFS{}
	entries, _ := migrations.Files.ReadDir(".")
	for _, n := range entries {
		if strings.HasSuffix(n.Name(), ".sql") && n.Name() < "020" {
			b, _ := migrations.Files.ReadFile(n.Name())
			base[n.Name()] = &fstest.MapFile{Data: b}
		}
	}
	ctx := context.Background()
	dir := t.TempDir()
	s, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if e = s.Initialize(ctx, Account{ID: "anon", PasswordHash: "anonymous", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "anon", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateMetadataJob(ctx, "preserved-job", "", []string{"book"}, nil); e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("UPDATE jobs SET state='paused' WHERE id='preserved-job'")
	for _, statement := range []string{
		`UPDATE enhancement_settings SET enabled=1,runtime_armed=0 WHERE feature='work_recommendations'`,
		`INSERT INTO work_proposals(id,job_id,library_id,proposal_key,kind,target_work_id,tier,primary_plan,revision,state,plan_json,created_at) VALUES('retained-proposal','preserved-job','lib','anonymous-key','create_work','','strong',1,7,'dismissed','{}',1)`,
		`INSERT INTO work_proposal_decisions(proposal_key,proposal_id,account_id,decision,before_json,after_json,accepted_members_json,result_json,revision,updated_at) VALUES('anonymous-key','retained-proposal','anon','dismissed','{}','{}','[]','{}',7,1)`,
	} {
		if _, e = s.Write.Exec(statement); e != nil {
			t.Fatal(e)
		}
	}

	setTagNames(t, s, "book", "parody", "anonymous")
	if _, e = s.SetTagImage(ctx, tagID("parody", "anonymous"), "foreground", 0, model.TagImage{Width: 2, Height: 3, Format: "png", Asset: &model.Asset{ID: "asset", Key: "assets/tags/ab/anonymous.png", Digest: "anonymous", Size: 5}}); e != nil {
		t.Fatal(e)
	}
	asset := filepath.Join(dir, "assets", "tags", "ab", "anonymous.png")
	os.MkdirAll(filepath.Dir(asset), 0700)
	os.WriteFile(asset, []byte("anon!"), 0600)
	rows, _ := s.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations' ORDER BY name")
	names := []string{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	before := map[string][][]any{}
	for _, n := range names {
		before[n] = dumpRows(t, s, preservedTableQuery(n))
	}
	ledger := dumpRows(t, s, "SELECT * FROM schema_migrations ORDER BY version")
	s.Close()
	bad := fstest.MapFS{}
	for k, v := range base {
		bad[k] = v
	}
	raw, _ := migrations.Files.ReadFile("020_tag_follows.sql")
	bad["020_tag_follows.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("INSERT INTO nonexistent VALUES(1);")...)}
	if failed, e := Open(ctx, dir, bad); e == nil {
		failed.Close()
		t.Fatal("bad migration succeeded")
	}
	old, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	if old.Version != 19 {
		t.Fatal(old.Version)
	}
	old.Close()
	for attempt := 0; attempt < 2; attempt++ {
		up, e := Open(ctx, dir, migrationsThrough(t, 24))
		if e != nil {
			t.Fatal(e)
		}
		if up.Version != 24 {
			t.Fatal(up.Version)
		}
		for _, n := range names {
			if !reflect.DeepEqual(before[n], dumpRows(t, up, preservedTableQuery(n))) {
				t.Fatal("old table changed", n)
			}
		}
		if !reflect.DeepEqual(ledger, dumpRows(t, up, "SELECT * FROM schema_migrations WHERE version<=19 ORDER BY version")) {
			t.Fatal("ledger changed")
		}
		var n int
		if e = up.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n); e != nil || n != 0 {
			t.Fatal(n, e)
		}
		if e = up.Read.QueryRow("SELECT count(*) FROM events WHERE owner_account_id IS NOT NULL").Scan(&n); e != nil || n != 0 {
			t.Fatal(n, e)
		}
		b, e := os.ReadFile(asset)
		if e != nil || string(b) != "anon!" {
			t.Fatal("asset changed", e)
		}
		up.Close()
	}
}

func TestTagFollowDictionaryOnlyAndSnapshotABA(t *testing.T) {
	s, _ := openTest(t)
	followAccounts(t, s)
	seedBook(t, s)
	setTagNames(t, s, "book", "artist", "used")
	ctx := followContext("owner-a")
	id := tagID("artist", "used")
	j, item := dictionaryJob(t, s, "dict-follow")
	v := model.TranslationVersion{ID: "follow-dict", Digest: "anonymous", Source: "anonymous", CacheKey: "anonymous"}
	if e := s.PublishDictionary(ctx, j, item, v, map[model.TagName]string{{Namespace: "artist", Name: "used"}: "常用", {Namespace: "artist", Name: "only"}: "仅词库"}); e != nil {
		t.Fatal(e)
	}
	if e := s.FinishBackground(ctx, j, ""); e != nil {
		t.Fatal(e)
	}
	before, e := s.MetadataDetails(ctx, "book")
	if e != nil {
		t.Fatal(e)
	}
	oldTags, e := s.Tags(ctx, "", "", "zh-CN")
	if e != nil {
		t.Fatal(e)
	}
	var catalog string
	catalog, _ = catalogRevision(ctx, s.Read)
	in := model.TagSuggestionInput{Page: 1, PageSize: 1}
	first, e := s.TagSuggestions(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	only, e := s.TagSuggestions(ctx, model.TagSuggestionInput{Query: "仅词库", Page: 1, PageSize: 50})
	if e != nil || only.Total != 1 || only.Items[0].TagID != nil || only.Items[0].Followed || only.Items[0].FollowRevision != 0 {
		t.Fatal(only, e)
	}
	if _, e = s.SetTagFollow(ctx, "owner-a", tagID("artist", "only"), 0, true); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("created dict-only tag", e)
	}
	followTag(t, s, "owner-a", id, 0, true)
	followTag(t, s, "owner-a", id, 1, false)
	in.Page = 2
	in.Snapshot = first.Snapshot
	if _, e = s.TagSuggestions(ctx, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("ABA snapshot survived", e)
	}
	after, e := s.MetadataDetails(ctx, "book")
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("follow polluted metadata/source", e)
	}
	afterTags, e := s.Tags(ctx, "", "", "zh-CN")
	if e != nil || !reflect.DeepEqual(oldTags, afterTags) {
		t.Fatal("old tags changed", e)
	}
	current, _ := catalogRevision(ctx, s.Read)
	if current != catalog {
		t.Fatal("follow advanced catalog epoch")
	}
}

func TestTagFollowPrivateEventRetention(t *testing.T) {
	s, _ := openTest(t)
	followAccounts(t, s)
	var cursor int64
	if _, e := s.Write.Exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('catalog','before-retention',1,unixepoch())"); e != nil {
		t.Fatal(e)
	}
	s.Read.QueryRow("SELECT max(id) FROM events").Scan(&cursor)
	if _, e := s.Write.Exec(`WITH RECURSIVE n(v) AS(SELECT 1 UNION ALL SELECT v+1 FROM n WHERE v<10001) INSERT INTO events(type,resource_id,revision,created_at,owner_account_id) SELECT 'tag_follow','private-retained',v,unixepoch(),'owner-b' FROM n`); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := s.Read.QueryRow("SELECT count(*) FROM events").Scan(&n); e != nil || n != 10000 {
		t.Fatal(n, e)
	}
	events, high, gap, e := s.EventsForAccount(context.Background(), "owner-a", cursor, 200)
	if e != nil || !gap || len(events) != 0 || high <= cursor {
		t.Fatal(events, high, gap, e)
	}
	events, _, gap, e = s.EventsForAccount(context.Background(), "owner-a", high, 200)
	if e != nil || gap || len(events) != 0 {
		t.Fatal(events, gap, e)
	}
	if _, e = s.Write.Exec("UPDATE events SET created_at=unixepoch()-86401"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("INSERT INTO events(type,resource_id,revision,created_at) VALUES('catalog','after-retention',1,unixepoch())"); e != nil {
		t.Fatal(e)
	}
	if e = s.Read.QueryRow("SELECT count(*) FROM events").Scan(&n); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	events, _, gap, e = s.EventsForAccount(context.Background(), "owner-a", 0, 200)
	if e != nil || gap || len(events) != 1 || events[0].ResourceID != "after-retention" {
		t.Fatal(events, gap, e)
	}
}

package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"nyamangaserver.local/server/internal/galleryhighlight"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

func galleryActors(t *testing.T, s *Store, now int64) (model.Session, model.Session) {
	t.Helper()
	followAccounts(t, s)
	actors := []model.Session{}
	for _, owner := range []string{"owner-a", "owner-b"} {
		hash := sha256.Sum256([]byte("anonymous-gallery-session-" + owner))
		a := model.Session{Hash: hash[:], AccountID: owner, Created: now, Expires: now + 86400}
		if _, e := s.Write.Exec("INSERT INTO sessions VALUES(?,?,?,?)", a.Hash, a.AccountID, a.Created, a.Expires); e != nil {
			t.Fatal(e)
		}
		actors = append(actors, a)
	}
	return actors[0], actors[1]
}
func galleryToggle(t *testing.T, s *Store, on bool) {
	t.Helper()
	v, e := s.Enhancements(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	transition, e := s.ChangeEnhancement(context.Background(), model.EnhancementPatch{Expected: v.Revision, Feature: model.EnhancementGallery, Enabled: &on})
	if e != nil || transition != nil {
		t.Fatal("gallery toggle did not settle immediately", e)
	}
}
func galleryMint(t *testing.T, svc *galleryhighlight.Service, actor model.Session) model.GalleryConnectionSecret {
	t.Helper()
	v, e := svc.Create(context.Background(), actor, model.GalleryConnectionCreate{})
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func galleryRead(t *testing.T, svc *galleryhighlight.Service, raw string, ids ...string) model.GalleryLookup {
	t.Helper()
	var out model.GalleryLookup
	e := svc.Lookup(context.Background(), raw, model.GalleryLookupInput{Provider: "ehentai", GalleryIDs: ids}, func(v model.GalleryLookup) error { out = v; return nil })
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestGalleryConnectionsScopeExpiryRevisionAndSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	now := time.Now().Unix()
	a, b := galleryActors(t, s, now)
	svc := galleryhighlight.New(s)
	svc.Now = func() time.Time { return time.Unix(now, 0) }
	state, e := s.Enhancements(ctx)
	if e != nil || state.Features[model.EnhancementGallery].Enabled {
		t.Fatal("gallery default must be off", e)
	}
	if _, e = svc.Create(ctx, a, model.GalleryConnectionCreate{}); !errors.Is(e, model.ErrGalleryDisabled) {
		t.Fatal(e)
	}
	// An unfinished old switch must survive immediate gallery on/off unchanged.
	off := false
	pending, e := s.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: state.Revision, Feature: model.EnhancementWork, Enabled: &off})
	if e != nil || pending == nil {
		t.Fatal(e)
	}
	before := dumpRows(t, s, "SELECT * FROM enhancement_transitions ORDER BY id")
	galleryToggle(t, s, true)
	galleryToggle(t, s, false)
	galleryToggle(t, s, true)
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM enhancement_transitions ORDER BY id")) {
		t.Fatal("gallery changed old transitions")
	}
	secret := galleryMint(t, svc, a)
	if secret.Connection.ExpiresAt != nil || secret.Connection.Revision != 1 {
		t.Fatal(secret.Connection)
	}
	page, e := svc.List(ctx, a, 1, 1)
	if e != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), secret.Token) || strings.Contains(string(raw), "token_hash") || strings.Contains(string(raw), "auth_revision") {
		t.Fatal("secret field leaked")
	}
	var digest []byte
	if e = s.Read.QueryRow("SELECT token_hash FROM gallery_highlight_connections WHERE id=?", secret.Connection.ID).Scan(&digest); e != nil || len(digest) != 32 || strings.Contains(string(digest), secret.Token) {
		t.Fatal("invalid secret storage", e)
	}
	other, e := svc.List(ctx, b, 1, 50)
	if e != nil || other.Total != 0 {
		t.Fatal(other, e)
	}
	if _, e = svc.Revoke(ctx, b, secret.Connection.ID, 1); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	// Mint two independent long-lived connections; old finite rows are covered separately.
	galleryMint(t, svc, a)
	galleryMint(t, svc, a)
	if _, e = svc.Revoke(ctx, a, secret.Connection.ID, 0); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	revoked, e := svc.Revoke(ctx, a, secret.Connection.ID, 1)
	if e != nil || revoked.Revision != 2 || revoked.State != "revoked" {
		t.Fatal(revoked, e)
	}
	if _, e = svc.Revoke(ctx, a, secret.Connection.ID, 1); !errors.Is(e, model.ErrGalleryConflict) {
		t.Fatal(e)
	}
	if _, e = svc.Revoke(ctx, a, secret.Connection.ID, 2); e != nil {
		t.Fatal(e)
	}
	if e = svc.Status(ctx, secret.Token, func(model.GalleryStatus) error { return errors.New("unexpected publish") }); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal(e)
	}
	valid := galleryMint(t, svc, a)
	galleryToggle(t, s, false)
	if e = svc.Status(ctx, valid.Token, func(model.GalleryStatus) error { return nil }); !errors.Is(e, model.ErrGalleryDisabled) {
		t.Fatal(e)
	}
	if p, e := svc.List(ctx, a, 1, 50); e != nil || p.Total != 4 {
		t.Fatal(p, e)
	}
	galleryToggle(t, s, true)
	if e = svc.Status(ctx, secret.Token, func(model.GalleryStatus) error { return nil }); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal("revoked revived", e)
	}
	if _, e = s.Write.Exec("UPDATE gallery_highlight_connections SET created_at=?,expires_at=? WHERE id=?", now-86400, now, valid.Connection.ID); e != nil {
		t.Fatal(e)
	}
	if e = svc.Status(ctx, valid.Token, func(model.GalleryStatus) error { return nil }); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal(e)
	}
	valid = galleryMint(t, svc, a)
	independent := galleryMint(t, svc, b)
	if e = s.ChangePassword(ctx, Account{ID: a.AccountID, Revision: 1}, "anonymous-new-password", now+1); e != nil {
		t.Fatal(e)
	}
	if e = svc.Status(ctx, valid.Token, func(model.GalleryStatus) error { return nil }); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal("password failed to invalidate", e)
	}
	if e = svc.Status(ctx, independent.Token, func(model.GalleryStatus) error { return nil }); e != nil {
		t.Fatal("password changed another account token", e)
	}
	if _, e = svc.Create(ctx, a, model.GalleryConnectionCreate{}); !errors.Is(e, model.ErrGalleryAuth) {
		t.Fatal("old session minted after password", e)
	}
}
func gallerySource(t *testing.T, s *Store, edition, gid string, matched int64) {
	t.Helper()
	mid := "metadata-" + gid
	if _, e := s.Write.Exec(`INSERT INTO external_metadata VALUES(?,'ehentai',?,'{}','{}',1,1,1) ON CONFLICT(provider,external_id) DO NOTHING`, mid, gid); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec(`INSERT INTO edition_sources VALUES(?,?,?,'sha1',?)`, edition, mid, "https://e-hentai.org/g/"+gid+"/0123456789/", matched); e != nil {
		t.Fatal(e)
	}
}
func TestGalleryLookupOfficialScopeAggregationAndQueryVersion(t *testing.T) {
	s, _ := openTest(t)
	now := time.Now().Unix()
	a, _ := galleryActors(t, s, now)
	tagLibraries(t, s)
	galleryToggle(t, s, true)
	svc := galleryhighlight.New(s)
	secret := galleryMint(t, svc, a)
	for i, tc := range []struct {
		gid, state string
		rev        int64
	}{{"1", "available", 1}, {"2", "missing", 1}, {"3", "corrupt", 1}, {"4", "needs_review", 1}, {"5", "available", 0}, {"6", "missing", 1}, {"6", "available", 1}, {"7", "available", 1}, {"8", "available", 1}, {"9", "available", 1}} {
		id := fmt.Sprintf("e%d", i)
		lib := "a"
		if i >= 8 {
			lib = "b"
		}
		seedEdition(t, s, id, lib, id+".cbz", "anonymous")
		gallerySQL(t, s, "UPDATE files SET file_state=? WHERE edition_id=?", tc.state, id)
		gallerySource(t, s, id, tc.gid, tc.rev)
	}
	// Hidden belongs in the aggregate. One healthy row in a different Work wins.
	gallerySQL(t, s, "INSERT INTO edition_user_states VALUES(?,?,0,1,1)", a.AccountID, "e0")
	gallerySQL(t, s, "UPDATE editions SET trashed_at=unixepoch() WHERE id='e7'")
	gallerySQL(t, s, "UPDATE libraries SET disabled=1 WHERE id='b'")
	// Formal metadata without an adopted association, plus manual URL, never counts.
	seedEdition(t, s, "manual", "a", "manual.cbz", "anonymous")
	gallerySQL(t, s, "INSERT INTO edition_metadata_state VALUES('manual','https://exhentai.org/g/10/0123456789/',1)")
	gallerySQL(t, s, "INSERT INTO external_metadata VALUES('only-candidate','ehentai','10','{}','{}',1,1,1)")
	if _, e := s.CreateMetadataJob(context.Background(), "candidate-job", "", []string{"manual"}, nil); e != nil {
		t.Fatal(e)
	}
	candidate, _ := json.Marshal(model.RemoteMetadata{ExternalID: "10", URL: "https://exhentai.org/g/10/0123456789/"})
	gallerySQL(t, s, "INSERT INTO source_previews VALUES('only-preview','manual','candidate-job',?,1,1,1,9999999999,'pending')", string(candidate))
	gallerySQL(t, s, "INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES('only-review','a','anonymous-request','metadata','review')")
	gallerySQL(t, s, "INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('only-review','manual','new',1)")
	gallerySQL(t, s, "INSERT INTO import_candidates(batch_id,edition_id,request_generation,content_revision,selection_revision,input_json,result_json) VALUES('only-review','manual',1,1,1,'{}',?)", string(candidate))
	gallerySQL(t, s, "INSERT INTO source_searches(id,batch_id,edition_id,mode,query,site,request_key,input_json,content_revision,selection_revision,edition_revision,member_revision) VALUES('only-search','only-review','manual','title','anonymous','ex','anonymous-search','{}',1,1,1,1)")
	gallerySQL(t, s, "INSERT INTO source_search_results(id,search_id,url,gallery_id,title,position,state,result_json) VALUES('only-result','only-search','https://exhentai.org/g/10/0123456789/','10','anonymous',1,'ready',?)", string(candidate))
	gallerySQL(t, s, "UPDATE edition_sources SET url='https://exhentai.org/g/1/0123456789/' WHERE edition_id='e0'")

	before := dumpRows(t, s, "SELECT * FROM catalog_state")
	out := galleryRead(t, svc, secret.Token, "001", "2", "3", "4", "5", "6", "7", "8", "9", "10", "001")
	want := []string{"collected", "needs_review", "needs_review", "needs_review", "needs_review", "collected", "none", "none", "none", "none"}
	if len(out.Items) != len(want) {
		t.Fatal(out)
	}
	for i, w := range want {
		if out.Items[i].State != w {
			t.Fatal(out)
		}
	}
	if !reflect.DeepEqual(before, dumpRows(t, s, "SELECT * FROM catalog_state")) {
		t.Fatal("read changed catalog")
	}
	// Same projected title/tags while official gid changes: request result/version changes.
	old := galleryRead(t, svc, secret.Token, "1", "11")
	if _, e := s.Write.Exec("UPDATE files SET filename_title='another anonymous filename' WHERE edition_id='manual'"); e != nil {
		t.Fatal(e)
	}
	unrelated := galleryRead(t, svc, secret.Token, "1", "11")
	if old.QueryVersion != unrelated.QueryVersion {
		t.Fatal("unrelated catalog revision changed result fingerprint")
	}
	gallerySQL(t, s, "INSERT INTO external_metadata VALUES('replacement','ehentai','11','{}','{}',1,1,1)")
	gallerySQL(t, s, "UPDATE edition_sources SET metadata_id='replacement' WHERE edition_id='e0'")
	current := galleryRead(t, svc, secret.Token, "1", "11")
	if old.QueryVersion == current.QueryVersion || current.Items[0].State != "none" || current.Items[1].State != "collected" {
		t.Fatal("version did not follow results")
	}
	repeated := galleryRead(t, svc, secret.Token, "1", "11")
	if current.QueryVersion != repeated.QueryVersion {
		t.Fatal("unstable result version")
	}
	if _, e := s.Write.Exec("UPDATE libraries SET disabled=0,trashed_at=unixepoch() WHERE id='b'"); e != nil {
		t.Fatal(e)
	}
	out = galleryRead(t, svc, secret.Token, "8", "9")
	for _, v := range out.Items {
		if v.State != "none" {
			t.Fatal(out)
		}
	}
	// The indexed path starts at provider+requested IDs, never materializes all gids.
	rows, e := s.Read.Query(`EXPLAIN QUERY PLAN SELECT m.external_id,max(f.file_state='available' AND s.matched_content_revision=f.content_revision)`+galleryLookupFrom+`?,?) GROUP BY m.external_id`, "1", "2")
	if e != nil {
		t.Fatal(e)
	}
	plan := ""
	for rows.Next() {
		var a, b, c int
		var detail string
		rows.Scan(&a, &b, &c, &detail)
		plan += detail + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "SEARCH m") || !strings.Contains(plan, "external_id=?") || !strings.Contains(plan, "gallery_sources_metadata (metadata_id=?)") {
		t.Fatal("unexpected lookup plan", plan)
	}
}

type galleryBlockedRepo struct {
	galleryhighlight.Repository
	entered, proceed chan struct{}
}

func (r *galleryBlockedRepo) GalleryLookup(ctx context.Context, g model.GalleryGrant, ids []string, now int64) ([]model.GalleryMatch, error) {
	items, e := r.Repository.GalleryLookup(ctx, g, ids, now)
	if e != nil {
		return nil, e
	}
	close(r.entered)
	select {
	case <-r.proceed:
		return items, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestGalleryConcurrentAuthorityBeforePublication(t *testing.T) {
	for _, action := range []string{"disable", "revoke", "password", "expiry"} {
		t.Run(action, func(t *testing.T) {
			s, _ := openTest(t)
			now := time.Now().Unix()
			a, _ := galleryActors(t, s, now)
			galleryToggle(t, s, true)
			base := galleryhighlight.New(s)
			secret := galleryMint(t, base, a)
			repo := &galleryBlockedRepo{Repository: s, entered: make(chan struct{}), proceed: make(chan struct{})}
			svc := galleryhighlight.New(repo)
			var clock atomic.Int64
			clock.Store(now)
			svc.Now = func() time.Time { return time.Unix(clock.Load(), 0) }
			done := make(chan error, 1)
			var published atomic.Bool
			go func() {
				done <- svc.Lookup(context.Background(), secret.Token, model.GalleryLookupInput{Provider: "ehentai", GalleryIDs: []string{"1"}}, func(model.GalleryLookup) error { published.Store(true); return nil })
			}()
			<-repo.entered
			switch action {
			case "disable":
				galleryToggle(t, s, false)
			case "revoke":
				if _, e := base.Revoke(context.Background(), a, secret.Connection.ID, 1); e != nil {
					t.Fatal(e)
				}
			case "expiry":
				gallerySQL(t, s, "UPDATE gallery_highlight_connections SET created_at=?,expires_at=? WHERE id=?", now-1, now+1, secret.Connection.ID)
				clock.Store(now + 2)
			case "password":
				if e := s.ChangePassword(context.Background(), Account{ID: a.AccountID, Revision: 1}, "new-anonymous", now); e != nil {
					t.Fatal(e)
				}
			}
			close(repo.proceed)
			e := <-done
			if e == nil || published.Load() {
				t.Fatal("in-flight unauthorized result published")
			}
			if e = svc.Status(context.Background(), secret.Token, func(model.GalleryStatus) error { return nil }); e == nil {
				t.Fatal("new request accepted after authority commit")
			}
		})
	}
}
func TestGalleryMigrationPreservesTwentyAndTransitions(t *testing.T) {
	ctx := context.Background()
	base := fstest.MapFS{}
	entries, _ := migrations.Files.ReadDir(".")
	for _, n := range entries {
		if strings.HasSuffix(n.Name(), ".sql") && n.Name() < "021" {
			b, _ := migrations.Files.ReadFile(n.Name())
			base[n.Name()] = &fstest.MapFile{Data: b}
		}
	}
	dir := t.TempDir()
	old, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, old)
	if e = old.Initialize(ctx, Account{ID: "anon", PasswordHash: "anonymous", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "anon", Created: 1, Expires: math.MaxInt64}); e != nil {
		t.Fatal(e)
	}
	if _, e = old.Write.Exec("UPDATE enhancement_settings SET enabled=1"); e != nil {
		t.Fatal(e)
	}
	off := false
	v, _ := old.Enhancements(ctx)
	if _, e = old.ChangeEnhancement(ctx, model.EnhancementPatch{Expected: v.Revision, Feature: model.EnhancementDictionary, Enabled: &off}); e != nil {
		t.Fatal(e)
	}

	if _, e = old.CreateMetadataJob(ctx, "retained-job", "", []string{"book"}, nil); e != nil {
		t.Fatal(e)
	}
	for _, statement := range []string{
		`UPDATE jobs SET state='paused',generation=7 WHERE id='retained-job'`,
		`INSERT INTO work_proposals(id,job_id,library_id,proposal_key,kind,target_work_id,tier,primary_plan,revision,state,plan_json,created_at) VALUES('retained-proposal','retained-job','lib','anonymous-key','create_work','','strong',1,7,'dismissed','{}',1)`,
		`INSERT INTO work_proposal_decisions(proposal_key,proposal_id,account_id,decision,before_json,after_json,accepted_members_json,result_json,revision,updated_at) VALUES('anonymous-key','retained-proposal','anon','dismissed','{}','{}','[]','{}',7,1)`,
	} {
		if _, e = old.Write.Exec(statement); e != nil {
			t.Fatal(e)
		}
	}
	gallerySource(t, old, "book", "123", 1)
	setTagNames(t, old, "book", "parody", "anonymous")
	if _, e = old.SetTagFollow(ctx, "anon", tagID("parody", "anonymous"), 0, true); e != nil {
		t.Fatal(e)
	}
	if _, e = old.SetTagImage(ctx, tagID("parody", "anonymous"), "foreground", 0, model.TagImage{Width: 2, Height: 3, Format: "png", Asset: &model.Asset{ID: "retained", Key: "assets/tags/aa/anonymous.png", Digest: "anonymous", Size: 4}}); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "assets", "tags", "aa", "anonymous.png")
	os.MkdirAll(filepath.Dir(file), 0700)
	os.WriteFile(file, []byte("anon"), 0600)
	rows, _ := old.Read.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='schema_migrations'")
	names := []string{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	rows.Close()
	before := map[string][][]any{}
	for _, n := range names {
		before[n] = dumpRows(t, old, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", n))
	}
	ledger := dumpRows(t, old, "SELECT * FROM schema_migrations ORDER BY version")
	old.Close()
	bad := fstest.MapFS{}
	for k, v := range base {
		bad[k] = v
	}
	raw, _ := migrations.Files.ReadFile("021_gallery_highlight.sql")
	bad["021_gallery_highlight.sql"] = &fstest.MapFile{Data: append(append([]byte{}, raw...), []byte("INSERT INTO absent VALUES(1);")...)}
	if db, e := Open(ctx, dir, bad); e == nil {
		db.Close()
		t.Fatal("bad migration committed")
	}
	prior, e := Open(ctx, dir, base)
	if e != nil || prior.Version != 20 {
		t.Fatal(e)
	}
	prior.Close()
	for i := 0; i < 2; i++ {
		up, e := Open(ctx, dir, migrationsThrough(t, 24))
		if e != nil {
			t.Fatal(e)
		}
		if up.Version != 24 {
			t.Fatal(up.Version)
		}
		for _, n := range names {
			if !reflect.DeepEqual(before[n], dumpRows(t, up, fmt.Sprintf("SELECT * FROM %q ORDER BY rowid", n))) {
				t.Fatal("old table changed", n)
			}
		}
		if !reflect.DeepEqual(ledger, dumpRows(t, up, "SELECT * FROM schema_migrations WHERE version<=20 ORDER BY version")) {
			t.Fatal("ledger changed")
		}
		state, e := up.Enhancements(ctx)
		if e != nil || state.Features[model.EnhancementGallery].Enabled {
			t.Fatal(e)
		}
		var n int
		if e = up.Read.QueryRow("SELECT count(*) FROM pragma_foreign_key_check").Scan(&n); e != nil || n != 0 {
			t.Fatal(n, e)
		}
		b, e := os.ReadFile(file)
		if e != nil || string(b) != "anon" {
			t.Fatal("asset changed", e)
		}
		up.Close()
	}
}

func gallerySQL(t *testing.T, s *Store, statement string, args ...any) {
	t.Helper()
	if _, e := s.Write.Exec(statement, args...); e != nil {
		t.Fatal(e)
	}
}

func TestGalleryConnectionCapacityIsAccountScoped(t *testing.T) {
	s, _ := openTest(t)
	a, b := galleryActors(t, s, time.Now().Unix())
	galleryToggle(t, s, true)
	svc := galleryhighlight.New(s)
	for i := 0; i < 20; i++ {
		galleryMint(t, svc, a)
	}
	if _, e := svc.Create(context.Background(), a, model.GalleryConnectionCreate{}); !errors.Is(e, model.ErrGalleryCapacity) {
		t.Fatal(e)
	}
	galleryMint(t, svc, b)
}

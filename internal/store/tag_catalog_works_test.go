package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func tagWorkFixture(t *testing.T) (*Store, string) {
	t.Helper()
	s, _ := openTest(t)
	tagLibraries(t, s)
	tagEdition(t, s, "primary", "a", "artist", "other")
	tagEdition(t, s, "a-spare", "a", "artist", "other")
	tagEdition(t, s, "z-tagged", "a", "artist", "author")
	tagEdition(t, s, "second-tagged", "a", "artist", "author")
	tagEdition(t, s, "separate", "a", "artist", "author")
	tagEdition(t, s, "foreign", "b", "artist", "author")
	tx, e := s.Write.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE editions SET work_id='i:primary' WHERE id IN('a-spare','z-tagged','second-tagged');
 DELETE FROM works WHERE id IN('i:a-spare','i:z-tagged','i:second-tagged');
 UPDATE works SET name_override='Manual Work',revision=2 WHERE id='i:primary';
 UPDATE works SET name_override='Other Work' WHERE id='i:separate';
 INSERT INTO series VALUES('series-a','a','Series Alpha',3);
 INSERT INTO series_members VALUES('series-a','i:primary',1);
 UPDATE files SET content_revision=7,file_state='missing' WHERE edition_id='primary';
 UPDATE files SET content_revision=11,file_state='corrupt' WHERE edition_id='a-spare';
 INSERT INTO cover_selections(edition_id,mode,content_revision,revision) VALUES('primary','auto',7,8),('a-spare','auto',11,9);
 INSERT INTO effective_editions(edition_id,title,fields_json) VALUES('primary','Preferred heading','{}'),('a-spare','Unrelated heading','{}'),('z-tagged','Tagged needle','{}'),('second-tagged','Second needle','{}') ON CONFLICT(edition_id) DO UPDATE SET title=excluded.title,fields_json=excluded.fields_json;`); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return s, tagID("artist", "author")
}
func TestTagCatalogWorksDedupSearchAndBatchSummaries(t *testing.T) {
	s, id := tagWorkFixture(t)
	ctx := context.Background()
	in := tagInput("a")
	v, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil || v.Total != 2 || len(v.Items) != 2 {
		t.Fatal(v, e)
	}
	w := v.Items[0]
	if w.ID != "i:primary" || w.LibraryID != "a" || w.Name != "Manual Work" || w.Revision != 2 {
		t.Fatal(w)
	}
	if w.Series == nil || w.Series.ID != "series-a" || w.Series.Name != "Series Alpha" || w.Series.Position != 1 || w.Series.Revision != 3 {
		t.Fatal(w.Series)
	}
	if w.Cover.EditionID != "primary" || w.Cover.ContentRevision != 7 || w.Cover.CoverRevision != 8 || w.Cover.WorkID != w.ID {
		t.Fatal(w.Cover)
	}
	if v.Items[1].Series != nil {
		t.Fatal("unrelated Series invented")
	}
	for _, test := range []struct {
		q     string
		total int
	}{{"Manual", 1}, {"Tagged needle", 1}, {"Second needle", 1}, {"Unrelated heading", 0}, {"Preferred heading", 0}} {
		in.Query = test.q
		got, e := s.TagCatalogWorks(ctx, id, in)
		if e != nil || got.Total != test.total {
			t.Fatal(test, got, e)
		}
	}
	// Only a non-preferred Edition has this content tag; all namespaces are supported.
	setTagNames(t, s, "z-tagged", "artist", "author", "custom", "unknown")
	in = tagInput("a")
	got, e := s.TagCatalogWorks(ctx, tagID("custom", "unknown"), in)
	if e != nil || got.Total != 1 || got.Items[0].ID != "i:primary" {
		t.Fatal(got, e)
	}
	// Work inheritance stays tied to the preferred metadata, not to a tagged member.
	if _, e = s.Write.Exec("UPDATE works SET name_override=NULL,revision=revision+1 WHERE id='i:primary'"); e != nil {
		t.Fatal(e)
	}
	in.Query = "Preferred heading"
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Total != 1 || got.Items[0].Name != "Preferred heading" {
		t.Fatal(got, e)
	}
}
func TestTagCatalogWorksScopeAndRecycledPreferred(t *testing.T) {
	s, id := tagWorkFixture(t)
	ctx := context.Background()
	in := tagInput("a")
	if e := s.Initialize(ctx, Account{ID: "tag-account", PasswordHash: "anonymous", Revision: 1}, Session{Hash: make([]byte, 32), AccountID: "tag-account", Created: 1, Expires: 2}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Write.Exec("INSERT INTO edition_user_states VALUES('tag-account','z-tagged',0,1,1);UPDATE libraries SET disabled=1 WHERE id='a'"); e != nil {
		t.Fatal(e)
	}
	got, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Total != 2 {
		t.Fatal("hidden/offline/disabled scope lost", got, e)
	}
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=1 WHERE id='primary'"); e != nil {
		t.Fatal(e)
	}
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil {
		t.Fatal(e)
	}
	w := got.Items[0]
	if w.Cover.EditionID != "a-spare" || w.Cover.ContentRevision != 11 || w.Cover.CoverRevision != 9 {
		t.Fatal("unstable representative", w)
	}
	if w.Name != "Manual Work" {
		t.Fatal("cover replacement changed name", w)
	}
	if _, e = s.Write.Exec("UPDATE works SET name_override=NULL,revision=revision+1 WHERE id='i:primary'"); e != nil {
		t.Fatal(e)
	}
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Items[1].Name != "Preferred heading" {
		t.Fatal("recycled preferred stopped inheritance", got, e)
	}
	// Trashing every tagged member removes that Work even if untagged members remain.
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=1 WHERE id IN('z-tagged','second-tagged')"); e != nil {
		t.Fatal(e)
	}
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Total != 1 || got.Items[0].ID != "i:separate" {
		t.Fatal(got, e)
	}
	if _, e = s.Write.Exec("UPDATE editions SET trashed_at=1 WHERE work_id='i:primary'"); e != nil {
		t.Fatal(e)
	}
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Total != 1 {
		t.Fatal(got, e)
	}
	in = tagInput("b")
	got, e = s.TagCatalogWorks(ctx, id, in)
	if e != nil || got.Total != 1 || got.Items[0].ID != "i:foreign" {
		t.Fatal("cross-library result", got, e)
	}
	if _, e = s.Write.Exec("UPDATE libraries SET trashed_at=1 WHERE id='b'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.TagCatalogWorks(ctx, id, in); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("recycled library included", e)
	}
}
func TestTagCatalogWorksPagingAndSnapshotInvalidation(t *testing.T) {
	s, id := tagWorkFixture(t)
	ctx := context.Background()
	in := tagInput("a")
	in.PageSize = 1
	first, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil || first.Total != 2 {
		t.Fatal(first, e)
	}
	in.Page = 2
	in.Snapshot = first.Snapshot
	second, e := s.TagCatalogWorks(ctx, id, in)
	if e != nil || second.Total != 2 || second.Items[0].ID == first.Items[0].ID {
		t.Fatal(second, e)
	}
	fresh := func() {
		in = tagInput("a")
		in.PageSize = 1
		v, e := s.TagCatalogWorks(ctx, id, in)
		if e != nil {
			t.Fatal(e)
		}
		in.Page = 2
		in.Snapshot = v.Snapshot
	}
	stale := func() {
		if _, e := s.TagCatalogWorks(ctx, id, in); !errors.Is(e, model.ErrSnapshot) {
			t.Fatal("stale Work page accepted", e)
		}
	}
	fresh()
	in.Query = "needle"
	stale()
	fresh()
	if _, e = s.Write.Exec("UPDATE works SET name_override='Renamed',revision=revision+1 WHERE id='i:primary'"); e != nil {
		t.Fatal(e)
	}
	stale()
	fresh()
	if _, e = s.Write.Exec("UPDATE works SET preferred_edition_id='a-spare',revision=revision+1 WHERE id='i:primary'"); e != nil {
		t.Fatal(e)
	}
	stale()
	// Cover updates intentionally have no catalog_state bump; the new epoch catches them.
	fresh()
	if _, e = s.Write.Exec("UPDATE cover_selections SET revision=revision+1 WHERE edition_id='a-spare'"); e != nil {
		t.Fatal(e)
	}
	stale()
	fresh()
	if _, e = s.Write.Exec("UPDATE series SET name='Renamed Series',revision=revision+1 WHERE id='series-a';UPDATE catalog_state SET revision=revision+1"); e != nil {
		t.Fatal(e)
	}
	stale()
	fresh()
	if e = s.TranslateTag(ctx, id, "zh-CN", 0, strptr("作者译名")); e != nil {
		t.Fatal(e)
	}
	stale()
	fresh()
	setTagNames(t, s, "separate", "artist", "different")
	stale()
	other := tagInput("a")
	other.PageSize = 1
	ed, e := s.TagCatalogEditions(ctx, id, other)
	if e != nil {
		t.Fatal(e)
	}
	in = other
	in.Snapshot = ed.Snapshot
	if _, e = s.TagCatalogWorks(ctx, id, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("Edition snapshot reused", e)
	}
	detail, e := s.TagCatalogDetail(ctx, id, other)
	if e != nil {
		t.Fatal(e)
	}
	in.Snapshot = detail.Snapshot
	if _, e = s.TagCatalogWorks(ctx, id, in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal("detail snapshot reused", e)
	}
	if _, e = s.TagCatalogWorks(ctx, "nonexistent", other); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	if _, e = s.TagCatalogWorks(ctx, id, tagInput("nonexistent")); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
}

func TestTagCatalogWorksReadTransactionDuringConcurrentRename(t *testing.T) {
	s, id := tagWorkFixture(t)
	ctx := context.Background()
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if _, e := s.Write.ExecContext(ctx, `UPDATE works SET name_override=CASE WHEN name_override='fluctuate' THEN 'other' ELSE 'fluctuate' END,revision=revision+1 WHERE id='i:primary'`); e != nil {
				done <- e
				return
			}
		}
	}()
	defer func() {
		close(stop)
		if e := <-done; e != nil {
			t.Error(e)
		}
	}()
	in := tagInput("a")
	in.Query = "fluctuate"
	for i := 0; i < 20; i++ {
		v, e := s.TagCatalogWorks(ctx, id, in)
		if e != nil {
			t.Fatal(e)
		}
		if v.Total != len(v.Items) || v.Total > 1 {
			t.Fatal("count and page observed different snapshots", v)
		}
		if v.Total == 1 && v.Items[0].Name != "fluctuate" {
			t.Fatal(v)
		}
	}
}

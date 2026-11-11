package store

import (
	"bytes"
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestM4UpgradeAndIndependentUserState(t *testing.T) {
	ctx := context.Background()
	first, _ := migrations.Files.ReadFile("001_foundation.sql")
	second, _ := migrations.Files.ReadFile("002_metadata.sql")
	dir := t.TempDir()
	s, e := Open(ctx, dir, fstest.MapFS{"001_foundation.sql": {Data: first}, "002_metadata.sql": {Data: second}})
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, s)
	if e = s.Initialize(ctx, model.Account{ID: "admin", PasswordHash: "anonymous", Revision: 1}, model.Session{Hash: make([]byte, 32), AccountID: "admin", Created: 1, Expires: 99}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec(`INSERT INTO field_overrides VALUES('book','title','value','"匿名人工标题"'),('book','title_original','value','"日本語"');INSERT INTO effective_books VALUES('book','匿名人工标题','{"title":"匿名人工标题","title_original":"日本語"}') ON CONFLICT(book_id) DO UPDATE SET title=excluded.title,fields_json=excluded.fields_json;UPDATE books SET revision=2 WHERE id='book';INSERT INTO jobs(id,library_id,type,state,stage,generation,total,created_at,updated_at) VALUES('retained','lib','metadata','paused','processing',2,1,1,1);INSERT INTO job_items(job_id,ordinal,state,book_id,content_revision,selection_revision) VALUES('retained',1,'pending','book',1,1)`); e != nil {
		t.Fatal(e)
	}
	before := model.Edition{ID: "book", Title: "匿名人工标题", Revision: 2}
	job := model.Job{ID: "retained"}
	s.Close()
	s, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var item string
	if e = s.Read.QueryRow("SELECT work_id FROM editions WHERE id='book'").Scan(&item); e != nil || item == "" {
		t.Fatal(item, e)
	}
	v := true
	state, e := s.SetUserState(ctx, "admin", "book", model.StatePatch{Favorite: &v})
	if e != nil || !state.Favorite || state.Revision != 1 {
		t.Fatal(state, e)
	}
	if _, e = s.SetUserState(ctx, "admin", "book", model.StatePatch{Hidden: &v}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	b, _ := s.Edition(ctx, "book")
	if b.Revision != before.Revision || b.Title != before.Title {
		t.Fatal("state invalidated metadata version")
	}
	var key []byte
	var title string
	if e = s.Read.QueryRow("SELECT title,sort_key FROM search_documents WHERE edition_id='book'").Scan(&title, &key); e != nil || title != "匿名人工标题" || bytes.Equal(key, nil) {
		t.Fatal(title, key, e)
	}
	retained, e := s.Job(ctx, job.ID)
	if e != nil || retained.State != "paused" {
		t.Fatal(retained, e)
	}
	details, e := s.MetadataDetails(ctx, "book")
	if e != nil || details.Fields.TitleOriginal == nil || *details.Fields.TitleOriginal != "日本語" {
		t.Fatal(details, e)
	}
	snapshots, e := filepath.Glob(filepath.Join(dir, "upgrade-snapshots", "*.sqlite"))
	if e != nil || len(snapshots) != 1 {
		t.Fatal(snapshots, e)
	}
	info, e := os.Stat(snapshots[0])
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, e)
	}

}

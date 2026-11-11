package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func seedCapacityWork(t *testing.T, s *Store) {
	t.Helper()
	tx, e := s.Write.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO works(id,library_id,preferred_edition_id,revision) VALUES('capacity','lib','cap-001',1);
 WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<100) INSERT INTO editions(id,library_id,work_id,revision,organization_revision) SELECT printf('cap-%03d',i),'lib','capacity',1,1 FROM n;
 INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_algorithm,page_count,file_state,indexed_at) SELECT id,library_id,id||'.cbz',id,1,1,1,'content-page8-v1',0,'available',1 FROM editions WHERE work_id='capacity';
 INSERT INTO search_documents SELECT edition_id,nya_normalize_v1(filename_title),'',nya_natural_v1(filename_title) FROM files WHERE edition_id LIKE 'cap-%';`)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}
func TestWorkCapacityAndLibraryRejectionMatchPreview(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	seedCapacityWork(t, s)
	for _, c := range []model.WorkCommand{{Action: "edition_move", EditionID: "book", TargetWorkID: "capacity"}, {Action: "work_merge", ID: "capacity", WorkIDs: []string{"i:book"}}} {
		c.Expected = commandContext(t, o, c).Expected
		for _, preview := range []bool{true, false} {
			if _, e := o.ChangeWork(ctx, "a", c, preview); !errors.Is(e, model.ErrCapacity) {
				t.Fatal(c.Action, preview, e)
			}
		}
	}
	if _, e := s.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('foreign','foreign','Other','utf8','anonymous',1)`); e != nil {
		t.Fatal(e)
	}
	seedEdition(t, s, "foreign", "foreign", "foreign.cbz", "Other")
	for _, c := range []model.WorkCommand{{Action: "work_merge", ID: "i:book", WorkIDs: []string{"i:foreign"}}, {Action: "edition_move", EditionID: "foreign", TargetWorkID: "i:book"}, {Action: "series_create", LibraryID: "lib", Name: "Wrong library", WorkIDs: []string{"i:foreign"}}} {
		c.Expected = commandContext(t, o, c).Expected
		for _, preview := range []bool{true, false} {
			if _, e := o.ChangeWork(ctx, "a", c, preview); !errors.Is(e, model.ErrLibraryConflict) {
				t.Fatal(c.Action, preview, e)
			}
		}
	}
	if e := checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}

type countedQueries struct {
	base  queryer
	count int
}

func (q *countedQueries) QueryContext(c context.Context, s string, a ...any) (*sql.Rows, error) {
	q.count++
	return q.base.QueryContext(c, s, a...)
}
func (q *countedQueries) QueryRowContext(c context.Context, s string, a ...any) *sql.Row {
	q.count++
	return q.base.QueryRowContext(c, s, a...)
}
func TestLargeSeriesContextUsesBoundedQueriesAndCapacity(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:book", WorkIDs: []string{"i:b2"}})
	tx, e := s.Write.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	_, e = tx.Exec(`WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<999) INSERT INTO works(id,library_id,preferred_edition_id,revision) SELECT printf('large-work-%04d',i),'lib',printf('large-edition-%04d',i),1 FROM n;
 WITH RECURSIVE n(i) AS(VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<999) INSERT INTO editions(id,library_id,work_id,revision,organization_revision) SELECT printf('large-edition-%04d',i),'lib',printf('large-work-%04d',i),1,1 FROM n;
 INSERT INTO files(edition_id,library_id,relative_path,filename_title,size,mtime_ns,content_revision,representative_algorithm,page_count,file_state,indexed_at) SELECT id,library_id,id||'.cbz',id,1,1,1,'content-page8-v1',0,'available',1 FROM editions WHERE id LIKE 'large-edition-%';
 INSERT INTO series VALUES('large','lib','Large',1);
 INSERT INTO series_members VALUES('large','i:book',1);
 INSERT INTO series_members SELECT 'large',id,CAST(substr(id,12) AS INTEGER)+1 FROM works WHERE id LIKE 'large-work-%';`)
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	read, e := s.Read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	q := &countedQueries{base: read}
	g, e := loadWorkContext(ctx, q, "a", nil, []string{"i:book"}, nil, true)
	read.Rollback()
	if e != nil || len(g.Works) != 1000 || len(g.Editions) != 2 || q.count > 8 {
		t.Fatal("unbounded context", len(g.Works), len(g.Editions), q.count, e)
	}
	c := model.WorkCommand{Action: "work_split", ID: "i:book", EditionIDs: []string{"b2"}, NewWorkID: "new", KeepSeries: true, Expected: g.Expected}
	for _, preview := range []bool{true, false} {
		if _, e = o.ChangeWork(ctx, "a", c, preview); !errors.Is(e, model.ErrCapacity) {
			t.Fatal(preview, e)
		}
	}
	page, e := s.SeriesWorkPage(ctx, "large", "", 2, 50)
	if e != nil || page.Total != 1000 || len(page.Items) != 50 || page.Expected["series:large"] != 1 {
		t.Fatal(page, e)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
func TestWorkPurgeRetainsSingleAndRemovesOnlyEmpty(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:book", WorkIDs: []string{"i:b2"}})
	series := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Series", WorkIDs: []string{"i:book", "i:b1"}})
	purge := func(id string) {
		t.Helper()
		b, e := s.Edition(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		trash, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{id}, Expected: map[string]int64{"book:" + id: b.Revision}})
		if e != nil {
			t.Fatal(e)
		}
		applyPlan(t, s, trash)
		b, e = s.Edition(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		plan, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "purge", BookIDs: []string{id}, Expected: map[string]int64{"book:" + id: b.Revision}})
		if e != nil {
			t.Fatal(e)
		}
		applyPlan(t, s, plan)
	}
	purge("book")
	w, e := s.Work(ctx, "i:book")
	if e != nil || w.EditionCount != 1 || w.PreferredEditionID != "b2" || w.SeriesID != series.ID || w.Position != 1 {
		t.Fatal(w, e)
	}
	purge("b2")
	if _, e = s.Work(ctx, "i:book"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	w, e = s.Work(ctx, "i:b1")
	if e != nil || w.Position != 1 || w.SeriesID != series.ID {
		t.Fatal(w, e)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
func TestPausedMetadataCheckpointSurvivesWorkMove(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	job, e := s.CreateMetadataJob(ctx, "paused-work", "", []string{"book"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, job.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	original, e := s.Items(ctx, job.ID, 1, 10, false)
	if e != nil {
		t.Fatal(e)
	}
	changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:b1", WorkIDs: []string{"i:book"}})
	before, e := s.Job(ctx, job.ID)
	if e != nil || before.State != "paused" || before.Generation != 2 {
		t.Fatal(before, e)
	}
	if e = s.ControlJob(ctx, job.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	claimed, e := s.Claim(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, job.ID, 1, 10, true)
	if e != nil || items[0].Input != original[0].Input || *items[0].BookID != "book" {
		t.Fatal(items, e)
	}
	title := "Source after work move"
	remote := model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Method: "manual", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{}`)}
	if e = s.PublishMetadata(ctx, *claimed, items[0], &remote, "", nil); e != nil {
		t.Fatal(e)
	}
	b, e := s.Edition(ctx, "book")
	if e != nil || b.WorkID != "i:b1" || b.Title != title {
		t.Fatal(b, e)
	}
}

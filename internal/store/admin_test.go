package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/migrations"
	"testing"
	"testing/fstest"
)

func applyPlan(t *testing.T, s *Store, p model.MaintenancePlan) {
	t.Helper()
	ctx := context.Background()
	job, e := s.ExecuteMaintenance(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.ExecuteMaintenance(ctx, p.ID)
	if e != nil || again.ID != job.ID {
		t.Fatal("not idempotent", again, e)
	}
	j, e := s.Claim(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, j.ID, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PauseMaintenanceResources(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyMaintenance(ctx, *j, items[0], p, "", "", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
}
func TestManagementTrashRestorePurgePreservesPrimaryHistory(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	r := changeWork(t, o, model.WorkCommand{Action: "work_merge", ID: "i:b10", WorkIDs: []string{"i:book", "i:b2"}})
	job, e := s.CreateMetadataJob(ctx, "history", "", []string{"b2"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, job.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	b, _ := s.Edition(ctx, "b2")
	p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{b.ID}, Expected: map[string]int64{"book:" + b.ID: b.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	applyPlan(t, s, p)
	b, _ = s.Edition(ctx, b.ID)
	if b.TrashedAt == nil {
		t.Fatal("not trashed")
	}
	v, e := search.New(s).Search(ctx, "a", model.SearchInput{Collapse: "none"})
	if e != nil || v.Total != 3 {
		t.Fatal(v, e)
	}
	p, e = s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "purge", BookIDs: []string{b.ID}, Expected: map[string]int64{"book:" + b.ID: b.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	applyPlan(t, s, p)
	if _, e = s.Edition(ctx, b.ID); !errors.Is(e, model.ErrNotFound) {
		t.Fatal(e)
	}
	work, e := s.Work(ctx, r.ID)
	if e != nil || work.EditionCount != 2 || work.PreferredEditionID != "b10" {
		t.Fatal(work, e)
	}
	items, e := s.Items(ctx, job.ID, 1, 10, false)
	if e != nil || items[0].BookID != nil {
		t.Fatal(items, e)
	}
	excluded, e := s.ScanExclusions(ctx, "lib")
	if e != nil || !excluded["b2.cbz"] {
		t.Fatal(excluded, e)
	}
	if e = checkOrganization(ctx, s.Read); e != nil {
		t.Fatal(e)
	}
}
func TestMaintenanceStaleRollbackAndLibraryBookFreeze(t *testing.T) {
	s, _ := orgFixture(t)
	ctx := context.Background()
	p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "trash", LibraryID: "lib", Expected: map[string]int64{"library:lib": 1}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='book'"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ExecuteMaintenance(ctx, p.ID); !errors.Is(e, model.ErrConflict) {
		t.Fatal("preview did not freeze books", e)
	}
	b, _ := s.Edition(ctx, "book")
	p, e = s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{"book"}, Expected: map[string]int64{"book:book": b.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.ExecuteMaintenance(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, _ := s.Items(ctx, j.ID, 1, 1, true)
	if e = s.ControlJob(ctx, j.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyMaintenance(ctx, *j, items[0], p, "", "", ""); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	after, _ := s.Edition(ctx, "book")
	if after.TrashedAt != nil {
		t.Fatal("cancelled result published")
	}
}
func TestSourcePreviewPreservesAndConflicts(t *testing.T) {
	s, _ := orgFixture(t)
	ctx := context.Background()
	job, e := s.CreateSourcePreview(ctx, "book", "https://e-hentai.org/g/1/0123456789/", 1, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	items, _ := s.Items(ctx, job.ID, 1, 1, true)
	if e = s.ConfirmSourcePreview(ctx, "book", items[0].PreviewID, 1, 1, 1); !errors.Is(e, model.ErrConflict) {
		t.Fatal("pending preview", e)
	}
	j, e := s.Claim(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	title := "Source"
	remote := model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"gid":1}`)}
	if e = s.PublishSourcePreview(ctx, *j, items[0], remote); e != nil {
		t.Fatal(e)
	}
	d, _ := s.MetadataDetails(ctx, "book")
	if d.Source != nil || d.ManualURL != "" {
		t.Fatal("preview adopted early", d)
	}
	if e = s.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Manual"`)}}); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmSourcePreview(ctx, "book", items[0].PreviewID, 1, 1, 1); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale draft accepted", e)
	}
	b, _ := s.Edition(ctx, "book")
	if b.Title != "Manual" {
		t.Fatal(b)
	}
}
func TestOrganizationPreviewCapacityMatchesCommit(t *testing.T) {
	g := model.WorkContext{Works: []model.Work{{ID: "w", LibraryID: "lib", EditionIDs: []string{"a", "b"}, PreferredEditionID: "a", SeriesID: "s", Revision: 1}}, Editions: []model.WorkEdition{{ID: "a", WorkID: "w", LibraryID: "lib"}, {ID: "b", WorkID: "w", LibraryID: "lib"}}, Series: []model.Series{{ID: "s", LibraryID: "lib", WorkIDs: make([]string, 1000)}}, Expected: map[string]int64{}}
	for i := range g.Series[0].WorkIDs {
		g.Series[0].WorkIDs[i] = fmt.Sprintf("w%d", i)
	}
	g.Series[0].WorkIDs[0] = "w"
	_, e := organization.PlanWork(g, model.WorkCommand{Action: "work_split", ID: "w", EditionIDs: []string{"b"}, NewWorkID: "new", KeepSeries: true})
	if !errors.Is(e, model.ErrCapacity) {
		t.Fatal(e)
	}
}
func TestManagementJobPagingAndMembers(t *testing.T) {
	s, o := orgFixture(t)
	ctx := context.Background()
	for _, id := range []string{"j1", "j2", "j3"} {
		if _, e := s.CreateTranslationJob(ctx, id, ""); e != nil {
			t.Fatal(e)
		}
		if e := s.ControlJob(ctx, id, "cancel", ""); e != nil {
			t.Fatal(e)
		}
	}
	jobs, total, e := s.JobPage(ctx, "tag_translation", "cancelled", "", 2, 2)
	if e != nil || total != 3 || len(jobs) != 1 {
		t.Fatal(jobs, total, e)
	}
	r := changeWork(t, o, model.WorkCommand{Action: "series_create", LibraryID: "lib", Name: "Series", WorkIDs: []string{"i:book", "i:b2"}})
	summary, e := s.SeriesDetail(ctx, r.ID)
	if e != nil || summary.MemberCount != 2 || len(summary.WorkIDs) != 0 {
		t.Fatal(summary, e)
	}
	page, e := s.SeriesWorkPage(ctx, r.ID, "", 1, 1)
	if e != nil || page.Total != 2 || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	filtered, e := s.SeriesWorkPage(ctx, r.ID, "Filename", 1, 50)
	if e != nil || filtered.Total != 1 || filtered.Items[0].Name != "Filename" {
		t.Fatal(filtered, e)
	}

}
func TestAdministrationUpgradePreservesSchemaThreeAndRollback(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := fstest.MapFS{}
	for _, name := range []string{"001_foundation.sql", "002_metadata.sql", "003_organization.sql"} {
		raw, e := migrations.Files.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		base[name] = &fstest.MapFile{Data: raw}
	}
	db, e := Open(ctx, dir, base)
	if e != nil {
		t.Fatal(e)
	}
	seedBook(t, db)
	job := model.Job{ID: "retained"}
	if _, e = db.Write.Exec(`INSERT INTO jobs(id,library_id,type,state,stage,generation,total,created_at,updated_at) VALUES('retained','lib','metadata','paused','processing',2,1,1,1);INSERT INTO job_items(job_id,ordinal,state,book_id,content_revision,selection_revision) VALUES('retained',1,'pending','book',1,1);INSERT INTO field_overrides VALUES('book','title','value','"Retained"');INSERT INTO effective_books VALUES('book','Retained','{"title":"Retained"}') ON CONFLICT(book_id) DO UPDATE SET title=excluded.title,fields_json=excluded.fields_json;UPDATE books SET revision=2 WHERE id='book';`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	bad := fstest.MapFS{}
	for name, v := range base {
		bad[name] = v
	}
	sql4, _ := migrations.Files.ReadFile("004_administration.sql")
	bad["004_administration.sql"] = &fstest.MapFile{Data: append(append([]byte{}, sql4...), []byte("; INSERT INTO absent_table VALUES(1);")...)}
	if failed, e := Open(ctx, dir, bad); e == nil {
		failed.Close()
		t.Fatal("failed migration accepted")
	}
	db, e = Open(ctx, dir, base)
	if e != nil {
		t.Fatal("rollback changed baseline", e)
	}
	var n int
	if e = db.Read.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='assets'").Scan(&n); e != nil || n != 0 {
		t.Fatal("migration partially committed", n, e)
	}
	db.Close()
	db, e = Open(ctx, dir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	b, e := db.Edition(ctx, "book")
	if e != nil || b.Title != "Retained" || b.ID != "book" {
		t.Fatal(b, e)
	}
	saved, e := db.Job(ctx, job.ID)
	if e != nil || saved.State != "paused" || saved.Generation != 2 {
		t.Fatal(saved, e)
	}
	items, e := db.Items(ctx, job.ID, 1, 100, false)
	if e != nil || len(items) != 1 || items[0].State != "pending" {
		t.Fatal(items, e)
	}
	if db.Version != 30 {
		t.Fatal(db.Version)
	}
	rows, e := db.Read.Query("PRAGMA foreign_key_check")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("upgrade broke references")
	}
}
func TestPurgeRejectsActiveAndRollsBackSQLFailure(t *testing.T) {
	s, _ := orgFixture(t)
	ctx := context.Background()
	if _, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "purge", BookIDs: []string{"book"}, Expected: map[string]int64{"book:book": 1}}); !errors.Is(e, model.ErrInput) {
		t.Fatal("active purge preview accepted", e)
	}
	p, e := s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "trash", BookIDs: []string{"book"}, Expected: map[string]int64{"book:book": 1}})
	if e != nil {
		t.Fatal(e)
	}
	applyPlan(t, s, p)
	b, _ := s.Edition(ctx, "book")
	p, e = s.CreateMaintenancePlan(ctx, model.MaintenanceInput{Operation: "purge", BookIDs: []string{"book"}, Expected: map[string]int64{"book:book": b.Revision}})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.ExecuteMaintenance(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, _ := s.Items(ctx, j.ID, 1, 1, true)
	if _, e = s.Write.Exec("CREATE TRIGGER refuse_purge BEFORE DELETE ON editions BEGIN SELECT RAISE(ABORT,'injected rollback'); END"); e != nil {
		t.Fatal(e)
	}
	if e = s.ApplyMaintenance(ctx, *j, items[0], p, "", "", ""); e == nil {
		t.Fatal("injected transaction succeeded")
	}
	after, e := s.Edition(ctx, "book")
	if e != nil || after.TrashedAt == nil {
		t.Fatal("purge partial rollback", after, e)
	}
	var n int
	_ = s.Read.QueryRow("SELECT count(*) FROM editions WHERE id='book'").Scan(&n)
	if n != 1 {
		t.Fatal("organization partially deleted")
	}
}
func TestReadySourcePreviewConfirmWithoutMetadataState(t *testing.T) {
	s, _ := orgFixture(t)
	ctx := context.Background()
	job, e := s.CreateSourcePreview(ctx, "book", "https://e-hentai.org/g/1/0123456789/", 1, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Claim(ctx, job.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, job.ID, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	title := "Source"
	remote := model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"gid":1}`)}
	if e = s.PublishSourcePreview(ctx, *j, items[0], remote); e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *j, ""); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmSourcePreview(ctx, "book", items[0].PreviewID, 1, 1, 1); e != nil {
		t.Fatal(e)
	}
	d, e := s.MetadataDetails(ctx, "book")
	if e != nil || d.Source == nil || d.SelectionRevision != 2 || d.ManualURL != "https://e-hentai.org/g/1/0123456789/" {
		t.Fatal(d, e)
	}
}
func TestAdminMetadataStateFilter(t *testing.T) {
	s, _ := orgFixture(t)
	ctx := context.Background()
	title := "Source"
	tx, e := s.Write.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = publishSource(ctx, tx, "book", model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"gid":1}`)}); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	svc := search.New(s)
	for state, want := range map[string]int{"none": 3, "matched": 1, "needs_review": 0} {
		result, e := svc.Admin(ctx, "a", model.SearchInput{MetadataState: state})
		if e != nil || result.Total != want {
			t.Fatal(state, result.Total, e)
		}
	}
	if _, e = s.Write.Exec("UPDATE files SET content_revision=content_revision+1 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	result, e := svc.Admin(ctx, "a", model.SearchInput{MetadataState: "needs_review"})
	if e != nil || result.Total != 1 || result.Items[0].ID != "book" {
		t.Fatal(result, e)
	}
	if _, e = svc.Admin(ctx, "a", model.SearchInput{MetadataState: "unknown"}); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
}

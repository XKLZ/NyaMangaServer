package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"sync/atomic"
	"testing"
)

type searchRemoteFixture struct {
	*controlledRemote
	pages atomic.Int32
	fills atomic.Int32
	pause atomic.Bool
}

func (r *searchRemoteFixture) SearchPage(ctx context.Context, mode, q, cursor, site string) (model.SearchPage, error) {
	r.pages.Add(1)
	p := model.SearchPage{Items: []model.Candidate{}}
	if cursor == "" {
		for i := 0; i < 17; i++ {
			p.Items = append(p.Items, model.Candidate{URL: fmt.Sprintf("https://e-hentai.org/g/%d/0123456789/", i+1), Title: fmt.Sprintf("Anonymous %d", i)})
		}
	}
	return p, ctx.Err()
}
func (r *searchRemoteFixture) FetchMany(ctx context.Context, urls []string) ([]model.FetchResult, error) {
	r.fills.Add(1)
	if r.pause.Load() {
		return nil, &model.RemoteError{Code: "provider_auth_required", ActionRequired: true}
	}
	out := []model.FetchResult{}
	for _, u := range urls {
		title := "Anonymous"
		_, gid, _, _ := provider.Gallery(u)
		m := model.RemoteMetadata{URL: u, ExternalID: gid, Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{}`)}
		out = append(out, model.FetchResult{URL: u, Metadata: &m})
	}
	return out, ctx.Err()
}
func TestSourceSearchWorkerPagesAndAuthResume(t *testing.T) {
	svc, db, r, _ := setup(t)
	ctx := context.Background()
	remote := &searchRemoteFixture{controlledRemote: r}
	svc.remote = remote
	_, e := db.Write.Exec(`INSERT INTO import_batches(id,library_id,request_key,stage,state) VALUES('search-batch','lib','search-batch','metadata','review');INSERT INTO import_members(batch_id,edition_id,change_kind,content_revision) VALUES('search-batch','book','new',1)`)
	if e != nil {
		t.Fatal(e)
	}
	remote.pause.Store(true)
	v, e := svc.CreateSourceSearch(ctx, "search-batch", "book", model.SourceSearchInput{Mode: "title", Query: "anonymous", RequestKey: "one", ExpectedRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	svc.execute(ctx, v.JobID)
	waitJob(t, db, v.JobID, "paused")
	page, e := svc.SearchResults(ctx, "search-batch", "book", v.ID, 1)
	if e != nil || page.Search.Loaded != 17 || len(page.Items) != 10 || page.Items[0].State != "pending" {
		t.Fatal(page, e)
	}
	remote.pause.Store(false)
	if e = svc.ControlSourceSearch(ctx, "search-batch", "book", v.ID, "resume", page.Search.Revision); e != nil {
		t.Fatal(e)
	}
	svc.execute(ctx, v.JobID)
	waitJob(t, db, v.JobID, "completed")
	page, e = svc.SearchResults(ctx, "search-batch", "book", v.ID, 1)
	if e != nil || page.Items[0].State != "ready" || remote.pages.Load() != 1 {
		t.Fatal(page, e)
	}
	second, e := svc.LoadSourceSearch(ctx, "search-batch", "book", v.ID, model.SearchLoad{Page: 2, ExpectedRevision: page.Search.Revision, RequestKey: "page-two"})
	if e != nil {
		t.Fatal(e)
	}
	svc.execute(ctx, second.JobID)
	waitJob(t, db, second.JobID, "completed")
	page, e = svc.SearchResults(ctx, "search-batch", "book", v.ID, 2)
	if e != nil || len(page.Items) != 7 || page.Items[6].State != "ready" || remote.pages.Load() != 1 {
		t.Fatal(page, e)
	}
	var count int
	db.Read.QueryRow("SELECT count(*) FROM edition_sources").Scan(&count)
	if count != 0 {
		t.Fatal("worker published formal sources")
	}
}

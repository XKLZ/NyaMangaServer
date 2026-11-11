package store

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestMetadataTaskGenerationAndConcurrentOwnership(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	j, e := s.CreateMetadataJob(ctx, "first", "", []string{"book"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateMetadataJob(ctx, "overlap", "", []string{"book"}, nil); !errors.Is(e, model.ErrJobActive) {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, j.ID, 1, 10, true)
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	item := items[0]
	title := "First source"
	r := model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Method: "hash", Fields: model.Metadata{Title: &title, Tags: []model.TagName{}}, Raw: json.RawMessage(`{"gid":1}`)}
	if e = s.ControlJob(ctx, j.ID, "pause", ""); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishMetadata(ctx, *j, item, &r, "", nil); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	if _, e = s.CreateMetadataJob(ctx, "paused-overlap", "", []string{"book"}, nil); !errors.Is(e, model.ErrJobActive) {
		t.Fatal(e)
	}
	if e = s.ControlJob(ctx, j.ID, "resume", ""); e != nil {
		t.Fatal(e)
	}
	newGeneration, e := s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PublishMetadata(ctx, *j, item, &r, "", nil); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	b, _ := s.Edition(ctx, "book")
	if e = s.ApplyOverrides(ctx, b.ID, b.Revision, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"Edited during request"`)}}); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishMetadata(ctx, *newGeneration, item, &r, "", nil); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishMetadata(ctx, *newGeneration, item, &r, "", nil); e != nil {
		t.Fatal(e)
	}
	b, _ = s.Edition(ctx, "book")
	if b.Title != "Edited during request" {
		t.Fatal(b)
	}
	j, _ = s.Job(ctx, j.ID)
	if j.Done != 1 {
		t.Fatal(j)
	}
	if e = s.FinishBackground(ctx, *newGeneration, ""); e != nil {
		t.Fatal(e)
	}
	next, e := s.CreateMetadataJob(ctx, "next", j.ID, []string{"book"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	next, e = s.Claim(ctx, next.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, _ = s.Items(ctx, next.ID, 1, 10, true)
	if _, e = s.Write.Exec("UPDATE files SET content_revision=2 WHERE edition_id='book'"); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishMetadata(ctx, *next, items[0], &r, "", nil); e != nil {
		t.Fatal(e)
	}
	items, _ = s.Items(ctx, next.ID, 1, 10, false)
	if items[0].Error != "content_changed" {
		t.Fatal(items)
	}
	if e = s.ControlJob(ctx, next.ID, "cancel", ""); e != nil {
		t.Fatal(e)
	}
	replacement, e := s.CreateMetadataJob(ctx, "replacement", "", []string{"book"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.FinishBackground(ctx, *next, ""); !errors.Is(e, model.ErrStaleJob) {
		t.Fatal(e)
	}
	var owner string
	if e = s.Read.QueryRow("SELECT job_id FROM job_resources WHERE resource_key='metadata:book:book'").Scan(&owner); e != nil || owner != replacement.ID {
		t.Fatal(owner, e)
	}
}
func TestEventsAndLookupRetention(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	events, high, gap, e := s.Events(ctx, 0, 100)
	if e != nil || gap || len(events) != 2 || high < 1 {
		t.Fatal(events, high, gap, e)
	}
	if _, e = s.Write.Exec("UPDATE editions SET revision=revision+1 WHERE id='book'"); e != nil {
		t.Fatal(e)
	}
	events, _, gap, e = s.Events(ctx, high, 100)
	if e != nil || gap || len(events) != 1 {
		t.Fatal(events, gap, e)
	}
	if _, e = s.Write.Exec("UPDATE events SET created_at=0"); e != nil {
		t.Fatal(e)
	}
	_, _, gap, e = s.Events(ctx, high, 100)
	if e != nil || !gap {
		t.Fatal(gap, e)
	}
	candidates := []model.Candidate{{URL: "https://e-hentai.org/g/1/0123456789/", Title: "Anonymous"}}
	if e = s.CacheHash(ctx, "eh", "abc", candidates); e != nil {
		t.Fatal(e)
	}
	got, found, e := s.LookupHash(ctx, "eh", "abc")
	if e != nil || !found || len(got) != 1 {
		t.Fatal(got, found, e)
	}
	if _, found, e = s.LookupHash(ctx, "ex", "abc"); e != nil || found {
		t.Fatal(found, e)
	}
}

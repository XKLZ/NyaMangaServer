package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestLookupChangeRejectsOldMetadataWithoutChangingHumanRevision(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	before, e := s.Edition(ctx, "book")
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.CreateMetadataJob(ctx, "lookup-history", "", []string{"book"}, nil)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, e := s.Items(ctx, j.ID, 1, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateLookup(ctx, *before, "new-content-sha1", 8); e != nil {
		t.Fatal(e)
	}
	if e = s.PublishMetadata(ctx, *j, items[0], nil, "no_match", nil); e != nil {
		t.Fatal(e)
	}
	items, _ = s.Items(ctx, j.ID, 1, 1, false)
	if items[0].Error != "lookup_input_changed" {
		t.Fatal(items)
	}
	after, _ := s.Edition(ctx, "book")
	if after.ContentRevision != before.ContentRevision || after.Revision != before.Revision || after.RepresentativeAlgorithm != "content-page8-v1" {
		t.Fatal(before, after)
	}
	if e = s.UpdateLookup(ctx, *before, "stale", 9); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale rehash accepted", e)
	}
	candidates, e := s.RehashCandidates(ctx, "", 100)
	if e != nil || len(candidates) != 0 {
		t.Fatal(candidates, e)
	}
}

func TestLookupChangeRejectsReadySourcePreview(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	b, _ := s.Edition(ctx, "book")
	j, e := s.CreateSourcePreview(ctx, b.ID, "https://e-hentai.org/g/1/0123456789/", b.Revision, 1, b.ContentRevision)
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	items, _ := s.Items(ctx, j.ID, 1, 1, true)
	result := model.RemoteMetadata{ExternalID: "1", URL: "https://e-hentai.org/g/1/0123456789/", Raw: []byte(`{}`), Fields: model.Metadata{Tags: []model.TagName{}}}
	if e = s.PublishSourcePreview(ctx, *j, items[0], result); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateLookup(ctx, *b, "new", b.PageCount); e != nil {
		t.Fatal(e)
	}
	if e = s.ConfirmSourcePreview(ctx, b.ID, items[0].PreviewID, b.Revision, 1, b.ContentRevision); !errors.Is(e, model.ErrConflict) {
		t.Fatal("stale preview accepted", e)
	}
}

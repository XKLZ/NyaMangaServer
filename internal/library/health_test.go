package library

import (
	"bytes"
	"context"
	"errors"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"path/filepath"
	"testing"
)

func TestHealthStableSelectedEntry(t *testing.T) {
	entry := 7
	name := "selected.png"
	page := 1
	v := model.CoverSelection{Mode: "page", State: "ready", PageIndex: &page, EntryIndex: &entry, EntryName: &name, DirectoryVersion: comic.DirectoryVersion}
	entries := []comic.Entry{{Index: 1, Entry: 4, Name: "new-first.png"}, {Index: 2, Entry: 7, Name: name}}
	selected, e := selectedCoverEntry(entries, v, true)
	if e != nil || selected.Entry != 7 || selected.Index != 2 {
		t.Fatal(selected, e)
	}
	v.State = "content_changed"
	if _, e = selectedCoverEntry(entries, v, true); !errors.Is(e, comic.ErrChanged) {
		t.Fatal(e)
	}
	v.State = "ready"
	v.EntryName = nil
	if _, e = selectedCoverEntry(entries, v, true); !errors.Is(e, comic.ErrChanged) {
		t.Fatal(e)
	}
}
func TestHealthCoverBytesUsesSelectionAndRejectsChangedContent(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.Editions(ctx, l.ID, "available", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	b := books[0]
	page := 2
	v, e := s.SetCover(ctx, b.ID, 0, model.CoverSelection{Mode: "page", PageIndex: &page, ContentRevision: b.ContentRevision})
	if e != nil {
		t.Fatal(e)
	}
	_, data, e := s.HealthCoverBytes(ctx, b.ID, true)
	if e != nil {
		t.Fatal(e)
	}
	normal, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, true)
	if e != nil || !bytes.Equal(data, normal) {
		t.Fatal("selection differs", e)
	}
	if v.EntryIndex == nil || v.EntryName == nil {
		t.Fatal(v)
	}
	if _, _, e = s.HealthCoverBytes(ctx, b.ID, false); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
}

func TestHealthMaintenanceUsesCachedCoverWithoutRebuild(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.Editions(ctx, l.ID, "available", 1, 100)
	if e != nil {
		t.Fatal(e)
	}
	b := books[0]
	before, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	selection, _ := s.CoverSelection(ctx, books[1].ID)
	if e = s.cache.Remove(filepath.Base(s.selectedCachePath(books[1], selection))); e != nil {
		t.Fatal(e)
	}
	db := s.store.(*store.Store)
	if _, e = db.Write.Exec("UPDATE health_state SET phase='paused'"); e != nil {
		t.Fatal(e)
	}
	cached, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, true)
	if e != nil || !bytes.Equal(before, cached) {
		t.Fatal(e)
	}
	if _, _, e = s.Image(ctx, books[1].ID, books[1].ContentRevision, 1, true); !errors.Is(e, model.ErrHealthMaintenance) {
		t.Fatal("cold cache generated during maintenance", e)
	}
	if _, _, e = s.Image(ctx, b.ID, b.ContentRevision, 1, false); e != nil {
		t.Fatal("read-only page blocked", e)
	}
}

package store

import (
	"context"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"reflect"
	"testing"
)

func TestTagCatalogRelationDisplayBeforePagination(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tagLibraries(t, s)
	for _, c := range []string{"00excluded", "01suppressed", "10one", "20two"} {
		tagEdition(t, s, c, "a", "parody", "alpha", "character", c)
	}
	tagEdition(t, s, "foreign", "b", "parody", "alpha", "character", "foreign")
	tagEdition(t, s, "zero", "b", "parody", "alpha", "character", "30confirmed", "character", "02history")
	p := tagID("parody", "alpha")
	for _, c := range []string{"00excluded", "02history"} {
		if _, e := s.DecideCharacterRelation(ctx, p, tagID("character", c), 0, "excluded"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.DecideCharacterRelation(ctx, p, tagID("character", "02history"), 1, "auto"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DecideCharacterRelation(ctx, p, tagID("character", "30confirmed"), 0, "confirmed"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetCharacterSuppression(ctx, "01suppressed", p, tagID("character", "01suppressed"), 0, true); e != nil {
		t.Fatal(e)
	}
	in := tagInput("a")
	all, e := s.TagCatalogRelations(ctx, p, in)
	if e != nil || all.Total != 6 {
		t.Fatal(all, e)
	}
	in.View = "all"
	explicit, e := s.TagCatalogRelations(ctx, p, in)
	if e != nil || !reflect.DeepEqual(all, explicit) {
		t.Fatal("default all changed", explicit, e)
	}
	in.View = "display"
	in.PageSize = 1
	first, e := s.TagCatalogRelations(ctx, p, in)
	if e != nil || first.Total != 3 || len(first.Items) != 1 || first.Items[0].Character.Name != "10one" {
		t.Fatal(first, e)
	}
	if first.UnclassifiedEditionCount != all.UnclassifiedEditionCount {
		t.Fatal("diagnostic lost")
	}
	for page, want := range map[int]string{2: "20two", 3: "30confirmed"} {
		in.Page = page
		in.Snapshot = first.Snapshot
		got, e := s.TagCatalogRelations(ctx, p, in)
		if e != nil || got.Total != 3 || len(got.Items) != 1 || got.Items[0].Character.Name != want {
			t.Fatal(got, e)
		}
		if page == 3 && (got.Items[0].EditionCount != 0 || got.Items[0].Decision != "confirmed") {
			t.Fatal(got)
		}
	}
	// Every displayed item retains exactly the all-view diagnostics and revisions.
	in = tagInput("a")
	in.View = "display"
	display, e := s.TagCatalogRelations(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	for _, r := range display.Items {
		found := false
		for _, old := range all.Items {
			if old.Character.ID == r.Character.ID {
				found = true
				if !reflect.DeepEqual(old, r) {
					t.Fatal(old, r)
				}
			}
		}
		if !found {
			t.Fatal(r)
		}
	}
	// Reverse direction and another library use the same SQL filter and library assignment counts.
	for _, c := range []string{"10one", "30confirmed"} {
		r, e := s.TagCatalogRelations(ctx, tagID("character", c), in)
		if e != nil || r.Total != 1 || r.Items[0].Parody.ID != p {
			t.Fatal(r, e)
		}
	}
	r, e := s.TagCatalogRelations(ctx, tagID("character", "00excluded"), in)
	if e != nil || r.Total != 0 {
		t.Fatal(r, e)
	}
	in.LibraryID = "b"
	r, e = s.TagCatalogRelations(ctx, p, in)
	if e != nil {
		t.Fatal(e)
	}
	for _, x := range r.Items {
		if x.Character.Name == "10one" || x.Character.Name == "20two" {
			t.Fatal("cross-library auto assignment", x)
		}
	}
	// Frozen view, scope and filters cannot be switched even on page one.
	for _, change := range []func(*model.TagCatalogInput){func(v *model.TagCatalogInput) { v.View = "all" }, func(v *model.TagCatalogInput) { v.LibraryID = "b" }, func(v *model.TagCatalogInput) { v.Category = "character" }, func(v *model.TagCatalogInput) { v.Query = "one" }, func(v *model.TagCatalogInput) { v.Sort = "usage" }} {
		v := tagInput("a")
		v.View = "display"
		v.Snapshot = display.Snapshot
		change(&v)
		if _, e := s.TagCatalogRelations(ctx, p, v); !errors.Is(e, model.ErrSnapshot) {
			t.Fatal(v, e)
		}
	}
	in = tagInput("a")
	in.View = "display"
	in.Snapshot = display.Snapshot
	if _, e := s.TagCatalogRelations(ctx, tagID("character", "10one"), in); !errors.Is(e, model.ErrSnapshot) {
		t.Fatal(e)
	}
}

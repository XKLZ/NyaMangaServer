package tagcatalog

import (
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestTagCatalogInputBoundaries(t *testing.T) {
	for _, in := range []model.TagCatalogInput{{}, {LibraryID: " "}, {LibraryID: "a", Category: "all"}, {LibraryID: "a", Page: 2}, {LibraryID: "a", PageSize: 101}, {LibraryID: "a", Sort: "name;DELETE"}, {LibraryID: "a", Order: "sideways"}, {LibraryID: "a", Partition: "related"}, {LibraryID: "a", Query: "\x00"}, {LibraryID: "a", Namespace: "\x00"}} {
		if _, e := Validate(in); e == nil {
			t.Fatalf("invalid input accepted: %+v", in)
		}
	}
	v, e := Validate(model.TagCatalogInput{LibraryID: "a"})
	if e != nil || v.Page != 1 || v.PageSize != 50 || v.Sort != "name" || v.Partition != "all" {
		t.Fatal(v, e)
	}
	for _, category := range []string{"parody", "character", "artist", "group", "content"} {
		if _, e = Validate(model.TagCatalogInput{LibraryID: "a", Category: category, Namespace: "unknown-future"}); e != nil {
			t.Fatal(e)
		}
	}
}

func TestTagCatalogWorksInputBoundaries(t *testing.T) {
	for _, in := range []model.TagCatalogInput{{}, {LibraryID: "a", Sort: "usage"}, {LibraryID: "a", Partition: "unclassified"}, {LibraryID: "a", RelatedTagID: "role"}, {LibraryID: "a", Category: "artist"}, {LibraryID: "a", Namespace: "artist"}, {LibraryID: "a", IncludeUnused: true}, {LibraryID: "a", Page: 2}, {LibraryID: "a", PageSize: 101}} {
		if _, e := ValidateWorks(in); e == nil {
			t.Fatalf("invalid works input accepted: %+v", in)
		}
	}
	v, e := ValidateWorks(model.TagCatalogInput{LibraryID: "a"})
	if e != nil || v.Page != 1 || v.PageSize != 50 || v.Sort != "name" || v.Order != "asc" {
		t.Fatal(v, e)
	}
	if _, e = ValidateWorks(model.TagCatalogInput{LibraryID: "a", Page: 2, Snapshot: "frozen", PageSize: 100, Order: "desc"}); e != nil {
		t.Fatal(e)
	}
}

func TestTagCatalogRelationViewScope(t *testing.T) {
	if _, e := Validate(model.TagCatalogInput{LibraryID: "a", View: "unknown"}); e == nil {
		t.Fatal("invalid view accepted")
	}
	svc := New(nil)
	for _, view := range []string{"all", "display", "unknown"} {
		in := model.TagCatalogInput{LibraryID: "a", View: view}
		if _, e := svc.Catalog(nil, in); e == nil {
			t.Fatal("catalog view accepted")
		}
		if _, e := svc.Detail(nil, "id", in); e == nil {
			t.Fatal("detail view accepted")
		}
		if _, e := svc.Editions(nil, "id", in); e == nil {
			t.Fatal("edition view accepted")
		}
		if _, e := svc.Works(nil, "id", in); e == nil {
			t.Fatal("work view accepted")
		}
	}
}

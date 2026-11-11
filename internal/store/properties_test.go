package store

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"testing"
)

func TestPropertiesAtomicSaveLegacyAndInheritance(t *testing.T) {
	s, _ := openTest(t)
	seedBook(t, s)
	ctx := context.Background()
	tags := `[{"namespace":"language","name":"japanese"},{"namespace":"language","name":"english"},{"namespace":"other","name":"full color"}]`
	if e := s.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(tags)}, "translated": {Op: "set", Value: json.RawMessage(`false`)}}); e != nil {
		t.Fatal(e)
	}
	d, e := s.MetadataDetails(ctx, "book")
	if e != nil || len(model.Languages(d.Properties.Languages)) != 2 || *d.Fields.Language != "english" || string(d.Properties.Translated.Value) != "false" {
		t.Fatal(d, e)
	}
	if e = s.ApplyOverrides(ctx, "book", 2, map[string]model.Override{"language": {Op: "set", Value: json.RawMessage(`"english"`)}}); !errors.Is(e, model.ErrMultipleLanguages) {
		t.Fatal(e)
	}
	if e = s.ApplyOverrides(ctx, "book", 2, map[string]model.Override{"languages": {Op: "set", Value: json.RawMessage(`["Japanese","ENGLISH","japanese"]`)}, "color_mode": {Op: "empty"}, "title": {Op: "set", Value: json.RawMessage(`"人工"`)}}); e != nil {
		t.Fatal(e)
	}
	d, e = s.MetadataDetails(ctx, "book")
	if e != nil || string(d.Properties.Languages.Value) != `["japanese","english"]` || d.Properties.ColorMode.State != "unknown" || d.Properties.ColorMode.Automatic.State != "normal" {
		t.Fatal(d, e)
	}
	if e = s.ApplyOverrides(ctx, "book", 2, map[string]model.Override{"languages": {Op: "empty"}}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	if e = s.ApplyOverrides(ctx, "book", 3, map[string]model.Override{"languages": {Op: "inherit"}, "color_mode": {Op: "inherit"}}); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.Read.QueryRow("SELECT count(*) FROM effective_edition_languages WHERE edition_id='book'").Scan(&n); e != nil || n != 2 {
		t.Fatal(n, e)
	}
	beforeSave, _ := catalogRevision(ctx, s.Read)
	if e = s.ApplyOverrides(ctx, "book", 4, map[string]model.Override{"translated": {Op: "set", Value: json.RawMessage(`false`)}}); e != nil {
		t.Fatal(e)
	}
	afterSave, _ := catalogRevision(ctx, s.Read)
	if beforeSave != afterSave {
		t.Fatal("same effective property save invalidated query")
	}
	before, _ := catalogRevision(ctx, s.Read)
	tx, _ := s.Write.BeginTx(ctx, nil)
	if e = projectProperties(ctx, tx, "book"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	after, _ := catalogRevision(ctx, s.Read)
	if before != after {
		t.Fatal("idempotent property projection invalidated query")
	}
}

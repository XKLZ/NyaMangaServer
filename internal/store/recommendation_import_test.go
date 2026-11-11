package store

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	pilot "nyamangaserver.local/server/internal/recommendationpilot"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolatedRecommendationImportAtomicAndEmptyOnly(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, e := Open(ctx, filepath.Join(root, "data"), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	book := pilot.LegacyBook{ID: 1, Title: "Anonymous", Original: "匿名", Pages: 8, Language: "english", Tags: [][2]string{{"language", "translated"}}}
	raw, _ := json.Marshal(map[string]any{"schema": "recommendation-books-v3", "books": []pilot.LegacyBook{book}})
	input := filepath.Join(root, "books.json")
	os.WriteFile(input, raw, 0600)
	var imageBytes bytes.Buffer
	jpeg.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil)
	name := strings.Repeat("a", 40) + ".jpg"
	covers := filepath.Join(root, "Covers")
	os.MkdirAll(filepath.Join(covers, "aa"), 0700)
	os.WriteFile(filepath.Join(covers, "aa", name), imageBytes.Bytes(), 0600)
	mapping := filepath.Join(root, "mapping.json")
	b, _ := json.Marshal(map[string]any{"books": []any{map[string]any{"id": 1, "title": book.Title, "title_jpn": book.Original, "cover_path": "/never/read/production/Covers/aa/" + name}}})
	os.WriteFile(mapping, b, 0600)
	// A transaction failure leaves neither partial identities nor new referenced files.
	if _, e = s.Write.Exec(`CREATE TRIGGER fail_snapshot BEFORE INSERT ON edition_sources BEGIN SELECT RAISE(ABORT,'anonymous fault'); END;`); e != nil {
		t.Fatal(e)
	}
	if _, e = pilot.Import(ctx, s, filepath.Join(root, "data"), input, mapping, covers); e == nil {
		t.Fatal("expected rollback")
	}
	if e = s.RecommendationSnapshotEmpty(ctx); e != nil {
		t.Fatal(e)
	}
	s.Write.Exec("DROP TRIGGER fail_snapshot")
	report, e := pilot.Import(ctx, s, filepath.Join(root, "data"), input, mapping, covers)
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Records) != 1 || report.Records[0].Asset == nil {
		t.Fatal(report)
	}
	for table, want := range map[string]int{"editions": 1, "files": 1, "works": 1, "accounts": 0, "sessions": 0, "jobs": 0, "series": 0, "field_overrides": 0, "recommendation_materials": 1} {
		var n int
		if e = s.Read.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil || n != want {
			t.Fatal(table, n, e)
		}
	}
	var state, provider, method string
	s.Read.QueryRow("SELECT file_state FROM files").Scan(&state)
	s.Read.QueryRow("SELECT provider FROM external_metadata").Scan(&provider)
	s.Read.QueryRow("SELECT match_method FROM edition_sources").Scan(&method)
	if state != "missing" || provider != "treec-validation" || method != "legacy_snapshot_unknown_ownership" {
		t.Fatal(state, provider, method)
	}
	if _, e = pilot.Import(ctx, s, filepath.Join(root, "data"), input, mapping, covers); e == nil {
		t.Fatal("accepted nonempty target")
	}
}

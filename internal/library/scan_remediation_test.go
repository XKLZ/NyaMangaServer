package library

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestScanPartialStagesRetryAndCacheReuse(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Pool.Close()
	s.Pool = comic.NewPoolWithBudget(comic.NewBudget(1))
	s.Start(ctx)
	job := scan(t, s, l)
	if job.ScanPartial != 2 || job.ScanBlocked != 1 || job.ScanWarnings != 2 {
		t.Fatal(job)
	}
	items, e := s.Items(ctx, job.ID, 1, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	var target model.Item
	for _, item := range items {
		if item.ScanResult != nil && item.ScanResult.Classification == "partial" {
			target = item
			break
		}
	}
	if target.BookID == nil || target.ScanResult.Stages["lookup"] != "completed" || len(target.ScanResult.Issues) != 1 || target.ScanResult.Issues[0].Resource != "estimated_memory_bytes" {
		t.Fatal(target)
	}
	b, e := s.Edition(ctx, *target.BookID)
	if e != nil || b.State != "available" || b.SHA1 == nil {
		t.Fatal(b, e)
	}
	hash := *b.SHA1
	s.Pool.Close()
	s.Pool = comic.NewPoolWithBudget(comic.NewBudget(comic.DefaultImageMemory))
	retry, e := s.RetryScan(ctx, job.ID, []int{target.Ordinal})
	if e != nil {
		t.Fatal(e)
	}
	done := wait(t, s, retry.ID)
	if done.Failed != 0 || done.ScanCompleted != 1 {
		t.Fatal(done)
	}
	again, e := s.Edition(ctx, b.ID)
	if e != nil || again.SHA1 == nil || *again.SHA1 != hash {
		t.Fatal(again, e)
	}
	path := s.cachePath(*again, 0)
	before, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	scan(t, s, l)
	after, e := os.Stat(path)
	if e != nil || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("valid cover regenerated", e)
	}
	old, e := s.Items(ctx, job.ID, 1, 100, false)
	if e != nil || old[target.Ordinal-1].State != "failed" {
		t.Fatal("history rewritten", old, e)
	}
}
func TestEncodingEquivalentOrderDoesNotInvalidatePositions(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.Editions(ctx, l.ID, "available", 1, 10)
	if e != nil || len(books) == 0 {
		t.Fatal(e)
	}
	b := books[0]
	page := 1
	cover, e := s.SetCover(ctx, b.ID, 0, model.CoverSelection{Mode: "page", PageIndex: &page, ContentRevision: b.ContentRevision, ParsingRevision: b.ParsingRevision})
	if e != nil {
		t.Fatal(e)
	}
	preview, e := s.PreviewEncoding(ctx, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	encoding := "gb18030"
	candidate := preview.Candidates[1]
	changed, e := s.SetEncoding(ctx, b.ID, model.EncodingUpdate{Expected: preview.Revision, ContentRevision: preview.ContentRevision, ParsingRevision: preview.ParsingRevision, Encoding: &encoding, DirectoryDigest: candidate.DirectoryDigest})
	if e != nil {
		t.Fatal(e)
	}
	if changed.ContentRevision != b.ContentRevision || changed.ParsingRevision != b.ParsingRevision {
		t.Fatal("equivalent order invalidated", changed)
	}
	selected, e := s.CoverSelection(ctx, b.ID)
	if e != nil || selected.State != "ready" || selected.Revision != cover.Revision {
		t.Fatal(selected, e)
	}
	duplicate, e := s.SetEncoding(ctx, b.ID, model.EncodingUpdate{Expected: changed.Revision, ContentRevision: changed.ContentRevision, ParsingRevision: changed.ParsingRevision, Encoding: &encoding, DirectoryDigest: candidate.DirectoryDigest})
	if e != nil || duplicate.Revision != changed.Revision {
		t.Fatal("duplicate setting mutated", duplicate, e)
	}
}

func TestLegacyParserSameOrderAndSelectedCoverRescan(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.Editions(ctx, l.ID, "available", 1, 10)
	if e != nil {
		t.Fatal(e)
	}
	b := books[0]
	db := s.store.(*store.Store)
	if _, e = db.Write.Exec("UPDATE files SET parser_version='natural-bmp-v2',directory_digest='',effective_encoding='',parsing_revision=1 WHERE edition_id=?", b.ID); e != nil {
		t.Fatal(e)
	}
	page := 1
	cover, e := s.SetCover(ctx, b.ID, 0, model.CoverSelection{Mode: "page", PageIndex: &page, ContentRevision: b.ContentRevision, ParsingRevision: 1})
	if e != nil {
		t.Fatal(e)
	}
	path := s.cachePath(b, 0)
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	scan(t, s, l)
	after, e := s.Edition(ctx, b.ID)
	if e != nil || after.ParsingRevision != 1 || after.ContentRevision != b.ContentRevision {
		t.Fatal(after, e)
	}
	current, e := s.CoverSelection(ctx, b.ID)
	if e != nil || current.State != "ready" || current.Revision != cover.Revision {
		t.Fatal(current, e)
	}
	if _, e = os.Stat(path); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("selected cover generated extra auto cache", e)
	}
}

func TestAmbiguousEncodingChoiceAndMultipleScanIssues(t *testing.T) {
	s, l, root := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, _ := s.Editions(ctx, l.ID, "available", 1, 10)
	b := books[0]
	raw, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, false)
	if e != nil {
		t.Fatal(e)
	}
	makeArchive := func(name, member string, broken bool) {
		var buf bytes.Buffer
		z := zip.NewWriter(&buf)
		w, e := z.CreateHeader(&zip.FileHeader{Name: member, NonUTF8: true, Method: zip.Store})
		if e != nil {
			t.Fatal(e)
		}
		w.Write(raw)
		z.Close()
		data := buf.Bytes()
		if broken {
			at := bytes.Index(data, raw)
			if at < 0 {
				t.Fatal("no payload")
			}
			data[at+len(raw)-1] ^= 1
		}
		if e = os.WriteFile(filepath.Join(root, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	makeArchive("ambiguous.cbz", string([]byte{0x82, 0xa0})+".png", false)
	makeArchive("bad-crc.cbz", "1.png", true)
	job := scan(t, s, l)
	items, e := s.Items(ctx, job.ID, 1, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	var ambiguous *model.Edition
	for _, item := range items {
		switch item.Path {
		case "ambiguous.cbz":
			ambiguous, e = s.Edition(ctx, *item.BookID)
			if e != nil || ambiguous.State != "needs_review" || item.ScanResult.Classification != "blocked" {
				t.Fatal(item, e)
			}
		case "bad-crc.cbz":
			if len(item.ScanResult.Issues) != 2 || item.ScanResult.Stages["cover"] != "failed" || item.ScanResult.Stages["lookup"] != "failed" || item.ScanResult.Classification != "partial" {
				t.Fatal(item.ScanResult)
			}
		}
	}
	if ambiguous == nil {
		t.Fatal("missing ambiguous fixture")
	}
	preview, e := s.PreviewEncoding(ctx, ambiguous.ID)
	if e != nil || !preview.RequiresChoice || preview.Candidates[0].Valid || !preview.Candidates[1].Valid || !preview.Candidates[2].Valid {
		t.Fatal(preview, e)
	}
	encoding := "shift_jis"
	after, e := s.SetEncoding(ctx, ambiguous.ID, model.EncodingUpdate{Expected: preview.Revision, ContentRevision: preview.ContentRevision, ParsingRevision: preview.ParsingRevision, Encoding: &encoding, DirectoryDigest: preview.Candidates[2].DirectoryDigest})
	if e != nil || after.State != "available" || after.ContentRevision != ambiguous.ContentRevision || after.EncodingOverride == nil || *after.EncodingOverride != encoding {
		t.Fatal(after, e)
	}
}

func TestLegacyParserTrueOrderChangeRequiresExplicitParse(t *testing.T) {
	s, l, root := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, _ := s.Editions(ctx, l.ID, "available", 1, 10)
	b := books[0]
	raw, _, e := s.Image(ctx, b.ID, b.ContentRevision, 1, false)
	if e != nil {
		t.Fatal(e)
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{"__MACOSX/01.png", "pages/01.png"} {
		w, _ := z.Create(name)
		w.Write(raw)
	}
	z.Close()
	path := filepath.Join(root, "old-parser.cbz")
	if e = os.WriteFile(path, buf.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	scan(t, s, l)
	all, _, _ := s.Editions(ctx, l.ID, "available", 1, 100)
	var old model.Edition
	for _, v := range all {
		if v.Path == "old-parser.cbz" {
			old = v
		}
	}
	db := s.store.(*store.Store)
	if _, e = db.Write.Exec("UPDATE files SET parser_version='natural-bmp-v2',directory_digest='',parsing_revision=1,page_count=2 WHERE edition_id=?", old.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Pages(ctx, old.ID, old.ContentRevision, 1, 10); !errors.Is(e, model.ErrParsingChanged) {
		t.Fatal("old coordinates applied to new directory", e)
	}
	// Explicit scan persists the new page order; equivalent legacy cases remain at 1.
	scan(t, s, l)
	after, e := s.Edition(ctx, old.ID)
	if e != nil || after.ParsingRevision != 2 || after.ContentRevision != old.ContentRevision {
		t.Fatal(after, e)
	}
	if _, e = s.Pages(ctx, old.ID, old.ContentRevision, 1, 10); e != nil {
		t.Fatal(e)
	}
}

func TestScanRejectsBrokenCoverCacheAndPreservesValidBytes(t *testing.T) {
	s, l, _ := setup(t)
	ctx := context.Background()
	s.Start(ctx)
	scan(t, s, l)
	books, _, e := s.Editions(ctx, l.ID, "available", 1, 10)
	if e != nil || len(books) == 0 {
		t.Fatal(e)
	}
	b := books[0]
	path := s.cachePath(b, 0)
	valid, e := os.ReadFile(path)
	if e != nil || !validScanCover(valid) {
		t.Fatal("fixture thumbnail", e)
	}
	for _, bad := range [][]byte{nil, valid[:len(valid)/2]} {
		if e = os.WriteFile(path, bad, 0600); e != nil {
			t.Fatal(e)
		}
		scan(t, s, l)
		raw, e := os.ReadFile(path)
		if e != nil || !validScanCover(raw) {
			t.Fatal("broken cache reused", e)
		}
	}
	before, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	// No source decode budget: success now depends on reusing the valid thumbnail.
	s.Pool.Close()
	s.Pool = comic.NewPoolWithBudget(comic.NewBudget(1))
	job := scan(t, s, l)
	items, e := s.Items(ctx, job.ID, 1, 100, false)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, item := range items {
		if item.BookID != nil && *item.BookID == b.ID {
			found = true
			if item.ScanResult == nil || item.ScanResult.Stages["cover"] != "completed" {
				t.Fatal(item)
			}
		}
	}
	after, e := os.Stat(path)
	if e != nil || !found || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("valid cache rewritten", e)
	}
}

package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func (s *Service) scan(ctx context.Context, id string) {
	release, leaseErr := s.healthIO(ctx)
	if leaseErr != nil {
		return
	}
	defer release()
	scopedContext, contextErr := s.store.JobContext(ctx, id)
	if contextErr != nil {
		return
	}
	ctx = scopedContext
	j, e := s.store.Claim(ctx, id)
	if e != nil {
		return
	}
	fail := func(e error) {
		if ctx.Err() == nil && !errors.Is(e, model.ErrStaleJob) {
			_ = s.store.Finish(ctx, *j, false, code(e))
		}
	}
	defer func() {
		if recover() != nil {
			fail(ErrUnavailable)
			s.Logger.Error("scan panic", "job_id", id)
		}
	}()
	l, e := s.store.Library(ctx, j.LibraryID)
	if e != nil {
		fail(e)
		return
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		fail(e)
		return
	}
	if j.Stage == "enumerating" {
		root, e := os.OpenRoot(r.Path)
		if e != nil {
			fail(ErrRoot)
			return
		}
		batch := []model.Item{}
		ordinal := 0
		e = fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return ErrRoot
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				if comic.IsSystemSidecar(path) {
					return fs.SkipDir
				}
				return nil
			}
			if comic.IsSystemSidecar(path) || sourceAppleDouble(root, path) {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".zip" && ext != ".cbz" {
				return nil
			}
			info, e := d.Info()
			if e != nil || !info.Mode().IsRegular() {
				return ErrRoot
			}
			ordinal++
			batch = append(batch, model.Item{Ordinal: ordinal, Path: path, Size: info.Size(), Mtime: info.ModTime().UnixNano()})
			if len(batch) >= 128 {
				e = s.store.StageEntries(ctx, *j, batch)
				batch = nil
				return e
			}
			return nil
		})
		root.Close()
		if e != nil {
			fail(e)
			return
		}
		if len(batch) > 0 {
			if e = s.store.StageEntries(ctx, *j, batch); e != nil {
				fail(e)
				return
			}
		}
		if _, e = s.checkedRoot(*l); e != nil {
			fail(e)
			return
		}
		if e = s.store.Freeze(ctx, *j); e != nil {
			fail(e)
			return
		}
	}
	for {
		items, e := s.store.Items(ctx, j.ID, 1, 64, true)
		if e != nil {
			fail(e)
			return
		}
		if len(items) == 0 {
			break
		}
		var wg sync.WaitGroup
		errs := make(chan error, len(items))
		for _, item := range items {
			select {
			case s.slots <- struct{}{}:
			case <-ctx.Done():
				wg.Wait()
				return
			}
			wg.Add(1)
			go func(item model.Item) {
				defer wg.Done()
				defer func() { <-s.slots }()
				errs <- s.process(ctx, *j, *l, r, item)
			}(item)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				fail(e)
				return
			}
		}
	}
	if _, e = s.checkedRoot(*l); e != nil {
		fail(e)
		return
	}
	// A retry freezes only selected members. It must never authorize a full
	// library missing-state update from that incomplete selection.
	if j.ParentID != nil {
		if e = s.store.Finish(ctx, *j, false, ""); e != nil {
			fail(e)
		}
		return
	}
	if e = s.store.Finalizing(ctx, *j); e != nil {
		fail(e)
		return
	}
	// Finalization re-enumerates all eligible paths and file identities. A changed
	// or incomplete directory never authorizes missing-state updates.
	root, e := os.OpenRoot(r.Path)
	if e != nil {
		fail(ErrRoot)
		return
	}
	excluded, e := s.store.ScanExclusions(ctx, l.ID)
	if e != nil {
		root.Close()
		fail(e)
		return
	}
	seen := map[string][2]int64{}
	e = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return ErrRoot
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if comic.IsSystemSidecar(p) {
				return fs.SkipDir
			}
			return nil
		}
		if comic.IsSystemSidecar(p) || sourceAppleDouble(root, p) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".zip" && ext != ".cbz" {
			return nil
		}
		v, e := d.Info()
		if e != nil || !v.Mode().IsRegular() {
			return ErrRoot
		}
		if excluded[p] {
			return nil
		}
		seen[p] = [2]int64{v.Size(), v.ModTime().UnixNano()}
		return nil
	})
	root.Close()
	if e != nil {
		fail(e)
		return
	}
	total := 0
	for page := 1; ; page++ {
		items, e := s.store.Items(ctx, j.ID, page, 100, false)
		if e != nil {
			fail(e)
			return
		}
		for _, v := range items {
			total++
			if value, ok := seen[v.Path]; !ok || value != [2]int64{v.Size, v.Mtime} {
				fail(comic.ErrChanged)
				return
			}
		}
		if len(items) < 100 {
			break
		}
	}
	if len(seen) != total {
		fail(comic.ErrChanged)
		return
	}
	if _, e = s.checkedRoot(*l); e != nil {
		fail(e)
		return
	}
	if total == 0 {
		_, existing, e := s.store.Editions(ctx, l.ID, "", 1, 1)
		if e != nil {
			fail(e)
			return
		}
		if existing > 0 {
			fail(Error("suspicious_empty_root"))
			return
		}
	}
	if e = s.store.Finish(ctx, *j, true, ""); e != nil {
		fail(e)
	}
}
func (s *Service) process(ctx context.Context, j model.Job, l model.Library, r config.Root, item model.Item) error {
	b, e := s.store.EditionAt(ctx, l.ID, item.Path)
	if e != nil && !errors.Is(e, model.ErrNotFound) {
		return e
	}
	inputParsing := int64(0)
	if b == nil {
		b = &model.Edition{LibraryID: l.ID, File: model.File{Path: item.Path, FilenameTitle: strings.TrimSuffix(filepath.Base(item.Path), filepath.Ext(item.Path)), ContentRevision: 1, FileParsing: model.FileParsing{ParsingRevision: 1, ParserVersion: comic.DirectoryVersion}}, Revision: 1}
		b.ID, e = auth.RandomToken(16)
		if e != nil {
			return e
		}
	} else {
		inputParsing = b.ParsingRevision
		if b.Size != item.Size || b.Mtime != item.Mtime {
			b.ContentRevision++
			b.SHA1 = nil
		}
	}
	previous := model.ScanResult{}
	_ = json.Unmarshal([]byte(item.Input), &previous)
	reuse := j.ParentID != nil && previous.ContentRevision == b.ContentRevision && previous.ParsingRevision == b.ParsingRevision && previous.EffectiveEncoding == effectiveEncoding(*b, l) && previous.ParserVersion == comic.DirectoryVersion
	result := &model.ScanResult{InputParsingRevision: inputParsing, InputParsingFingerprint: b.FileParsing.Fingerprint(), Classification: "completed", ContentRevision: b.ContentRevision, Stages: map[string]string{}, Issues: []model.ScanIssue{}}
	if j.ParentID != nil {
		result.SourceJobID = *j.ParentID
		result.SourceOrdinal = previous.SourceOrdinal
	}
	b.Size = item.Size
	b.Mtime = item.Mtime
	b.State = "available"
	b.Error = ""
	b.ScanResult = result
	issue := func(stage string, err error) {
		v := model.ScanIssue{Stage: stage, Code: code(err)}
		var limit *comic.LimitError
		if errors.As(err, &limit) {
			v.Resource = limit.Resource
			v.Actual = limit.Actual
			v.Limit = limit.Limit
		}
		result.Issues = append(result.Issues, v)
		result.Stages[stage] = "failed"
		if b.Error == "" {
			b.Error = v.Code
			if stage == "lookup" {
				b.Error = "lookup_" + b.Error
			}
		}
	}
	encoding := effectiveEncoding(*b, l)
	entries, e := s.Pool.Inspect(ctx, r.Path, item.Path, encoding, item.Size, item.Mtime)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b.State = "corrupt"
		if errors.Is(e, comic.ErrEncoding) || errors.Is(e, comic.ErrLimit) {
			b.State = "needs_review"
		}
		issue("directory", e)
		result.Classification = "blocked"
		result.Stages["cover"] = "not_started"
		result.Stages["lookup"] = "not_started"
		// Keep prior page count, digest and human positions until a successful parse.
	} else {
		digest := directoryDigest(entries)
		oldDigest := b.DirectoryDigest
		if inputParsing > 0 && oldDigest == "" && b.ParserVersion == "natural-bmp-v2" {
			legacy, err := s.Pool.InspectLegacyOrder(ctx, r.Path, item.Path, encoding, item.Size, item.Mtime)
			if err == nil {
				oldDigest = directoryDigest(legacy)
			}
		}
		if inputParsing > 0 && oldDigest != digest {
			b.ParsingRevision++
			reuse = false
		}
		b.ParsingRevision = max(1, b.ParsingRevision)
		b.ParserVersion = comic.DirectoryVersion
		b.DirectoryDigest = digest
		b.EffectiveEncoding = encoding
		b.PageCount = len(entries)
		result.Stages["directory"] = "completed"
		selection, err := s.store.CoverSelection(ctx, b.ID)
		if errors.Is(err, model.ErrNotFound) && inputParsing == 0 {
			selection = model.CoverSelection{Mode: "auto"}
			err = nil
		}
		if err != nil {
			return err
		}
		if selection.Mode != "auto" {
			result.Stages["cover"] = "preserved"
		} else {
			key := filepath.Base(s.cachePath(*b, 0))
			if raw, err := s.cache.Peek(key); err == nil && validScanCover(raw) {
				result.Stages["cover"] = "completed"
			} else {
				image, _, _, err := s.Pool.Image(ctx, r.Path, item.Path, item.Size, item.Mtime, entries[0].Entry, true)
				if err == nil {
					err = s.writeCache(*b, 0, image)
					if err != nil {
						err = Error("cover_cache_failed")
					}
				}
				if err != nil {
					issue("cover", err)
				} else {
					result.Stages["cover"] = "completed"
				}
			}
		}
		if reuse && previous.Stages["lookup"] == "completed" && b.SHA1 != nil {
			result.Stages["lookup"] = "completed"
		} else {
			hash, _, err := s.Pool.LookupSHA1(ctx, r.Path, item.Path, encoding, item.Size, item.Mtime)
			if err != nil {
				b.SHA1 = nil
				issue("lookup", err)
			} else {
				b.SHA1 = &hash
				b.RepresentativeAlgorithm = comic.RepresentativeAlgorithm
				result.Stages["lookup"] = "completed"
			}
		}
		if len(result.Issues) > 0 {
			result.Classification = "partial"
			result.WarningCount = len(result.Issues)
		}
	}
	result.ParsingRevision = b.ParsingRevision
	result.ParserVersion = b.ParserVersion
	result.DirectoryDigest = b.DirectoryDigest
	result.EffectiveEncoding = encoding
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, e = s.checkedRoot(l); e != nil {
		return e
	}
	if s.BeforePublish != nil {
		s.BeforePublish(ctx, j, item)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return s.store.Publish(ctx, j, item, *b)
}

func sourceAppleDouble(root *os.Root, name string) bool {
	if !strings.HasPrefix(filepath.Base(name), "._") {
		return false
	}
	f, e := root.Open(name)
	if e != nil {
		return false
	}
	defer f.Close()
	var header [26]byte
	if _, e = io.ReadFull(f, header[:]); e != nil {
		return false
	}
	return comic.AppleDoubleHeader(header[:])
}

// Validate only the bounded derived thumbnail, never the source page on a hit.
// DecodeConfig caps allocation; full PNG decode detects truncated data/bad CRC.
func validScanCover(raw []byte) bool {
	if len(raw) == 0 || len(raw) > 8<<20 {
		return false
	}
	cfg, e := png.DecodeConfig(bytes.NewReader(raw))
	if e != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 320 || cfg.Height > 480 {
		return false
	}
	_, e = png.Decode(bytes.NewReader(raw))
	return e == nil
}

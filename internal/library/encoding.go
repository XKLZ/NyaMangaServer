package library

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
)

func effectiveEncoding(b model.Edition, l model.Library) string {
	if b.EncodingOverride != nil {
		return *b.EncodingOverride
	}
	return l.Encoding
}
func directoryDigest(entries []comic.Entry) string {
	// Physical ZIP member identity/order is stable across equivalent decodings.
	ids := make([]int, len(entries))
	for i, e := range entries {
		ids[i] = e.Entry
	}
	raw, _ := json.Marshal(ids)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (s *Service) PreviewEncoding(ctx context.Context, id string) (model.EncodingPreview, error) {
	out := model.EncodingPreview{EditionID: id, Candidates: []model.EncodingCandidate{}}
	if e := s.store.ManagementGuard(ctx, id); e != nil {
		return out, e
	}
	b, e := s.store.Edition(ctx, id)
	if e != nil {
		return out, e
	}
	l, e := s.store.Library(ctx, b.LibraryID)
	if e != nil {
		return out, e
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		return out, e
	}
	out.Revision = b.Revision
	out.ContentRevision = b.ContentRevision
	out.ParsingRevision = b.ParsingRevision
	out.InheritedEncoding = l.Encoding
	valid := 0
	for _, encoding := range []string{"utf8", "gb18030", "shift_jis"} {
		v := model.EncodingCandidate{Encoding: encoding, Sample: []model.Page{}}
		entries, e := s.Pool.Inspect(ctx, r.Path, b.Path, encoding, b.Size, b.Mtime)
		if e != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			v.Error = code(e)
		} else {
			v.Valid = true
			valid++
			v.PageCount = len(entries)
			v.DirectoryDigest = directoryDigest(entries)
			for _, p := range entries[:min(len(entries), 8)] {
				v.Sample = append(v.Sample, model.Page{Index: p.Index, Entry: p.Entry, Name: p.Name, MediaType: p.MediaType})
			}
		}
		out.Candidates = append(out.Candidates, v)
	}
	out.RequiresChoice = valid > 1
	return out, nil
}
func (s *Service) SetEncoding(ctx context.Context, id string, in model.EncodingUpdate) (*model.Edition, error) {
	if in.Expected < 1 || in.ContentRevision < 1 || in.ParsingRevision < 1 {
		return nil, model.ErrInput
	}
	preview, e := s.PreviewEncoding(ctx, id)
	if e != nil {
		return nil, e
	}
	if preview.Revision != in.Expected || preview.ContentRevision != in.ContentRevision || preview.ParsingRevision != in.ParsingRevision {
		return nil, model.ErrConflict
	}
	encoding := preview.InheritedEncoding
	if in.Encoding != nil {
		encoding = *in.Encoding
	}
	for _, candidate := range preview.Candidates {
		if candidate.Encoding == encoding && candidate.Valid {
			b, e := s.store.Edition(ctx, id)
			if e != nil {
				return nil, e
			}
			l, e := s.store.Library(ctx, b.LibraryID)
			if e != nil {
				return nil, e
			}
			r, e := s.checkedRoot(*l)
			if e != nil {
				return nil, e
			}
			oldDigest := b.DirectoryDigest
			if oldDigest == "" {
				var old []comic.Entry
				if b.ParserVersion == "natural-bmp-v2" {
					old, e = s.Pool.InspectLegacyOrder(ctx, r.Path, b.Path, effectiveEncoding(*b, *l), b.Size, b.Mtime)
				} else {
					old, e = s.Pool.Inspect(ctx, r.Path, b.Path, effectiveEncoding(*b, *l), b.Size, b.Mtime)
				}
				if e == nil {
					oldDigest = directoryDigest(old)
				}
			}
			revision := b.ParsingRevision
			if oldDigest != candidate.DirectoryDigest {
				revision++
			}
			return s.store.SetFileEncoding(ctx, id, in, model.FileParsing{ParsingRevision: revision, ParserVersion: comic.DirectoryVersion, EffectiveEncoding: encoding, DirectoryDigest: candidate.DirectoryDigest}, candidate.PageCount)
		}
	}
	return nil, model.ErrInput
}

// Legacy records can be read without a rescan when physical page order is
// provably unchanged. A real parser-induced change must be explicitly persisted
// before old reading coordinates can address the new directory.
func (s *Service) ensurePageOrder(ctx context.Context, b model.Edition, l model.Library, root string, entries []comic.Entry) error {
	digest := directoryDigest(entries)
	if b.DirectoryDigest != "" {
		if b.DirectoryDigest != digest {
			return model.ErrParsingChanged
		}
		return nil
	}
	if b.ParserVersion == "natural-bmp-v2" {
		old, e := s.Pool.InspectLegacyOrder(ctx, root, b.Path, effectiveEncoding(b, l), b.Size, b.Mtime)
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return model.ErrParsingChanged
		}
		if directoryDigest(old) != digest {
			return model.ErrParsingChanged
		}
	}
	return nil
}

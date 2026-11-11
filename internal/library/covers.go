package library

import (
	"context"
	"crypto/sha256"
	"fmt"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/comic"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
)

func (s *Service) CoverSelection(ctx context.Context, id string) (model.CoverSelection, error) {
	return s.store.CoverSelection(ctx, id)
}
func (s *Service) SetCover(ctx context.Context, id string, expected int64, v model.CoverSelection) (model.CoverSelection, error) {
	if v.Mode != "auto" && v.Mode != "page" {
		return v, model.ErrInput
	}
	if v.Mode == "auto" {
		v.AssetID = nil
		v.PageIndex = nil
	}
	if v.Mode == "page" {
		if v.PageIndex == nil {
			return v, model.ErrInput
		}
		entries, e := s.directory(ctx, id, v.ContentRevision)
		if e != nil {
			return v, e
		}
		if *v.PageIndex < 1 || *v.PageIndex > len(entries) {
			return v, model.ErrInput
		}
		p := entries[*v.PageIndex-1]
		v.EntryIndex = &p.Entry
		v.EntryName = &p.Name
		v.DirectoryVersion = comic.DirectoryVersion
		b, e := s.store.Edition(ctx, id)
		if e != nil {
			return v, e
		}
		if v.ParsingRevision == 0 {
			v.ParsingRevision = 1
		}
		if v.ParsingRevision != b.ParsingRevision {
			return v, model.ErrConflict
		}
	}
	return s.store.SetCover(ctx, id, expected, v, nil)
}
func (s *Service) UploadCover(ctx context.Context, id string, expected int64, raw []byte) (model.CoverSelection, error) {
	release, err := s.healthIO(ctx)
	if err != nil {
		return model.CoverSelection{}, err
	}
	defer release()
	data, e := s.Pool.NormalizeUpload(ctx, raw)
	if e != nil {
		return model.CoverSelection{}, e
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	aid, e := model.NewID()
	if e != nil {
		return model.CoverSelection{}, e
	}
	key := filepath.Join("assets", "covers", digest[:2], aid+".png")
	path := filepath.Join(s.Config.DataDir, key)
	if e = appdata.NoSymlinks(path); e != nil {
		return model.CoverSelection{}, e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return model.CoverSelection{}, e
	}
	f, e := os.CreateTemp(filepath.Dir(path), "pending-")
	if e != nil {
		return model.CoverSelection{}, e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return model.CoverSelection{}, e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return model.CoverSelection{}, e
	}
	if e = f.Close(); e != nil {
		return model.CoverSelection{}, e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return model.CoverSelection{}, e
	}
	v, e := s.store.SetCover(ctx, id, expected, model.CoverSelection{Mode: "asset"}, &model.Asset{ID: aid, Key: filepath.ToSlash(key), Digest: digest, Size: int64(len(data))})
	if e != nil {
		_ = os.Remove(path)
	}
	return v, e
}
func (s *Service) RebuildCover(ctx context.Context, id string, content int64) error {
	release, err := s.healthIO(ctx)
	if err != nil {
		return err
	}
	defer release()
	b, e := s.store.Edition(ctx, id)
	if e != nil {
		return e
	}
	if b.ContentRevision != content {
		return model.ErrConflict
	}
	v, e := s.store.CoverSelection(ctx, id)
	if e != nil {
		return e
	}
	_ = s.cache.Remove(filepath.Base(s.selectedCachePath(*b, v)))
	_, _, e = s.Image(ctx, id, content, 1, true)
	return e
}
func (s *Service) CreateCoverJob(ctx context.Context, id string, expected int64) (*model.Job, error) {
	return s.store.CreateCoverJob(ctx, id, expected)
}
func (s *Service) selectedCachePath(b model.Edition, v model.CoverSelection) string {
	name := model.SelectedCoverCacheName(b.ID, b.ContentRevision, v.ParsingRevision, v.Revision, v.Mode)
	if v.Mode == "auto" {
		name = model.CoverCacheName(b.ID, b.ContentRevision, b.ParsingRevision, 0)
	}
	return filepath.Join(s.cacheDir, name[:2], name)
}

func (s *Service) ClearCovers() error {
	release, e := s.healthIO(context.Background())
	if e != nil {
		return e
	}
	defer release()
	return s.cache.Clear()
}

func (s *Service) RemoveCover(name string) error {
	release, e := s.healthIO(context.Background())
	if e != nil {
		return e
	}
	defer release()
	return s.cache.Remove(name)
}

// Called only by maintenance under its health IO lease and writer transaction.
// Do not acquire another health lease or query SQL from this callback.
func (s *Service) RemoveCoverIfUnchanged(name string, size, mtime int64) error {
	return s.cache.RemoveIfUnchanged(name, size, mtime)
}

func (s *Service) healthIO(ctx context.Context) (func(), error) {
	if r, ok := s.store.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		return r.BeginHealthIO(ctx)
	}
	return func() {}, nil
}

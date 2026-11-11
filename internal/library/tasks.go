package library

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"os"
	"time"
)

func (s *Service) Control(ctx context.Context, id, action string) error {
	if e := s.store.ControlJob(ctx, id, action, ""); e != nil {
		return e
	}
	if action != "resume" {
		s.mu.Lock()
		for _, v := range s.active {
			if v.id == id {
				v.cancel()
			}
		}
		s.mu.Unlock()
	}
	s.Wake()
	return nil
}
func (s *Service) RetryScan(ctx context.Context, parent string, ordinals []int) (*model.Job, error) {
	return s.RetryScanWithID(ctx, parent, ordinals, "")
}

// RetryScanWithID lets the import orchestrator materialize a previously reserved child.
func (s *Service) RetryScanWithID(ctx context.Context, parent string, ordinals []int, reserved string) (*model.Job, error) {
	j, e := s.store.Job(ctx, parent)
	if e != nil {
		return nil, e
	}
	if j.Type != "scan" || (j.State != "failed" && j.State != "completed" && j.State != "cancelled") {
		return nil, model.ErrConflict
	}
	l, e := s.store.Library(ctx, j.LibraryID)
	if e != nil {
		return nil, e
	}
	r, e := s.checkedRoot(*l)
	if e != nil {
		return nil, e
	}
	root, e := os.OpenRoot(r.Path)
	if e != nil {
		return nil, ErrRoot
	}
	defer root.Close()
	selection := map[int]bool{}
	for _, v := range ordinals {
		if v < 1 || selection[v] {
			return nil, model.ErrInput
		}
		selection[v] = true
	}
	var items []model.Item
	matched := 0
	for page := 1; ; page++ {
		batch, e := s.store.Items(ctx, parent, page, 100, false)
		if e != nil {
			return nil, e
		}
		for _, v := range batch {
			selected := selection[v.Ordinal]
			if len(selection) == 0 {
				selected = v.State == "failed" || v.State == "deferred"
			}
			if !selected {
				continue
			}
			matched++
			if v.State == "completed" {
				return nil, model.ErrInput
			}
			info, e := root.Stat(v.Path)
			if e != nil || !info.Mode().IsRegular() {
				return nil, ErrUnavailable
			}
			if v.ScanResult != nil {
				if (info.Size() != v.Size || info.ModTime().UnixNano() != v.Mtime) && v.ScanResult.Stages["directory"] == "completed" {
					return nil, model.ErrConflict
				}
				v.ScanResult.SourceOrdinal = v.Ordinal
				if v.BookID != nil {
					b, e := s.store.Edition(ctx, *v.BookID)
					if e != nil {
						return nil, e
					}
					if v.ScanResult.Stages["directory"] == "completed" && (b.ParsingRevision != v.ScanResult.ParsingRevision || effectiveEncoding(*b, *l) != v.ScanResult.EffectiveEncoding) {
						return nil, model.ErrConflict
					}
				}
			}
			v.Size = info.Size()
			v.Mtime = info.ModTime().UnixNano()
			items = append(items, v)
		}
		if len(batch) < 100 {
			break
		}
	}
	if len(items) == 0 || len(items) > 1000 || (len(selection) > 0 && matched != len(selection)) {
		return nil, model.ErrInput
	}
	id := reserved
	if id == "" {
		id, e = model.NewID()
		if e != nil {
			return nil, e
		}
	}
	next, e := s.store.CreateScanRetry(ctx, id, parent, l.ID, items)
	if e == nil {
		s.Wake()
	}
	return next, e
}

// Drain cancels local IO and waits for its ownership to be released. Durable states
// are changed by the caller first, so no old worker can publish after this barrier.
func (s *Service) Drain(ctx context.Context) error {
	for {
		s.mu.Lock()
		active := map[string]activeScan{}
		for k, v := range s.active {
			active[k] = v
		}
		s.mu.Unlock()
		n := 0
		for _, v := range active {
			j, e := s.store.Job(ctx, v.id)
			if e != nil || j.State == "paused" || j.State == "cancelled" {
				v.cancel()
				n++
			}
		}
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

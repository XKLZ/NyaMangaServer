package recommendation

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
)

const SeriesExecutionVersion = "series-signals-v1"

type SeriesSignal struct {
	A      string          `json:"a"`
	B      string          `json:"b"`
	Key    string          `json:"key"`
	Signal json.RawMessage `json:"signal"`
}
type SeriesEvidenceBlock struct {
	Key        string `json:"key"`
	StorageKey string `json:"storage_key"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Records    int    `json:"records"`
}
type SeriesCheckpoint struct {
	Signals  []SeriesSignal             `json:"signals"`
	Routes   map[string]json.RawMessage `json:"routes,omitempty"`
	More     bool                       `json:"more"`
	Computed int64                      `json:"computed_pairs"`
	Reused   int64                      `json:"reused_pairs"`
}
type SeriesIncrementalRepository interface {
	BeginSeriesEvidence(context.Context, model.Job, map[string]string) ([]SeriesEvidenceBlock, error)
	SaveSeriesEvidence(context.Context, model.Job, SeriesEvidenceBlock, int64, int64) error
	CompleteSeriesEvidence(context.Context, model.Job) error
}

func (s *Service) seriesBlockPath(key string) (string, error) {
	if !strings.HasPrefix(key, "indexes/recommendations/series/") || strings.Contains(key, "..") || filepath.IsAbs(key) {
		return "", model.ErrInput
	}
	p := filepath.Join(s.data, filepath.FromSlash(key))
	if e := appdata.NoSymlinks(p); e != nil {
		return "", e
	}
	return p, nil
}
func (s *Service) readSeriesBlock(ref SeriesEvidenceBlock) (SeriesCheckpoint, error) {
	var out SeriesCheckpoint
	p, e := s.seriesBlockPath(ref.StorageKey)
	if e != nil {
		return out, e
	}
	if ref.Size <= 0 || ref.Size > 8<<20 {
		return out, model.ErrInput
	}
	f, e := os.Open(p)
	if e != nil {
		return out, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, ref.Size+1))
	if e != nil || int64(len(raw)) != ref.Size {
		return out, model.ErrInput
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != ref.SHA256 {
		return out, model.ErrInput
	}
	z, e := gzip.NewReader(bytes.NewReader(raw))
	if e != nil {
		return out, e
	}
	defer z.Close()
	plain, e := io.ReadAll(io.LimitReader(z, 16<<20+1))
	if e != nil || len(plain) > 16<<20 {
		return out, model.ErrInput
	}
	e = json.Unmarshal(plain, &out)
	if len(out.Signals) > 512 {
		return out, model.ErrInput
	}
	return out, e
}
func (s *Service) writeSeriesBlock(c SeriesCheckpoint) (SeriesEvidenceBlock, error) {
	var ref SeriesEvidenceBlock
	raw, e := json.Marshal(c)
	if e != nil {
		return ref, e
	}
	if len(raw) > 16<<20 {
		return ref, model.ErrCapacity
	}
	var packed bytes.Buffer
	z := gzip.NewWriter(&packed)
	if _, e = z.Write(raw); e != nil {
		return ref, e
	}
	if e = z.Close(); e != nil {
		return ref, e
	}
	sum := sha256.Sum256(packed.Bytes())
	key := hex.EncodeToString(sum[:])
	ref = SeriesEvidenceBlock{Key: key, StorageKey: "indexes/recommendations/series/" + key[:2] + "/" + key + ".json.gz", SHA256: key, Size: int64(packed.Len()), Records: len(c.Signals)}
	p, e := s.seriesBlockPath(ref.StorageKey)
	if e != nil {
		return ref, e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return ref, e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".block-")
	if e != nil {
		return ref, e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(packed.Bytes())
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return ref, e
	}
	e = os.Rename(f.Name(), p)
	return ref, e
}
func (s *Service) restoreSeriesEvidence(ctx context.Context, j model.Job, key string, profiles map[string]string) error {
	d, ok := s.repo.(SeriesIncrementalRepository)
	if !ok {
		return nil
	}
	refs, e := d.BeginSeriesEvidence(ctx, j, profiles)
	if e != nil {
		return e
	}
	for _, ref := range refs {
		c, err := s.readSeriesBlock(ref)
		if err != nil {
			continue
		}
		var out map[string]any
		if _, e = s.callFeature(ctx, model.EnhancementSeries, "series_restore", map[string]any{"pool_key": key, "signals": nonNilSeriesSignals(c.Signals), "routes": nonNilSeriesRoutes(c.Routes)}, &out); e != nil {
			return e
		}
		if e = s.checkpointSeries(ctx, j, key); e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) checkpointSeries(ctx context.Context, j model.Job, key string) error {
	d, ok := s.repo.(SeriesIncrementalRepository)
	if !ok {
		return nil
	}
	for {
		var c SeriesCheckpoint
		if _, e := s.callFeature(ctx, model.EnhancementSeries, "series_checkpoint", map[string]any{"pool_key": key}, &c); e != nil {
			return e
		}
		if len(c.Signals) > 512 {
			return model.ErrInput
		}
		ref, e := s.writeSeriesBlock(c)
		if e != nil {
			return e
		}
		if e = d.SaveSeriesEvidence(ctx, j, ref, c.Computed, c.Reused); e != nil {
			return e
		}
		if !c.More {
			return nil
		}
	}
}

func (s *Service) retireSeriesBlocks(ctx context.Context) {
	d, ok := s.repo.(interface {
		SeriesEvidenceKeys(context.Context) (map[string]bool, error)
	})
	if !ok {
		return
	}
	keep, e := d.SeriesEvidenceKeys(ctx)
	if e != nil {
		return
	}
	root := filepath.Join(s.data, "indexes", "recommendations", "series")
	if appdata.NoSymlinks(root) != nil {
		return
	}
	_ = filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if len(name) != 72 || !strings.HasSuffix(name, ".json.gz") {
			return nil
		}
		if _, e := hex.DecodeString(name[:64]); e != nil {
			return nil
		}
		relative, e := filepath.Rel(s.data, p)
		if e == nil && !keep[filepath.ToSlash(relative)] {
			_ = os.Remove(p)
		}
		return nil
	})
}
func (s *Service) SeriesStatus(ctx context.Context, id string) (map[string]any, error) {
	j, e := s.repo.Job(ctx, id)
	if e != nil {
		return nil, e
	}
	if j.Type != "series_recommendation" {
		return nil, model.ErrInput
	}
	if d, ok := s.repo.(interface {
		SeriesEvidenceStats(context.Context, string) (map[string]any, error)
	}); ok {
		return d.SeriesEvidenceStats(ctx, id)
	}
	return map[string]any{}, nil
}

// Older checkpoints omit empty maps. The worker wire always uses collections,
// never JSON null; Python also accepts historical nullable collections.
func nonNilSeriesSignals(v []SeriesSignal) []SeriesSignal {
	if v == nil {
		return []SeriesSignal{}
	}
	return v
}
func nonNilSeriesRoutes(v map[string]json.RawMessage) map[string]json.RawMessage {
	if v == nil {
		return map[string]json.RawMessage{}
	}
	return v
}

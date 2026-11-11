// Package recommendationpilot prepares an explicitly isolated, offline validation snapshot.
// It never reads source archives or imports credentials or original organization.
package recommendationpilot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
)

const LibraryID = "treec-validation"
const CookieName = "nyamanga_recommendation_pilot"

type LegacyBook struct {
	ID         int64       `json:"id"`
	Title      string      `json:"title"`
	Original   string      `json:"title_jpn"`
	Pages      int         `json:"pages"`
	Language   string      `json:"language_code"`
	Tags       [][2]string `json:"tags"`
	Translated int         `json:"is_translated"`
	Color      int         `json:"is_full_color"`
	Censorship string      `json:"censorship"`
	Ads        int         `json:"has_extraneous_ads"`
}
type Record struct {
	SourceID    string         `json:"source_id"`
	EditionID   string         `json:"edition_id"`
	WorkID      string         `json:"work_id"`
	Metadata    model.Metadata `json:"metadata"`
	Original    LegacyBook     `json:"legacy_unknown"`
	Asset       *model.Asset   `json:"asset,omitempty"`
	CoverStatus string         `json:"cover_status"`
	InputHash   string         `json:"input_sha256"`
}
type Report struct {
	Version     int      `json:"version"`
	InputHash   string   `json:"input_sha256"`
	MappingHash string   `json:"mapping_sha256"`
	Records     []Record `json:"records"`
	Notice      string   `json:"notice"`
}
type Repository interface {
	ImportRecommendationSnapshot(context.Context, []Record, string) error
	RecommendationSnapshotEmpty(context.Context) error
}

func EditionID(id int64) string { return "pilot-edition-" + strconv.FormatInt(id, 10) }
func WorkID(id int64) string    { return "pilot-work-" + strconv.FormatInt(id, 10) }
func read(path string, limit int64) ([]byte, error) {
	if e := appdata.NoSymlinks(path); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid snapshot file")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
func Hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

var coverName = regexp.MustCompile(`^[0-9a-f]{40}\.jpg$`)

// CoverPath ignores the original absolute prefix; only this validated suffix is resolved.
func CoverPath(root, legacy string) (string, error) {
	parts := strings.Split(filepath.ToSlash(legacy), "/")
	for _, p := range parts {
		if p == ".." {
			return "", model.ErrInput
		}
	}
	if len(parts) < 3 {
		return "", model.ErrInput
	}
	p := parts[len(parts)-3:]
	if p[0] != "Covers" || !coverName.MatchString(p[2]) || p[1] != p[2][:2] {
		return "", model.ErrInput
	}
	path := filepath.Join(root, p[1], p[2])
	return path, appdata.NoSymlinks(path)
}
func validBook(b LegacyBook) error {
	if b.ID <= 0 || b.Pages < 0 || b.Pages > 10000 {
		return model.ErrInput
	}
	for k, v := range map[string]string{"title": b.Title, "title_original": b.Original, "language": b.Language} {
		if v != "" {
			raw, _ := json.Marshal(v)
			if e := model.ValidateField(k, raw); e != nil {
				return e
			}
		}
	}
	tags := make([]model.TagName, 0, len(b.Tags))
	for _, v := range b.Tags {
		tags = append(tags, model.TagName{Namespace: v[0], Name: v[1]})
	}
	raw, _ := json.Marshal(tags)
	return model.ValidateField("tags", raw)
}
func ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Import preflights all records, then atomically commits business data. Only newly
// created asset files are removed on failure; existing files are never overwritten.
func Import(ctx context.Context, repo Repository, dataDir, input, mapping, covers string) (report Report, err error) {
	if err = repo.RecommendationSnapshotEmpty(ctx); err != nil {
		return
	}
	raw, e := read(input, 16<<20)
	if e != nil {
		return report, e
	}
	mapped, e := read(mapping, 32<<20)
	if e != nil {
		return report, e
	}
	var pack struct {
		Schema string       `json:"schema"`
		Books  []LegacyBook `json:"books"`
	}
	if e = json.Unmarshal(raw, &pack); e != nil || pack.Schema != "recommendation-books-v3" || len(pack.Books) == 0 || len(pack.Books) > 10000 {
		return report, model.ErrInput
	}
	var coverMap struct {
		Books []struct {
			ID        int64  `json:"id"`
			CoverPath string `json:"cover_path"`
			Title     string `json:"title"`
			Original  string `json:"title_jpn"`
		} `json:"books"`
	}
	if e = json.Unmarshal(mapped, &coverMap); e != nil {
		return report, e
	}
	type coverEntry struct{ Path, Title, Original string }
	paths := map[int64]coverEntry{}
	for _, v := range coverMap.Books {
		if _, ok := paths[v.ID]; ok {
			return report, model.ErrInput
		}
		paths[v.ID] = coverEntry{v.CoverPath, v.Title, v.Original}
	}
	report = Report{Version: 1, InputHash: Hash(raw), MappingHash: Hash(mapped), Records: []Record{}, Notice: "Offline validation snapshot; all Files missing, legacy values have unknown ownership; no source archive, account, task, group or Series imported."}
	created := []string{}
	defer func() {
		if err != nil {
			for _, p := range created {
				_ = os.Remove(p)
			}
		}
	}()
	seen := map[int64]bool{}
	for _, b := range pack.Books {
		if e = ctx.Err(); e != nil {
			return report, e
		}
		if seen[b.ID] {
			return report, model.ErrInput
		}
		seen[b.ID] = true
		if e = validBook(b); e != nil {
			return report, fmt.Errorf("invalid snapshot record: %w", e)
		}
		entry, ok := paths[b.ID]
		if !ok || entry.Title != b.Title || entry.Original != b.Original {
			return report, errors.New("snapshot mapping identity mismatch")
		}
		rec := Record{SourceID: strconv.FormatInt(b.ID, 10), EditionID: EditionID(b.ID), WorkID: WorkID(b.ID), Original: b, CoverStatus: "missing"}
		rec.Metadata = model.Metadata{Title: ptr(b.Title), TitleOriginal: ptr(b.Original), Language: ptr(b.Language), Tags: []model.TagName{}}
		for _, tag := range b.Tags {
			rec.Metadata.Tags = append(rec.Metadata.Tags, model.TagName{Namespace: tag[0], Name: tag[1]})
		}
		identity, _ := json.Marshal(b)
		rec.InputHash = Hash(identity)
		if entry.Path != "" {
			source, ce := CoverPath(covers, entry.Path)
			if ce != nil {
				return report, ce
			}
			imageBytes, ce := read(source, 10<<20)
			if ce == nil {
				cfg, format, de := image.DecodeConfig(bytes.NewReader(imageBytes))
				if de != nil || format != "jpeg" || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
					return report, errors.New("invalid snapshot cover")
				}
				hash := Hash(imageBytes)
				key := filepath.ToSlash(filepath.Join("assets", "recommendation-validation", hash[:2], hash+".jpg"))
				target := filepath.Join(dataDir, filepath.FromSlash(key))
				if ce = appdata.NoSymlinks(target); ce != nil {
					return report, ce
				}
				if ce = os.MkdirAll(filepath.Dir(target), 0700); ce != nil {
					return report, ce
				}
				added, ce := publishMaterial(target, imageBytes)
				if ce != nil {
					return report, ce
				}
				if added {
					created = append(created, target)
				}
				rec.Asset = &model.Asset{ID: "pilot-asset-" + hash, Key: key, Digest: hash, Size: int64(len(imageBytes))}
				rec.CoverStatus = "registered"
			} else if !errors.Is(ce, os.ErrNotExist) {
				return report, ce
			}
		}
		report.Records = append(report.Records, rec)
	}
	err = repo.ImportRecommendationSnapshot(ctx, report.Records, report.InputHash)
	return
}

// publishMaterial exposes only a fully synced file. Link is atomic and refuses
// overwrite; stale private temporary files cannot become referenced assets.
func publishMaterial(target string, data []byte) (bool, error) {
	if e := appdata.NoSymlinks(target); e != nil {
		return false, e
	}
	if existing, e := read(target, 10<<20); e == nil {
		if Hash(existing) != Hash(data) {
			return false, errors.New("asset conflict")
		}
		return false, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return false, e
	}
	f, e := os.CreateTemp(filepath.Dir(target), ".snapshot-material-")
	if e != nil {
		return false, e
	}
	temporary := f.Name()
	defer os.Remove(temporary)
	_, w := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if e = errors.Join(w, syncErr, closeErr); e != nil {
		return false, e
	}
	if e = os.Link(temporary, target); e != nil {
		if errors.Is(e, os.ErrExist) {
			existing, re := read(target, 10<<20)
			if re == nil && Hash(existing) == Hash(data) {
				return false, nil
			}
		}
		return false, e
	}
	return true, nil
}

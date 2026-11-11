package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strings"
)

// This explicit offline manifest is produced by the independent converter. Keys
// are relative to the manifest directory, never supplied through an HTTP API.
// This accepts a prepared directory/JSON manifest, not conversion-archive.zip.
type MigrationOriginalManifest struct {
	Format int                  `json:"format_version"`
	Files  []model.InstanceFile `json:"files"`
}
type verifiedOriginal struct {
	Asset model.StorageAsset `json:"asset"`
	File  model.InstanceFile `json:"file"`
	Mtime int64              `json:"mtime_ns"`
}
type originalReceipt struct {
	DatabaseDevice uint64                      `json:"database_device"`
	DatabaseInode  uint64                      `json:"database_inode"`
	Format         int                         `json:"format_version"`
	Archive        string                      `json:"archive_manifest"`
	Digest         string                      `json:"manifest_sha256"`
	Files          map[string]verifiedOriginal `json:"assets"`
}

func originalReceiptPath(c config.Config) string {
	return filepath.Join(c.DataDir, "health/storage-original-verification.json")
}

// Caller holds the ordinary stopped-instance lock and supplies assets read from
// that exact database. No source comics, archive files or asset rows are written.
func VerifyMigrationOriginals(ctx context.Context, c config.Config, manifest string, assets []model.StorageAsset) (int, error) {
	if !filepath.IsAbs(manifest) || appdata.NoSymlinks(manifest) != nil {
		return 0, model.ErrInput
	}
	rel, e := filepath.Rel(c.DataDir, manifest)
	if e != nil || rel == "." || !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return 0, model.ErrInput
	}
	var m MigrationOriginalManifest
	if e = readInstanceJSON(manifest, &m, 16<<20); e != nil {
		return 0, e
	}
	if m.Format != 1 || len(m.Files) == 0 || len(m.Files) > 100000 {
		return 0, model.ErrInput
	}
	digest, _, e := instanceHash(ctx, manifest)
	if e != nil {
		return 0, e
	}
	byDigest := map[string]model.InstanceFile{}
	keys := map[string]bool{}
	for _, f := range m.Files {
		hash, x := hex.DecodeString(f.SHA256)
		if !fs.ValidPath(f.Key) || strings.ContainsAny(f.Key, "\\\x00") || f.Size < 0 || f.Size > 128<<30 || x != nil || len(hash) != 32 || keys[f.Key] {
			return 0, model.ErrInput
		}
		keys[f.Key] = true
		byDigest[f.SHA256] = f
	}
	receipt := originalReceipt{Format: 1, Archive: manifest, Digest: digest, Files: map[string]verifiedOriginal{}}
	databaseInfo, e := os.Stat(filepath.Join(c.DataDir, "app.sqlite"))
	if e != nil {
		return 0, e
	}
	receipt.DatabaseDevice, receipt.DatabaseInode = fileIdentity(databaseInfo)
	if receipt.DatabaseInode == 0 {
		return 0, model.ErrInput
	}

	for _, a := range assets {
		if !fs.ValidPath(a.Key) || strings.ContainsAny(a.Key, "\\\x00") || !strings.HasPrefix(a.Key, "assets/treec-originals/") {
			return 0, model.ErrInput
		}
		f, ok := byDigest[a.Digest]
		if !ok || f.Size != a.Size {
			return 0, model.ErrConflict
		}
		// Both copies must really match the registered digest, not just their names.
		for _, path := range []string{filepath.Join(filepath.Dir(manifest), f.Key), filepath.Join(c.DataDir, a.Key)} {
			if e = appdata.NoSymlinks(path); e != nil {
				return 0, e
			}
			got, n, x := instanceHash(ctx, path)
			if x != nil {
				return 0, x
			}
			if got != a.Digest || n != a.Size {
				return 0, model.ErrConflict
			}
		}
		info, e := os.Stat(filepath.Join(filepath.Dir(manifest), f.Key))
		if e != nil {
			return 0, e
		}
		receipt.Files[a.Key] = verifiedOriginal{Asset: a, File: f, Mtime: info.ModTime().UnixNano()}
	}
	after, _, e := instanceHash(ctx, manifest)
	if e != nil {
		return 0, e
	}
	if after != digest {
		return 0, model.ErrConflict
	}
	if e = instancePrivateDir(filepath.Dir(originalReceiptPath(c))); e != nil {
		return 0, e
	}
	return len(receipt.Files), instanceJSON(originalReceiptPath(c), receipt)
}
func (s *Service) loadOriginalReceipt() (originalReceipt, error) {
	var r originalReceipt
	if e := readInstanceJSON(originalReceiptPath(s.c), &r, 16<<20); e != nil {
		return r, e
	}
	databaseInfo, e := os.Stat(filepath.Join(s.c.DataDir, "app.sqlite"))
	if e != nil {
		return r, e
	}
	dev, ino := fileIdentity(databaseInfo)
	if ino == 0 || dev != r.DatabaseDevice || ino != r.DatabaseInode {
		return r, model.ErrConflict
	}

	if r.Format != 1 || !filepath.IsAbs(r.Archive) || appdata.NoSymlinks(r.Archive) != nil {
		return r, model.ErrInput
	}
	var manifest MigrationOriginalManifest
	if e := readInstanceJSON(r.Archive, &manifest, 16<<20); e != nil {
		return r, e
	}
	f, e := os.Open(r.Archive)
	if e != nil {
		return r, e
	}
	raw, e := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	f.Close()
	if e != nil {
		return r, e
	}
	if len(raw) > 16<<20 {
		return r, model.ErrInput
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != r.Digest {
		return r, model.ErrConflict
	}
	return r, nil
}
func originalReceiptMatches(r originalReceipt, a model.StorageAsset) error {
	v, ok := r.Files[a.Key]
	if !ok || v.Asset.ID != a.ID || v.Asset.Digest != a.Digest || v.Asset.Size != a.Size || a.Referenced {
		return model.ErrConflict
	}
	if !fs.ValidPath(v.File.Key) || strings.Contains(v.File.Key, "\\") {
		return model.ErrInput
	}
	path := filepath.Join(filepath.Dir(r.Archive), v.File.Key)
	if e := appdata.NoSymlinks(path); e != nil {
		return e
	}
	info, e := os.Stat(path)
	if e != nil {
		return e
	}
	if !info.Mode().IsRegular() || info.Size() != a.Size || info.ModTime().UnixNano() != v.Mtime {
		return model.ErrConflict
	}
	return nil
}
func (s *Service) verifyOriginalForDelete(ctx context.Context, key, digest string) error {
	r, e := s.loadOriginalReceipt()
	if e != nil {
		return e
	}
	v, ok := r.Files[key]
	if !ok || v.Asset.Digest != digest {
		return model.ErrConflict
	}
	if e = originalReceiptMatches(r, v.Asset); e != nil {
		return e
	}
	hash, n, e := instanceHash(ctx, filepath.Join(filepath.Dir(r.Archive), v.File.Key))
	if e != nil {
		return e
	}
	if hash != v.Asset.Digest || n != v.Asset.Size {
		return model.ErrConflict
	}
	return nil
}

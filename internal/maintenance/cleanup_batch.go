package maintenance

import (
	"context"
	"errors"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type cleanupStamp struct {
	device, inode uint64
	size, mtime   int64
	missing       bool
}

func cleanupIdentity(path string) (cleanupStamp, error) {
	if e := appdata.NoSymlinks(path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return cleanupStamp{}, e
	}
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return cleanupStamp{missing: true}, nil
	}
	if e != nil {
		return cleanupStamp{}, e
	}
	if !info.Mode().IsRegular() {
		return cleanupStamp{}, model.ErrConflict
	}
	dev, ino := fileIdentity(info)
	return cleanupStamp{device: dev, inode: ino, size: info.Size(), mtime: info.ModTime().UnixNano()}, nil
}
func matchesCleanupFile(st cleanupStamp, f cleanupFile) bool {
	return !st.missing && st.device == f.Device && st.inode == f.Inode && st.size == f.Size && st.mtime == f.Mtime
}

// Metadata is trusted only within one batch, and only while its file identity
// remains unchanged. Changed receipts/activation logs are re-read before unlink.
func (s *Service) prepareCleanupBatch(ctx context.Context, j model.Job, root *os.Root, files []cleanupFile, offset int, categories []string) ([]model.StorageDeletion, error) {
	started := time.Now()
	out := []model.StorageDeletion{}
	activation := cleanupStamp{}
	activationLoaded := false
	var activationRecord instanceActivation
	var receipt originalReceipt
	var receiptStamp, manifestStamp cleanupStamp
	receiptLoaded := false
	for i, file := range files {
		if e := s.repo.CheckExecution(ctx, j); e != nil {
			return nil, e
		}
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if !fs.ValidPath(file.Key) || strings.Contains(file.Key, "\\") {
			return nil, model.ErrInput
		}
		if file.Material.Kind == "instance_recovery" && !activationLoaded {
			st, e := cleanupIdentity(activationPath(s.c))
			if e != nil {
				return nil, e
			}
			if !st.missing {
				if e = readInstanceJSON(activationPath(s.c), &activationRecord, instanceMaxManifest); e != nil {
					return nil, e
				}
			}
			after, e := cleanupIdentity(activationPath(s.c))
			if e != nil {
				return nil, e
			}
			if after != st {
				return nil, model.ErrConflict
			}
			activation = st
			activationLoaded = true

		}
		if file.Material.Kind == "instance_recovery" && activationRecord.ID == file.Material.OperationID && !(activationRecord.State == "completed" || activationRecord.State == "rolled_back" && activationRecord.RollbackFinished) {
			return nil, model.ErrConflict
		}

		var externalPath string
		var externalStamp cleanupStamp
		if file.Kind == "migration_originals" {
			if !receiptLoaded {
				var e error
				receiptStamp, e = cleanupIdentity(originalReceiptPath(s.c))
				if e != nil {
					return nil, e
				}
				receipt, e = s.loadOriginalReceipt()
				if e != nil {
					return nil, e
				}
				after, e := cleanupIdentity(originalReceiptPath(s.c))
				if e != nil {
					return nil, e
				}
				if after != receiptStamp {
					return nil, model.ErrConflict
				}
				manifestStamp, e = cleanupIdentity(receipt.Archive)
				if e != nil {
					return nil, e
				}
				digest, _, e := instanceHash(ctx, receipt.Archive)
				if e != nil {
					return nil, e
				}
				afterManifest, e := cleanupIdentity(receipt.Archive)
				if e != nil {
					return nil, e
				}
				if afterManifest != manifestStamp || digest != receipt.Digest {
					return nil, model.ErrConflict
				}
				receiptLoaded = true
			}
			v, ok := receipt.Files[file.Key]
			if !ok || v.Asset.Digest != file.Digest {
				return nil, model.ErrConflict
			}
			if e := originalReceiptMatches(receipt, v.Asset); e != nil {
				return nil, e
			}
			externalPath = filepath.Join(filepath.Dir(receipt.Archive), v.File.Key)
			var e error
			externalStamp, e = cleanupIdentity(externalPath)
			if e != nil {
				return nil, e
			}
			digest, n, e := instanceHash(ctx, externalPath)
			if e != nil {
				return nil, e
			}
			after, e := cleanupIdentity(externalPath)
			if e != nil {
				return nil, e
			}
			if after != externalStamp || digest != file.Digest || n != file.Size {
				return nil, model.ErrConflict
			}
		}
		path := filepath.Join(s.c.DataDir, file.Key)
		st, e := cleanupIdentity(path)
		if e != nil {
			return nil, e
		}
		valid := matchesCleanupFile(st, file)
		if valid {
			digest, n, e := instanceHash(ctx, path)
			if e != nil {
				return nil, e
			}
			after, e := cleanupIdentity(path)
			if e != nil {
				return nil, e
			}
			valid = st == after && digest == file.Digest && n == file.Size
		}
		f := model.StorageDeletion{Kind: file.Kind, Key: file.Key, AssetID: file.AssetID, Digest: file.Digest, Ordinal: int64(offset + i), Size: file.Size, Material: file.Material, OnlyRedundant: file.Kind == "covers" && !slices.Contains(categories, "covers") || file.Kind == "tag_images" && !slices.Contains(categories, "tag_images")}
		f.Remove = func() (bool, error) {
			if e := ctx.Err(); e != nil {
				return false, e
			}
			if file.Kind == "snapshots" {
				latest, e := latestUpgradeSnapshot(s.c.DataDir)
				if e != nil {
					return false, e
				}
				if filepath.Base(file.Key) == latest {
					return false, nil
				}
			}
			if file.Material.Kind == "instance_recovery" {
				current, e := cleanupIdentity(activationPath(s.c))
				if e != nil {
					return false, e
				}
				if current != activation {
					if e = s.validateMaterialActivation(file.Material); e != nil {
						return false, e
					}
					return false, model.ErrConflict
				}
			}
			if file.Kind == "migration_originals" {
				current, e := cleanupIdentity(originalReceiptPath(s.c))
				if e != nil {
					return false, e
				}
				manifest, e := cleanupIdentity(receipt.Archive)
				if e != nil {
					return false, e
				}
				ext, e := cleanupIdentity(externalPath)
				if e != nil {
					return false, e
				}
				if current != receiptStamp || manifest != manifestStamp || ext != externalStamp {
					if e = s.verifyOriginalForDelete(ctx, file.Key, file.Digest); e != nil {
						return false, e
					}
					return false, model.ErrConflict
				}
			}
			current, e := cleanupIdentity(path)
			if e != nil {
				return false, e
			}
			if current.missing {
				return false, os.ErrNotExist
			}
			if !valid || current != st || !matchesCleanupFile(current, file) {
				return false, nil
			}
			if file.Kind == "tag_images" {
				if s.tagImages == nil {
					return false, model.ErrInput
				}
				e = s.tagImages.RemoveCacheIfUnchanged(filepath.Base(file.Key), file.Size, file.Mtime)
			} else if file.Kind == "covers" {
				e = s.catalog.RemoveCoverIfUnchanged(filepath.Base(file.Key), file.Size, file.Mtime)
			} else {
				e = root.Remove(file.Key)
			}
			return e == nil, e
		}
		out = append(out, f)
		if time.Since(started) >= 250*time.Millisecond {
			break
		}
	}
	return out, nil
}

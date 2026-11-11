package maintenance

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/tagimages"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func storageWrite(t *testing.T, dir, key string, raw []byte) {
	t.Helper()
	path := filepath.Join(dir, key)
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func storageCleanup(t *testing.T, s *Service, db *store.Store, categories string) model.MaintenancePlan {
	t.Helper()
	ctx := context.Background()
	p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":` + categories + `}`)})
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.Execute(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	if done := wait(t, db, j.ID); done.State != "completed" {
		t.Fatal(done)
	}
	p, e = db.MaintenancePlan(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestStorageRedundantCacheOriginalsAndStatistics(t *testing.T) {
	s, db, cat, l, _ := fixture(t)
	ctx := context.Background()
	b := indexed(t, db, cat, l)
	raw, _, e := cat.Image(ctx, b.ID, b.ContentRevision, 1, false)
	if e != nil {
		t.Fatal(e)
	}
	selection, e := cat.UploadCover(ctx, b.ID, 0, raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = cat.Image(ctx, b.ID, b.ContentRevision, 1, true); e != nil {
		t.Fatal(e)
	}
	name := model.CoverCacheName(b.ID, b.ContentRevision, b.ParsingRevision, 0)
	oldKey := "cache/covers/v1/" + name[:2] + "/" + name
	name = model.SelectedCoverCacheName(b.ID, b.ContentRevision, selection.ParsingRevision, selection.Revision, "asset")
	selectedKey := "cache/covers/v1/" + name[:2] + "/" + name
	originalKey := "assets/treec-originals/anonymous.png"
	storageWrite(t, s.c.DataDir, originalKey, raw)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, e = db.Write.Exec("INSERT INTO assets VALUES('original',?,?,?,1)", originalKey, digest, len(raw)); e != nil {
		t.Fatal(e)
	}
	before, e := s.Storage(ctx)
	if e != nil {
		t.Fatal(e)
	}
	storageWrite(t, s.c.DataDir, "unrecognized.bin", []byte("retained"))
	cached, e := s.Storage(ctx)
	if e != nil || before["statistics_at"] != cached["statistics_at"] {
		t.Fatal("statistics not cached", e)
	}
	p := storageCleanup(t, s, db, `["redundant_covers","orphan_assets"]`)
	if _, e = os.Stat(filepath.Join(s.c.DataDir, oldKey)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("redundant auto cover retained", e)
	}
	for _, key := range []string{selectedKey, originalKey, "unrecognized.bin"} {
		if _, e = os.Stat(filepath.Join(s.c.DataDir, key)); e != nil {
			t.Fatal(key, e)
		}
	}
	result := p.Summary["cleanup_result"].(map[string]any)
	if result["deleted_files"].(float64) < 1 || result["released_bytes"].(float64) < 1 {
		t.Fatal(result)
	}
	assets, e := store.MigrationOriginalAssets(ctx, s.c.DataDir)
	if e != nil || len(assets) != 1 {
		t.Fatal(assets, e)
	}
	archive := t.TempDir()
	storageWrite(t, archive, "original.png", raw)
	manifest := filepath.Join(archive, "originals.json")
	if e = instanceJSON(manifest, MigrationOriginalManifest{Format: 1, Files: []model.InstanceFile{{Key: "original.png", Size: int64(len(raw)), SHA256: digest}}}); e != nil {
		t.Fatal(e)
	}
	if n, e := VerifyMigrationOriginals(ctx, s.c, manifest, assets); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	// Generic cleanup still cannot bypass the explicit migration category.
	files, _, e := s.cleanupPreview(ctx, []string{"orphan_assets"})
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range files {
		if f.Key == originalKey {
			t.Fatal("original escaped category")
		}
	}
	storageCleanup(t, s, db, `["migration_originals"]`)
	if _, e = os.Stat(filepath.Join(s.c.DataDir, originalKey)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(archive, "original.png")); e != nil {
		t.Fatal("external original removed", e)
	}
	if _, e = db.Asset(ctx, "original"); !errors.Is(e, model.ErrNotFound) {
		t.Fatal("asset row retained", e)
	}
}
func TestStorageOriginalMutationAndNewReferenceProtect(t *testing.T) {
	for _, mode := range []string{"archive_changed", "new_reference", "asset_replaced"} {
		t.Run(mode, func(t *testing.T) {
			s, db, cat, l, _ := fixture(t)
			ctx := context.Background()
			b := indexed(t, db, cat, l)
			raw, _, e := cat.Image(ctx, b.ID, b.ContentRevision, 1, false)
			if e != nil {
				t.Fatal(e)
			}
			key := "assets/treec-originals/one.png"
			digest := fmt.Sprintf("%x", sha256.Sum256(raw))
			storageWrite(t, s.c.DataDir, key, raw)
			if _, e = db.Write.Exec("INSERT INTO assets VALUES('original',?,?,?,1)", key, digest, len(raw)); e != nil {
				t.Fatal(e)
			}
			archive := t.TempDir()
			storageWrite(t, archive, "one.png", raw)
			manifest := filepath.Join(archive, "manifest.json")
			instanceJSON(manifest, MigrationOriginalManifest{Format: 1, Files: []model.InstanceFile{{Key: "one.png", Size: int64(len(raw)), SHA256: digest}}})
			assets, _ := store.MigrationOriginalAssets(ctx, s.c.DataDir)
			if _, e = VerifyMigrationOriginals(ctx, s.c, manifest, assets); e != nil {
				t.Fatal(e)
			}
			p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["migration_originals"]}`)})
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "archive_changed":
				storageWrite(t, archive, "one.png", []byte("corrupt"))
			case "new_reference":
				_, e = db.SetCover(ctx, b.ID, 0, model.CoverSelection{Mode: "asset", AssetID: ptrString("original")}, nil)
			case "asset_replaced":
				_, e = db.Write.Exec("UPDATE assets SET id='replacement' WHERE id='original'")
			}
			if e != nil {
				t.Fatal(e)
			}
			j, e := s.Execute(ctx, p.ID)
			if e != nil {
				t.Fatal(e)
			}
			s.Start(ctx)
			done := wait(t, db, j.ID)
			if mode == "archive_changed" && done.State != "failed" {
				t.Fatal(done)
			}
			if _, e = os.Stat(filepath.Join(s.c.DataDir, key)); e != nil {
				t.Fatal("protected original removed", e)
			}
		})
	}
}
func ptrString(s string) *string { return &s }
func offlineStorageFixture(t *testing.T, s *Service, id string) InstanceManifest {
	t.Helper()
	raw := []byte("synthetic staging only")
	f := model.InstanceFile{Key: "app.sqlite", Size: int64(len(raw)), SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	m := InstanceManifest{Format: 1, Application: "nyamangaserver", Baseline: "foundation-v1", Build: InstanceBuild{"test", "anonymous"}, Secrets: true, Files: []model.InstanceFile{f}}
	root := filepath.Join(s.c.DataDir, "instance-recovery", id)
	storageWrite(t, root, "validated/app.sqlite", raw)
	instanceJSON(filepath.Join(root, "manifest.json"), m)
	zf, e := os.Create(filepath.Join(root, "upload.zip"))
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(zf)
	w, _ := z.Create("manifest.json")
	json.NewEncoder(w).Encode(m)
	w, _ = z.Create("app.sqlite")
	w.Write(raw)
	z.Close()
	zf.Close()
	return m
}
func TestStorageOfflineFailedAndCompletedMaterialsRestart(t *testing.T) {
	s, db, _, _, _ := fixture(t)
	ctx := context.Background()
	offlineStorageFixture(t, s, "offline-failed")
	m := offlineStorageFixture(t, s, "offline-completed")
	root := filepath.Join(s.c.DataDir, "instance-recovery/offline-completed")
	storageWrite(t, root, "prepared/app.sqlite", []byte("synthetic staging only"))
	storageWrite(t, root, "before-activation/app.sqlite", []byte("synthetic staging only"))
	p := instanceActivation{ID: "offline-completed", State: "completed", Revision: 3, Build: m.Build, Config: instanceConfigHash(s.c), Incoming: m.Files, Before: m.Files, RollbackFinished: false}
	if e := writeInstanceActivation(s.c, p); e != nil {
		t.Fatal(e)
	}
	storageWrite(t, s.c.DataDir, "instance-recovery/unknown/unrecognized.bin", []byte("keep"))
	storageWrite(t, s.c.DataDir, "instance-recovery/offline-completed/log.json", []byte("{}"))
	plan := storageCleanup(t, s, db, `["instance_recovery"]`)
	if plan.Summary["cleanup_result"].(map[string]any)["deleted_files"].(float64) != 6 {
		t.Fatal(plan.Summary)
	}
	for _, key := range []string{"instance-recovery/offline-failed/manifest.json", "instance-recovery/offline-completed/log.json", "instance-recovery/unknown/unrecognized.bin"} {
		if _, e := os.Stat(filepath.Join(s.c.DataDir, key)); e != nil {
			t.Fatal(key, e)
		}
	}
	result, e := ReadInstanceActivation(s.c, m.Build)
	if e != nil || !result.MaterialsCleaned || result.State != "completed" {
		t.Fatal(result, e)
	}
	if e = ActivateInstance(ctx, s.c, m.Build, InstanceActivationOptions{}); e != nil {
		t.Fatal("cleaned receipt blocked startup", e)
	}
	if _, e = RequestOfflineInstanceRollback(ctx, s.c, m.Build, result.Revision, func(context.Context, string) (int64, error) { return 0, nil }); !errors.Is(e, model.ErrConflict) {
		t.Fatal("rollback after cleanup accepted", e)
	}
	stop, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s.Shutdown(stop)
	db.Close()
	reopened, e := store.Open(ctx, s.c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	var count int
	if e = reopened.Read.QueryRow("SELECT count(*) FROM storage_material_receipts WHERE state='cleaned'").Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
}
func TestStorageHealthRollbackMaterialsExplicitlyEnd(t *testing.T) {
	s, db, cat, l, _ := fixture(t)
	b := indexed(t, db, cat, l)
	ctx := context.Background()
	db.Write.Exec("UPDATE search_documents SET title='synthetic broken' WHERE edition_id=?", b.ID)
	h := NewHealth(db, s.c, cat.Pool)
	p := healthPlanFor(t, h, db, "projection")
	r := healthExecute(t, h, p)
	h.Tick(ctx)
	r, e := db.HealthRepair(ctx, r.ID)
	if e != nil || r.State != "completed" {
		t.Fatal(r, e)
	}
	files, _, e := s.cleanupPreview(ctx, []string{"health_rollback"})
	if e != nil || len(files) == 0 {
		t.Fatal(files, e)
	}
	storageCleanup(t, s, db, `["health_rollback"]`)
	r, e = db.HealthRepair(ctx, r.ID)
	if e != nil || !r.MaterialsCleaned {
		t.Fatal(r, e)
	}
	if _, e = h.Rollback(ctx, r.ID, model.HealthExecuteInput{RequestKey: "cleaned-rollback", ExpectedRevision: r.Revision, ConfirmMaintenance: true}); !errors.Is(e, model.ErrHealthStale) {
		t.Fatal(e)
	}
	for _, f := range files {
		if _, e = os.Stat(filepath.Join(s.c.DataDir, f.Key)); !errors.Is(e, os.ErrNotExist) {
			t.Fatal(e)
		}
	}
	if _, e = os.Stat(filepath.Join(s.c.DataDir, "health/repairs", r.ID, "journal.json")); e != nil {
		t.Fatal("journal removed", e)
	}
}

type interruptedStorage struct {
	Repository
	storageRepository
	calls int
}

func (r *interruptedStorage) DeleteStorageFile(ctx context.Context, j model.Job, kind, key, id, digest string, onlyRedundant bool, index, size int64, m model.StorageMaterial, remove func() (bool, error)) (bool, error) {
	r.calls++
	if r.calls == 2 {
		return false, context.Canceled
	}
	return r.storageRepository.DeleteStorageFile(ctx, j, kind, key, id, digest, onlyRedundant, index, size, m, remove)
}
func TestStorageFrozenIdentityAndInterruptedCheckpoint(t *testing.T) {
	s, db, cat, _, _ := fixture(t)
	ctx := context.Background()
	old := time.Now().Add(-48 * time.Hour)
	for _, key := range []string{"tmp/a", "tmp/b", "tmp/c"} {
		storageWrite(t, s.c.DataDir, key, []byte("old"))
		os.Chtimes(filepath.Join(s.c.DataDir, key), old, old)
	}
	p, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["tmp"]}`)})
	if e != nil {
		t.Fatal(e)
	}
	// Replacement preserves size and mtime; the frozen inode and digest still differ.
	path := filepath.Join(s.c.DataDir, "tmp/c")
	os.Remove(path)
	storageWrite(t, s.c.DataDir, "tmp/c", []byte("new"))
	os.Chtimes(path, old, old)
	j, e := s.Execute(ctx, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	s.repo = &interruptedStorage{Repository: db, storageRepository: db}
	if e = s.cleanupFrozen(ctx, *claimed, p.Input.Options); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	progress, e := db.CleanupProgress(ctx, *claimed)
	if e != nil || progress["next_index"] != 1 || progress["released_bytes"] != 3 {
		t.Fatal(progress, e)
	}
	cat.Close()
	db.Close()
	again, e := store.Open(ctx, s.c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	resumed := New(again, s.c, cat, noRemote{}, s.logger)
	resumed.Start(ctx)
	defer resumed.Shutdown(ctx)
	if done := wait(t, again, j.ID); done.State != "completed" {
		t.Fatal(done)
	}
	progress, e = again.CleanupProgress(ctx, *claimed)
	if e != nil || progress["next_index"] != 3 || progress["released_bytes"] != 6 || progress["deleted_files"] != 2 || progress["skipped_files"] != 1 {
		t.Fatal(progress, e)
	}
	raw, e := os.ReadFile(path)
	if e != nil || string(raw) != "new" {
		t.Fatal("replacement removed", e)
	}
}

func TestStorageOperationSelectionAndSnapshotRetention(t *testing.T) {
	s, _, _, _, _ := fixture(t)
	ctx := context.Background()
	offlineStorageFixture(t, s, "offline-one")
	offlineStorageFixture(t, s, "offline-two")
	files, summary, e := s.cleanupPreview(ctx, []string{"instance_recovery"}, []string{"instance:offline-one"})
	if e != nil || len(files) != 2 {
		t.Fatal(len(files), summary, e)
	}
	for _, f := range files {
		if f.Material.OperationID != "offline-one" {
			t.Fatal(f)
		}
	}
	if _, _, e = s.cleanupPreview(ctx, []string{"instance_recovery"}, []string{"instance:../../outside"}); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
	for _, name := range []string{"older.sqlite", "latest.sqlite", "report.json"} {
		storageWrite(t, s.c.DataDir, "upgrade-snapshots/"+name, []byte("anonymous"))
	}
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(s.c.DataDir, "upgrade-snapshots/older.sqlite"), old, old)
	files, _, e = s.cleanupPreview(ctx, []string{"snapshots"})
	if e != nil || len(files) != 1 || filepath.Base(files[0].Key) != "older.sqlite" {
		t.Fatal(files, e)
	}
}

// The callback faults after unlink but before the writer transaction commits.
// after runs only after a committed deletion and can change the next member.
type storageDeleteProbe struct {
	*store.Store
	crashKey string
	after    func(string) error
	facts    int
	holdRead bool
}

func (r *storageDeleteProbe) StorageFacts(ctx context.Context) (model.StorageFacts, error) {
	r.facts++
	return r.Store.StorageFacts(ctx)
}
func (r *storageDeleteProbe) DeleteStorageFile(ctx context.Context, j model.Job, kind, key, id, digest string, onlyRedundant bool, index, size int64, m model.StorageMaterial, remove func() (bool, error)) (bool, error) {
	var readWaited atomic.Bool
	if r.holdRead {
		conn, e := r.Read.Conn(ctx)
		if e != nil {
			return false, e
		}
		defer conn.Close()
		// A regression that queries a second connection from the writer callback
		// fails deterministically instead of hanging the entire test process.
		timer := time.AfterFunc(500*time.Millisecond, func() { readWaited.Store(true); conn.Close() })
		defer timer.Stop()
	}
	removed, e := r.Store.DeleteStorageFile(ctx, j, kind, key, id, digest, onlyRedundant, index, size, m, func() (bool, error) {
		removed, e := remove()
		if e == nil && removed && key == r.crashKey {
			return false, context.Canceled
		}
		return removed, e
	})
	if readWaited.Load() {
		return false, errors.New("filesystem callback reacquired SQL/health lease")
	}
	if e == nil && removed && r.after != nil {
		e = r.after(key)
	}
	return removed, e
}

func TestStoragePreviewSingleInventoryAndCapacityBeforeHash(t *testing.T) {
	s, db, _, _, _ := fixture(t)
	probe := &storageDeleteProbe{Store: db}
	s.repo = probe
	var hashes int
	s.CleanupPreviewReader = func(r io.Reader) io.Reader { hashes++; return r }
	old := time.Now().Add(-48 * time.Hour)
	storageWrite(t, s.c.DataDir, "tmp/one", []byte("one"))
	os.Chtimes(filepath.Join(s.c.DataDir, "tmp/one"), old, old)
	_, e := s.Plan(context.Background(), model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["tmp"]}`)})
	if e != nil || probe.facts != 1 || hashes != 1 {
		t.Fatal(probe.facts, hashes, e)
	}
	for i := 0; i < 2; i++ {
		if _, e = s.Storage(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if probe.facts != 2 || hashes != 1 {
		t.Fatal("statistics did unnecessary walk/hash", probe.facts, hashes)
	}
	entries := []storageEntry{{cleanupFile: cleanupFile{Kind: "tmp", Key: "tmp/one", Size: (1 << 40) + 1}, Cleanable: true}}
	if _, _, e = s.cleanupPreviewEntries(context.Background(), entries, []string{"tmp"}); !errors.Is(e, model.ErrCapacity) || hashes != 1 {
		t.Fatal("budget checked after IO", e, hashes)
	}
}

func TestStorageCacheReferenceChangesBetweenDeletions(t *testing.T) {
	for _, category := range []string{"redundant_covers", "covers", "redundant_tag_images", "tag_images"} {
		t.Run(category, func(t *testing.T) {
			s, db, cat, l, _ := fixture(t)
			ctx := context.Background()
			b := indexed(t, db, cat, l)
			var secondKey string
			var switchCurrent func() error
			kind := "covers"
			if strings.Contains(category, "tag_images") {
				kind = "tag_images"
				images := tagimages.New(db, s.c, cat.Pool)
				s.WithTagImages(images)
				raw := []byte("anonymous digest source")
				digest := fmt.Sprintf("%x", sha256.Sum256(raw))
				// A registered unreferenced original; selecting it between items must protect
				// the matching existing cache, independent of the original's own lifetime.
				if _, e := db.Write.Exec("INSERT INTO tags(id,namespace,name) VALUES('cache-tag','character','anonymous cache tag')"); e != nil {
					t.Fatal(e)
				}
				if _, e := db.Write.Exec("INSERT INTO assets VALUES('cache-asset','assets/tags/cache.png',?,?,1)", digest, len(raw)); e != nil {
					t.Fatal(e)
				}
				name := model.TagImageCacheName(digest, "foreground")
				secondKey = "cache/tag-images/v1/" + name[:2] + "/" + name
				switchCurrent = func() error {
					_, e := db.Write.Exec("INSERT INTO tag_images(tag_id,role,asset_id,width,height,format,revision) VALUES('cache-tag','foreground','cache-asset',1,1,'png',1)")
					return e
				}
			} else {
				raw, _, e := cat.Image(ctx, b.ID, b.ContentRevision, 1, false)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = cat.UploadCover(ctx, b.ID, 0, raw); e != nil {
					t.Fatal(e)
				}
				name := model.CoverCacheName(b.ID, b.ContentRevision, b.ParsingRevision, 0)
				secondKey = "cache/covers/v1/" + name[:2] + "/" + name
				// Simulate a concurrent committed writer while cleanup is already active;
				// unlike SetCover's ordinary admission, the deletion guard must stand alone.
				switchCurrent = func() error {
					_, e := db.Write.Exec("UPDATE cover_selections SET mode='auto',asset_id=NULL,revision=revision+1 WHERE edition_id=?", b.ID)
					return e
				}
			}
			prefix := "cache/covers/v1/"
			if kind == "tag_images" {
				prefix = "cache/tag-images/v1/"
			}
			firstKey := prefix + "00/" + strings.Repeat("0", 64) + ".png"
			storageWrite(t, s.c.DataDir, firstKey, []byte("first"))
			storageWrite(t, s.c.DataDir, secondKey, []byte("second"))
			switched := false
			db.Read.SetMaxOpenConns(1)
			s.repo = &storageDeleteProbe{Store: db, holdRead: true, after: func(key string) error {
				if key == firstKey {
					switched = true
					return switchCurrent()
				}
				return nil
			}}
			p := storageCleanup(t, s, db, `["`+category+`"]`)
			if !switched {
				t.Fatal("did not switch after first unlink")
			}
			_, e := os.Stat(filepath.Join(s.c.DataDir, secondKey))
			if strings.HasPrefix(category, "redundant_") {
				if e != nil {
					t.Fatal("new current cache deleted", e)
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				t.Fatal("explicit all-clear unexpectedly protected", e)
			}
			if p.Summary["cleanup_result"] == nil {
				t.Fatal(p)
			}
		})
	}
}

// The probe intentionally inserts a competing writer between transactions.
// Force single-file batches here; production batch exclusion is tested separately.
func (r *storageDeleteProbe) DeleteStorageBatch(ctx context.Context, j model.Job, files []model.StorageDeletion) ([]bool, error) {
	out := []bool{}
	for _, f := range files {
		removed, e := r.DeleteStorageFile(ctx, j, f.Kind, f.Key, f.AssetID, f.Digest, f.OnlyRedundant, f.Ordinal, f.Size, f.Material, f.Remove)
		if e != nil {
			return nil, e
		}
		out = append(out, removed)
	}
	return out, nil
}

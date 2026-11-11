package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageHealthCleanupCrashThenStandardBackupRestore(t *testing.T) {
	for _, crash := range []string{"none", "asset", "health"} {
		t.Run(crash, func(t *testing.T) {
			s, db, cat, l, _ := fixture(t)
			ctx := context.Background()
			b := indexed(t, db, cat, l)
			if _, _, e := auth.New(db, "anonymous-setup").Setup(ctx, "anonymous-setup", "anonymous recovery password"); e != nil {
				t.Fatal(e)
			}
			secretDir := t.TempDir()
			s.c.ProviderKeyFile = filepath.Join(secretDir, "key")
			s.c.ProviderCredentialsFile = filepath.Join(secretDir, "credentials")
			secret := "anonymous-cookie"
			if _, e := provider.NewVault(s.c.ProviderKeyFile, s.c.ProviderCredentialsFile).Credentials(provider.CredentialPatch{Expected: 1, Fields: map[string]provider.SecretChange{"ipb_pass_hash": {Op: "set", Value: &secret}}}); e != nil {
				t.Fatal(e)
			}
			if _, e := db.Write.Exec("UPDATE search_documents SET title='anonymous broken' WHERE edition_id=?", b.ID); e != nil {
				t.Fatal(e)
			}
			health := NewHealth(db, s.c, cat.Pool)
			hp := healthPlanFor(t, health, db, "projection")
			repair := healthExecute(t, health, hp)
			health.Tick(ctx)
			repair, e := db.HealthRepair(ctx, repair.ID)
			if e != nil || repair.State != "completed" {
				t.Fatal(repair, e)
			}
			materials, _, e := s.cleanupPreview(ctx, []string{"health_rollback"})
			if e != nil || len(materials) == 0 {
				t.Fatal(materials, e)
			}
			key := "assets/anonymous-orphan.bin"
			raw := []byte("unreferenced original")
			storageWrite(t, s.c.DataDir, key, raw)
			old := time.Now().Add(-48 * time.Hour)
			os.Chtimes(filepath.Join(s.c.DataDir, key), old, old)
			if _, e = db.Write.Exec("INSERT INTO assets VALUES('cleanup-orphan',?,?,?,1)", key, fmt.Sprintf("%x", sha256.Sum256(raw)), len(raw)); e != nil {
				t.Fatal(e)
			}
			plan, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["orphan_assets","health_rollback"]}`)})
			if e != nil {
				t.Fatal(e)
			}
			job, e := s.Execute(ctx, plan.ID)
			if e != nil {
				t.Fatal(e)
			}
			claimed, e := db.Claim(ctx, job.ID)
			if e != nil {
				t.Fatal(e)
			}
			crashKey := ""
			if crash == "asset" {
				crashKey = key
			}
			if crash == "health" {
				crashKey = materials[0].Key
			}
			s.repo = &storageDeleteProbe{Store: db, crashKey: crashKey}
			e = s.cleanupFrozen(ctx, *claimed, plan.Input.Options)
			if crash == "none" {
				if e != nil {
					t.Fatal(e)
				}
				s.repo = db
				s.Start(ctx)
				if done := wait(t, db, job.ID); done.State != "completed" {
					t.Fatal(done)
				}
			} else {
				if !errors.Is(e, context.Canceled) {
					t.Fatal(e)
				}
				if _, e = os.Stat(filepath.Join(s.c.DataDir, crashKey)); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("fault not after unlink", e)
				}
				var stillRegistered int
				query := "SELECT count(*) FROM assets WHERE storage_key=?"
				if crash == "health" {
					query = "SELECT count(*) FROM health_artifacts WHERE key=? AND pinned=1"
				}
				if e = db.Read.QueryRow(query, crashKey).Scan(&stillRegistered); e != nil || stillRegistered != 1 {
					t.Fatal("SQL did not roll back", stillRegistered, e)
				}
				cat.Close()
				db.Close()
				db, e = store.Open(ctx, s.c.DataDir, migrations.Files)
				if e != nil {
					t.Fatal(e)
				}
				defer db.Close()
				resumed := New(db, s.c, cat, noRemote{}, s.logger)
				resumed.Start(ctx)
				defer resumed.Shutdown(ctx)
				if done := wait(t, db, job.ID); done.State != "completed" {
					t.Fatal(done)
				}
				progress, e := db.CleanupProgress(ctx, *claimed)
				if e != nil || progress["skipped_files"] != 1 {
					t.Fatal("missing item not settled conservatively", progress, e)
				}
			}
			var orphan, pinned int
			if e = db.Read.QueryRow("SELECT count(*) FROM assets WHERE id='cleanup-orphan'").Scan(&orphan); e != nil || orphan != 0 {
				t.Fatal(orphan, e)
			}
			if e = db.Read.QueryRow("SELECT count(*) FROM health_artifacts WHERE repair_id=? AND pinned=1", repair.ID).Scan(&pinned); e != nil || pinned != 0 {
				t.Fatal("deleted artifact still required", pinned, e)
			}
			var retained int
			if e = db.Read.QueryRow("SELECT count(*) FROM health_artifacts WHERE repair_id=?", repair.ID).Scan(&retained); e != nil || retained != len(materials) {
				t.Fatal("history removed", retained, e)
			}
			// Use the normal complete package path, not a hand-crafted manifest fixture.
			instance := NewInstance(db, s.c, InstanceBuild{"anonymous-dev", "cleanup-lifecycle"}, migrations.Files)
			instance.Inspect = store.ValidateInstanceSchema
			defer instance.Shutdown(ctx)
			backup, e := instance.CreateBackup(ctx, model.InstanceRequest{RequestKey: "after-cleanup"})
			if e != nil {
				t.Fatal(e)
			}
			if done := waitInstance(t, instance, backup.ID); done.State != "completed" {
				t.Fatal("standard backup failed", done)
			}
			root, _ := instance.root(backup.ID)
			destination := t.TempDir()
			target := config.Config{DataDir: filepath.Join(destination, "data"), ProviderKeyFile: filepath.Join(destination, "secrets/key"), ProviderCredentialsFile: filepath.Join(destination, "secrets/credentials")}
			if e = instancePrivateDir(filepath.Dir(target.ProviderKeyFile)); e != nil {
				t.Fatal(e)
			}
			in := InstanceRestoreInput{RequestKey: "cleanup-roundtrip", AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{{LibraryID: l.ID, TargetRootKey: nil}}}
			options := InstanceOfflineOptions{Inspect: store.ValidateInstanceSchema, PrepareDatabase: store.PrepareInstanceDatabase, CheckTarget: store.CheckInstanceOfflineTarget}
			for _, expected := range []int64{0, 1} {
				in.Expected = expected
				if _, e = PrepareOfflineInstanceRestore(ctx, target, instance.build, migrations.Files, filepath.Join(root, "archive.zip"), in, options); e != nil {
					t.Fatal("standard restore prepare", e)
				}
			}
			if e = activateLocked(t, target, instance.build, nil); e != nil {
				t.Fatal(e)
			}
			restored, e := store.Open(ctx, target.DataDir, migrations.Files)
			if e != nil {
				t.Fatal(e)
			}
			defer restored.Close()
			r, e := restored.HealthRepair(ctx, repair.ID)
			if e != nil || !r.MaterialsCleaned {
				t.Fatal("cleanup history not restored", r, e)
			}
			if e = restored.Read.QueryRow("SELECT count(*) FROM health_artifacts WHERE repair_id=? AND pinned=1", repair.ID).Scan(&pinned); e != nil || pinned != 0 {
				t.Fatal(pinned, e)
			}
			if _, e = os.Stat(filepath.Join(target.DataDir, "health/repairs", repair.ID, "journal.json")); e != nil {
				t.Fatal("journal not restored", e)
			}
			if _, e = restored.Edition(ctx, b.ID); e != nil {
				t.Fatal("business identity lost", e)
			}
		})
	}
}

func TestStorageTerminalWriterFilenamesAndActiveProtection(t *testing.T) {
	s, db, _, _, _ := fixture(t)
	ctx := context.Background()
	for _, op := range []struct{ id, kind, state string }{{"backup-failed", "backup", "failed"}, {"restore-cancelled", "restore", "cancelled"}, {"restore-rolled", "restore", "rolled_back"}, {"backup-active", "backup", "running"}, {"restore-active", "restore", "uploading"}} {
		if _, e := db.Write.Exec("INSERT INTO instance_operations(id,kind,request_key,state,created_at,updated_at) VALUES(?,?,?,?,1,1)", op.id, op.kind, op.id, op.state); e != nil {
			t.Fatal(e)
		}
		for _, name := range []string{"archive.partial", "upload.partial", "before-rollback/app.sqlite", "archive.zip", "unknown.bin", "pending.zip", "rollback-current/unknown.bin"} {
			storageWrite(t, s.c.DataDir, "instance-recovery/"+op.id+"/"+name, []byte("anonymous"))
		}
	}
	files, _, e := s.cleanupPreview(ctx, []string{"instance_recovery"})
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]bool{"instance-recovery/backup-failed/archive.partial": true, "instance-recovery/restore-cancelled/upload.partial": true, "instance-recovery/restore-cancelled/before-rollback/app.sqlite": true, "instance-recovery/restore-rolled/upload.partial": true, "instance-recovery/restore-rolled/before-rollback/app.sqlite": true}
	if len(files) != len(want) {
		t.Fatal(files)
	}
	for _, f := range files {
		if !want[f.Key] {
			t.Fatal("unknown or active data selected", f)
		}
	}
	storageCleanup(t, s, db, `["instance_recovery"]`)
	for _, key := range []string{"instance-recovery/backup-active/archive.partial", "instance-recovery/restore-active/upload.partial", "instance-recovery/backup-failed/archive.zip", "instance-recovery/restore-rolled/unknown.bin", "instance-recovery/restore-rolled/rollback-current/unknown.bin"} {
		if _, e = os.Stat(filepath.Join(s.c.DataDir, key)); e != nil {
			t.Fatal(key, e)
		}
	}
	// Offline rollback can lack a database row: use only the activation receipt's
	// exact RollbackCurrent members, never arbitrary children of that directory.
	m := offlineStorageFixture(t, s, "offline-rolled")
	storageWrite(t, s.c.DataDir, "instance-recovery/offline-rolled/before-rollback/app.sqlite", []byte("old"))
	storageWrite(t, s.c.DataDir, "instance-recovery/offline-rolled/before-rollback/unknown.bin", []byte("keep"))
	p := instanceActivation{ID: "offline-rolled", State: "rolled_back", RollbackFinished: true, Revision: 3, Build: m.Build, Config: instanceConfigHash(s.c), RollbackCurrent: m.Files}
	if e = writeInstanceActivation(s.c, p); e != nil {
		t.Fatal(e)
	}
	files, _, e = s.cleanupPreview(ctx, []string{"instance_recovery"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range files {
		if f.Key == "instance-recovery/offline-rolled/before-rollback/app.sqlite" {
			found = true
		}
		if f.Key == "instance-recovery/offline-rolled/before-rollback/unknown.bin" {
			t.Fatal("unknown offline member selected")
		}
	}
	if !found {
		t.Fatal("offline rollback material omitted")
	}
}

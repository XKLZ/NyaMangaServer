package maintenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func waitInstanceState(t *testing.T, s *InstanceService, id, state string) model.InstanceOperation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		v, e := s.Operation(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == state {
			return v
		}
		if v.State == "failed" {
			t.Fatalf("operation failed: %+v", v)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wanted %s, last %+v", state, v)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestInstanceValidationAbandonCancelsAndKeepsFailurePhase(t *testing.T) {
	s, _, _ := instanceFixture(t)
	ctx := context.Background()
	backup, e := s.CreateBackup(ctx, model.InstanceRequest{RequestKey: "validation-abandon-backup"})
	if e != nil {
		t.Fatal(e)
	}
	if v := waitInstance(t, s, backup.ID); v.State != "completed" {
		t.Fatalf("backup %+v", v)
	}
	archive, _, e := s.Download(ctx, backup.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer archive.Close()
	uploaded, e := s.UploadRestore(ctx, "validation-abandon-upload", archive)
	if e != nil {
		t.Fatal(e)
	}
	entered := make(chan struct{})
	s.Inspect = func(ctx context.Context, _ string, _ fs.FS) (model.InstanceInventory, error) {
		close(entered)
		<-ctx.Done()
		return model.InstanceInventory{}, ctx.Err()
	}
	validating, e := s.ValidateRestore(ctx, uploaded.ID, uploaded.Revision)
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("validation did not enter IO")
	}
	requested, e := s.AbandonRestore(ctx, uploaded.ID, validating.Revision)
	if e != nil || requested.State != "abandon_requested" {
		t.Fatalf("abandon %+v %v", requested, e)
	}
	settled := waitInstanceState(t, s, uploaded.ID, "abandoned")
	if settled.Error != "cancelled" || settled.Phase != "validating" {
		t.Fatalf("lost cancellation category/phase %+v", settled)
	}
}

func TestInstanceMappingsStatOnlyUnlinkedAndSpaceFailure(t *testing.T) {
	s, db, c := instanceFixture(t)
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "anonymous.cbz")
	if e := os.WriteFile(source, []byte("stat only, never opened"), 0000); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(source)
	if e != nil {
		t.Fatal(e)
	}
	s.c.Roots = []config.Root{{Key: "target", Name: "Anonymous", Path: root}}
	inventory := model.InstanceInventory{Roots: []model.InstanceRoot{{LibraryID: "library", RootKey: "source", Identity: "old-host"}}, Sources: []model.InstanceSource{{LibraryID: "library", EditionID: "edition", Path: "anonymous.cbz", Size: info.Size(), Mtime: 1, State: "available", Revision: 8}}}
	key := "target"
	in := InstanceRestoreInput{AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{{LibraryID: "library", TargetRootKey: &key}}}
	roots, stats, unlinked, e := s.mapRestore(ctx, inventory, in)
	if e != nil || len(roots) != 1 || len(stats) != 1 || stats[0].Mtime != info.ModTime().UnixNano() || unlinked != 0 {
		t.Fatalf("stat mapping %+v %+v %d %v", roots, stats, unlinked, e)
	}
	inventory.Sources[0].Size++
	if _, _, _, e = s.mapRestore(ctx, inventory, in); !errors.Is(e, model.ErrInstanceMapping) {
		t.Fatalf("wrong root accepted %v", e)
	}
	in.Mappings[0].TargetRootKey = nil
	if _, stats, unlinked, e = s.mapRestore(ctx, inventory, in); e != nil || len(stats) != 0 || unlinked != 1 {
		t.Fatalf("unlinked %d %v", unlinked, e)
	}
	inventory.Sources[0].Path = "../outside"
	in.Mappings[0].TargetRootKey = &key
	if _, _, _, e = s.mapRestore(ctx, inventory, in); !errors.Is(e, model.ErrInstanceMapping) {
		t.Fatalf("traversal accepted %v", e)
	}
	before, e := db.InstanceState(ctx)
	if e != nil {
		t.Fatal(e)
	}
	s.SpaceProbe = func(string) (uint64, error) { return 0, nil }
	if _, e = s.space(ctx, InstanceManifest{}, 0); !errors.Is(e, syscall.ENOSPC) {
		t.Fatalf("space failure %v", e)
	}
	after, e := db.InstanceState(ctx)
	if e != nil || before.WriteEpoch != after.WriteEpoch || after.Owner != nil {
		t.Fatalf("space failure changed target %+v %v", after, e)
	}
	if _, e = os.Stat(filepath.Join(c.DataDir, "app.sqlite")); e != nil {
		t.Fatal(e)
	}
}

func TestInstanceInterruptedRestorePreparationCanBeAbandoned(t *testing.T) {
	s, db, c := instanceFixture(t)
	ctx := context.Background()
	op, _, e := db.CreateInstanceOperation(ctx, "anonymous-interrupted-restore", "restore", "interrupted-restore-request")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.BeginInstanceProtection(ctx, op.ID, op.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = db.SetInstanceOperation(ctx, op.ID, op.Revision, "preparing", "draining", "{}", 0, "", ""); e != nil {
		t.Fatal(e)
	}
	db.Close() // equivalent durable state to hard process loss before activation journal
	reopened, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if e = reopened.RecoverInstanceOperations(ctx); e != nil {
		t.Fatal(e)
	}
	s.repo = reopened
	op, e = reopened.InstanceOperation(ctx, op.ID)
	if e != nil || op.State != "interrupted" {
		t.Fatalf("restart receipt %+v %v", op, e)
	}
	if _, e = s.AbandonRestore(ctx, op.ID, op.Revision); e != nil {
		t.Fatal(e)
	}
	state, e := reopened.InstanceState(ctx)
	if e != nil || state.Owner != nil {
		t.Fatalf("owner leak %+v %v", state, e)
	}
	health, e := reopened.HealthState(ctx)
	if e != nil || health.Phase != "idle" {
		t.Fatalf("barrier leak %+v %v", health, e)
	}
}

func TestInstanceConfirmedJournalRepairsMissingSQLReceipt(t *testing.T) {
	s, target, c, id, _ := instanceRestoreFixture(t)
	ctx := context.Background()
	v, e := target.InstanceOperation(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = target.Write.ExecContext(ctx, "UPDATE instance_operations SET state='preparing' WHERE id=?", id); e != nil {
		t.Fatal(e)
	}
	repeated, e := s.ConfirmRestore(ctx, id, model.InstanceRequest{Expected: v.Revision - 1, RequestKey: "anonymous-confirm"})
	if e != nil || repeated.ID != id || repeated.State != "awaiting_restart" {
		t.Fatalf("lost response replay %+v %v", repeated, e)
	}
	if _, e = s.AbandonRestore(ctx, id, repeated.Revision); e != nil {
		t.Fatal(e)
	}
	p, e := ReadInstanceActivation(c, s.build)
	if e != nil || p.State != "rolled_back" {
		t.Fatalf("durable cancel %+v %v", p, e)
	}
}

func TestInstanceOfflineExplicitUnhealthyReplacementPreservesAndRestoresProtection(t *testing.T) {
	source, _, _ := instanceFixture(t)
	ctx := context.Background()
	backup, e := source.CreateBackup(ctx, model.InstanceRequest{RequestKey: "unhealthy-source-backup"})
	if e != nil {
		t.Fatal(e)
	}
	if v := waitInstance(t, source, backup.ID); v.State != "completed" {
		t.Fatalf("backup %+v", v)
	}
	root, _ := source.root(backup.ID)
	archive := filepath.Join(root, "archive.zip")
	c := config.Config{DataDir: filepath.Join(t.TempDir(), "data")}
	broken, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = broken.Write.ExecContext(ctx, "UPDATE health_state SET phase='recovery_required'"); e != nil {
		t.Fatal(e)
	}
	broken.Close()
	if e = os.WriteFile(filepath.Join(c.DataDir, "health-write-unsafe"), []byte("recovery_required"), 0600); e != nil {
		t.Fatal(e)
	}
	journal := filepath.Join(c.DataDir, "health/repairs/old/journal.json")
	if e = instancePrivateDir(filepath.Dir(journal)); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(journal, []byte("anonymous failed health journal"), 0600); e != nil {
		t.Fatal(e)
	}
	in := InstanceRestoreInput{RequestKey: "explicit-unhealthy-replacement", AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{}}
	options := InstanceOfflineOptions{Inspect: store.ValidateInstanceSchema, PrepareDatabase: store.PrepareInstanceDatabase, CheckTarget: store.CheckInstanceOfflineTarget, CheckUnhealthyTarget: func(ctx context.Context, dir string) error {
		return store.CheckInstanceOfflineReplacement(ctx, dir, true)
	}}
	if _, e = PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options); !errors.Is(e, model.ErrInstanceRecovery) {
		t.Fatalf("default bypassed unhealthy target %v", e)
	}
	options.ReplaceUnhealthyTarget = true
	preview, e := PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options)
	if e != nil || !preview.ReplacesUnhealthyTarget {
		t.Fatalf("force preview %+v %v", preview, e)
	}
	in.Expected = 1
	prepared, e := PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options)
	if e != nil {
		t.Fatal(e)
	}
	again, e := PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options)
	if e != nil || again.Operation.ID != prepared.Operation.ID {
		t.Fatalf("offline idempotency %+v %v", again, e)
	}
	if e = activateLocked(t, c, source.build, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(c.DataDir, "health-write-unsafe")); !os.IsNotExist(e) {
		t.Fatal("old unsafe marker leaked into restored instance")
	}
	restored, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	state, e := restored.HealthState(ctx)
	if e != nil || state.Phase != "idle" {
		t.Fatalf("restored health %+v %v", state, e)
	}
	restored.Close()
	activation, e := ReadInstanceActivation(c, source.build)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RequestOfflineInstanceRollback(ctx, c, source.build, activation.Revision, store.InstanceOfflineEpoch); e != nil {
		t.Fatal(e)
	}
	if e = activateLocked(t, c, source.build, nil); e != nil {
		t.Fatal(e)
	}
	if marker, e := os.ReadFile(filepath.Join(c.DataDir, "health-write-unsafe")); e != nil || string(marker) != "recovery_required" {
		t.Fatalf("rollback lost unsafe marker %v", e)
	}
	if old, e := os.ReadFile(journal); e != nil || string(old) != "anonymous failed health journal" {
		t.Fatalf("rollback lost health journal %v", e)
	}
}
func instanceRestoreFixture(t *testing.T) (*InstanceService, *store.Store, config.Config, string, string) {
	t.Helper()
	source, db, sourceConfig := instanceFixture(t)
	ctx := context.Background()
	token, e := auth.RandomToken(32)
	if e != nil {
		t.Fatal(e)
	}
	oldSession, _, e := auth.New(db, token).Setup(ctx, token, "anonymous-password-123")
	if e != nil {
		t.Fatal(e)
	}
	asset := []byte("anonymous activation material")
	sum := sha256.Sum256(asset)
	key := "assets/covers/activation.bin"
	if e = instancePrivateDir(filepath.Dir(filepath.Join(sourceConfig.DataDir, key))); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(sourceConfig.DataDir, key), asset, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.ExecContext(ctx, "INSERT INTO assets VALUES('activation-asset',?,?,?,1)", key, hex.EncodeToString(sum[:]), len(asset)); e != nil {
		t.Fatal(e)
	}
	cookie := "anonymous activation cookie"
	if _, e = provider.NewVault(sourceConfig.ProviderKeyFile, sourceConfig.ProviderCredentialsFile).Credentials(provider.CredentialPatch{Expected: 1, Fields: map[string]provider.SecretChange{"proxy_password": {Op: "set", Value: &cookie}}}); e != nil {
		t.Fatal(e)
	}
	backup, e := source.CreateBackup(ctx, model.InstanceRequest{RequestKey: "source-complete-backup"})
	if e != nil {
		t.Fatal(e)
	}
	if v := waitInstance(t, source, backup.ID); v.State != "completed" {
		t.Fatalf("backup %+v", v)
	}
	archive, _, e := source.Download(ctx, backup.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer archive.Close()
	root := t.TempDir()
	c := config.Config{DataDir: filepath.Join(root, "data"), ProviderKeyFile: filepath.Join(root, "secrets/key"), ProviderCredentialsFile: filepath.Join(root, "secrets/credentials")}
	if e = instancePrivateDir(filepath.Dir(c.ProviderKeyFile)); e != nil {
		t.Fatal(e)
	}
	target, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { target.Close() })
	if _, e = target.Write.ExecContext(ctx, "INSERT INTO accounts VALUES(1,'original-target','target-hash',4,1)"); e != nil {
		t.Fatal(e)
	}
	if _, e = target.Write.ExecContext(ctx, "INSERT INTO sessions VALUES(?,'original-target',1,9999999999)", auth.HashToken("anonymous-old-target-session")); e != nil {
		t.Fatal(e)
	}
	s := NewInstance(target, c, source.build, migrations.Files)
	s.Inspect = store.ValidateInstanceSchema
	s.PrepareDatabase = store.PrepareInstanceDatabase
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	uploaded, e := s.UploadRestore(ctx, "anonymous-target-restore", archive)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ValidateRestore(ctx, uploaded.ID, uploaded.Revision); e != nil {
		t.Fatal(e)
	}
	validated := waitInstanceState(t, s, uploaded.ID, "validated")
	preview, e := s.PreviewRestore(ctx, uploaded.ID, InstanceRestoreInput{Expected: validated.Revision, AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConfirmRestore(ctx, uploaded.ID, model.InstanceRequest{Expected: preview.Operation.Revision, RequestKey: "anonymous-confirm"}); e != nil {
		t.Fatal(e)
	}
	return s, target, c, uploaded.ID, oldSession
}
func activateLocked(t *testing.T, c config.Config, build InstanceBuild, fault func(string, int) error) error {
	t.Helper()
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	return ActivateInstance(context.Background(), c, build, InstanceActivationOptions{Epoch: store.InstanceOfflineEpoch, CheckRollback: store.CheckInstanceRollbackIdentity, FinishRollback: store.FinishInstanceRollback, Fault: fault, ValidatePrepared: func(ctx context.Context, path string, roots []model.InstanceRebase) error {
		inventory, e := store.ValidateInstanceSchema(ctx, path, migrations.Files)
		if e != nil {
			return e
		}
		return ValidatePreparedInstanceRoots(ctx, c, inventory, roots)
	}})
}
func TestInstanceRestoreLoginCheckpointAndControlledRollback(t *testing.T) {
	s, target, c, id, oldSession := instanceRestoreFixture(t)
	ctx := context.Background()
	target.Close()
	if e := activateLocked(t, c, s.build, nil); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := restored.Account(ctx)
	if e != nil || identity.ID == "original-target" {
		t.Fatalf("source account not restored %+v %v", identity, e)
	}
	login := auth.New(restored, "")
	if _, e = login.Session(ctx, oldSession); e == nil {
		t.Fatal("old source session survived")
	}
	if _, _, e = login.Login(ctx, "anonymous-password-123"); e != nil {
		t.Fatal(e)
	}
	state, e := restored.InstanceState(ctx)
	if e != nil || !state.BackgroundHold || state.WriteEpoch != 0 {
		t.Fatalf("post login state %+v %v", state, e)
	}
	if allowed, e := restored.AutomaticWorkAllowed(ctx); e != nil || allowed {
		t.Fatalf("automatic work admitted %v %v", allowed, e)
	}
	if e = restored.Close(); e != nil {
		t.Fatal(e)
	}
	p, e := ReadInstanceActivation(c, s.build)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RequestOfflineInstanceRollback(ctx, c, s.build, p.Revision, store.InstanceOfflineEpoch); e != nil {
		t.Fatal(e)
	}
	if e = activateLocked(t, c, s.build, nil); e != nil {
		t.Fatal(e)
	}
	prior, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer prior.Close()
	account, e := prior.Account(ctx)
	if e != nil || account.ID != "original-target" {
		t.Fatalf("rollback account %+v %v", account, e)
	}
	if _, e = auth.New(prior, "").Session(ctx, "anonymous-old-target-session"); e == nil {
		t.Fatal("rollback resurrected an old target session")
	}
	health, e := prior.HealthState(ctx)
	if e != nil || health.Phase != "idle" {
		t.Fatalf("target barrier not released %+v %v", health, e)
	}
	if p, e = ReadInstanceActivation(c, s.build); e != nil || p.ID != id || p.State != "rolled_back" {
		t.Fatalf("journal %+v %v", p, e)
	}
}

func TestInstanceEveryInstallBoundaryCanContinueOrRollback(t *testing.T) {
	for _, stage := range []string{"before_replace", "after_replace"} {
		for ordinal := 0; ordinal < 4; ordinal++ {
			t.Run(fmt.Sprintf("%s_%d", stage, ordinal), func(t *testing.T) {
				s, target, c, _, _ := instanceRestoreFixture(t)
				target.Close()
				injected := errors.New("anonymous boundary interruption")
				fault := func(point string, index int) error {
					if point == stage && index == ordinal {
						return injected
					}
					return nil
				}
				if e := activateLocked(t, c, s.build, fault); !errors.Is(e, injected) {
					t.Fatalf("boundary not reached: %v", e)
				}
				if ordinal%2 == 0 {
					if e := activateLocked(t, c, s.build, nil); e != nil {
						t.Fatal(e)
					}
					p, e := ReadInstanceActivation(c, s.build)
					if e != nil || p.State != "completed" {
						t.Fatalf("continue %+v %v", p, e)
					}
				} else {
					p, e := ReadInstanceActivation(c, s.build)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = RequestOfflineInstanceRollback(context.Background(), c, s.build, p.Revision, store.InstanceOfflineEpoch); e != nil {
						t.Fatal(e)
					}
					if e = activateLocked(t, c, s.build, nil); e != nil {
						t.Fatal(e)
					}
					old, e := store.Open(context.Background(), c.DataDir, migrations.Files)
					if e != nil {
						t.Fatal(e)
					}
					defer old.Close()
					account, e := old.Account(context.Background())
					if e != nil || account.ID != "original-target" {
						t.Fatalf("rollback %+v %v", account, e)
					}
				}
			})
		}
	}
}

func TestInstanceTerminalJournalAllowsFutureBuildAndConfigButRefusesOldRollback(t *testing.T) {
	s, target, c, _, _ := instanceRestoreFixture(t)
	target.Close()
	if e := activateLocked(t, c, s.build, nil); e != nil {
		t.Fatal(e)
	}
	changed := c
	changed.Listen = "127.0.0.1:19000"
	changed.PublicURL = "http://127.0.0.1:19000"
	newBuild := InstanceBuild{Version: s.build.Version, Commit: "future-anonymous-build"}
	if e := activateLocked(t, changed, newBuild, nil); e != nil {
		t.Fatalf("terminal history blocked ordinary upgrade/config change: %v", e)
	}
	p, e := ReadInstanceActivation(changed, newBuild)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RequestOfflineInstanceRollback(context.Background(), changed, newBuild, p.Revision, store.InstanceOfflineEpoch); !errors.Is(e, model.ErrInstanceBuild) {
		t.Fatalf("old rollback crossed build/config boundary: %v", e)
	}
}
func TestInstanceActivationCrashAfterDatabaseSwitchAndNewWritesRefuseRollback(t *testing.T) {
	s, target, c, _, _ := instanceRestoreFixture(t)
	target.Close()
	injected := errors.New("anonymous crash")
	fault := func(stage string, index int) error {
		p, e := loadInstanceActivation(c, s.build)
		if e != nil {
			return e
		}
		if stage == "after_replace" && p.Keys[index] == "app.sqlite" {
			return injected
		}
		return nil
	}
	if e := activateLocked(t, c, s.build, fault); !errors.Is(e, injected) {
		t.Fatalf("fault not reached %v", e)
	}
	if e := activateLocked(t, c, s.build, nil); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(context.Background(), c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restored.Write.Exec("UPDATE accounts SET auth_revision=auth_revision+1"); e != nil {
		t.Fatal(e)
	}
	restored.Close()
	p, e := ReadInstanceActivation(c, s.build)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = RequestOfflineInstanceRollback(context.Background(), c, s.build, p.Revision, store.InstanceOfflineEpoch); !errors.Is(e, model.ErrConflict) {
		t.Fatalf("overwrote newer business state %v", e)
	}
}
func TestInstanceOfflineRestoreEmptyCorruptAndLockedTargets(t *testing.T) {
	source, _, _ := instanceFixture(t)
	ctx := context.Background()
	backup, e := source.CreateBackup(ctx, model.InstanceRequest{RequestKey: "anonymous-offline-backup"})
	if e != nil {
		t.Fatal(e)
	}
	if v := waitInstance(t, source, backup.ID); v.State != "completed" {
		t.Fatalf("backup %+v", v)
	}
	root, _ := source.root(backup.ID)
	archive := filepath.Join(root, "archive.zip")
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "corrupt"}[corrupt], func(t *testing.T) {
			c := config.Config{DataDir: filepath.Join(t.TempDir(), "data")}
			if e := instancePrivateDir(c.DataDir); e != nil {
				t.Fatal(e)
			}
			if corrupt {
				if e := os.WriteFile(filepath.Join(c.DataDir, "app.sqlite"), []byte("anonymous corrupt original"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			options := InstanceOfflineOptions{Inspect: store.ValidateInstanceSchema, PrepareDatabase: store.PrepareInstanceDatabase, CheckTarget: store.CheckInstanceOfflineTarget}
			lock, e := appdata.Acquire(c.DataDir)
			if e != nil {
				t.Fatal(e)
			}
			in := InstanceRestoreInput{Expected: 1, RequestKey: "offline-explicit-confirm", AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{}}
			if _, e = PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options); e == nil {
				t.Fatal("live target lock ignored")
			}
			lock.Close()
			in.Expected = 0
			if _, e = PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options); e != nil {
				t.Fatal(e)
			}
			in.Expected = 1
			result, e := PrepareOfflineInstanceRestore(ctx, c, source.build, migrations.Files, archive, in, options)
			if e != nil {
				t.Fatal(e)
			}
			if e = activateLocked(t, c, source.build, nil); e != nil {
				t.Fatal(e)
			}
			recovered, e := store.Open(ctx, c.DataDir, migrations.Files)
			if e != nil {
				t.Fatal(e)
			}
			recovered.Close()
			if corrupt {
				before, e := os.ReadFile(filepath.Join(c.DataDir, "instance-recovery", result.Operation.ID, "before-activation/app.sqlite"))
				if e != nil || string(before) != "anonymous corrupt original" {
					t.Fatalf("corrupt target not preserved %v", e)
				}
			}
		})
	}
}

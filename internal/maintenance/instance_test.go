package maintenance

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

func instanceFixture(t *testing.T) (*InstanceService, *store.Store, config.Config) {
	t.Helper()
	root := t.TempDir()
	c := config.Config{DataDir: filepath.Join(root, "data"), ProviderKeyFile: filepath.Join(root, "secrets/key"), ProviderCredentialsFile: filepath.Join(root, "secrets/credentials")}
	db, e := store.Open(context.Background(), c.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	service := NewInstance(db, c, InstanceBuild{"anonymous-dev", "fixed-anonymous-build"}, migrations.Files)
	service.Inspect = store.ValidateInstanceSchema
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := service.Shutdown(ctx); e != nil {
			t.Error(e)
		}
	})
	return service, db, c
}
func waitInstance(t *testing.T, s *InstanceService, id string) model.InstanceOperation {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		v, e := s.Operation(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == "completed" || v.State == "failed" || v.State == "cancelled" {
			return v
		}
		select {
		case <-ctx.Done():
			t.Fatal("operation did not settle")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func TestInstanceBackupSnapshotSecretsAndIndependentCompression(t *testing.T) {
	s, db, c := instanceFixture(t)
	ctx := context.Background()
	raw := []byte("anonymous retained asset")
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	key := "assets/covers/anonymous.bin"
	if e := instancePrivateDir(filepath.Dir(filepath.Join(c.DataDir, key))); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(c.DataDir, key), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Write.ExecContext(ctx, "INSERT INTO accounts VALUES(1,'account','opaque-password-hash',7,1);INSERT INTO assets VALUES('asset',?,?,?,1)", key, digest, len(raw)); e != nil {
		t.Fatal(e)
	}
	vault := provider.NewVault(c.ProviderKeyFile, c.ProviderCredentialsFile)
	cookie := "anonymous-secret"
	if _, e := vault.Credentials(provider.CredentialPatch{Expected: 1, Fields: map[string]provider.SecretChange{"ipb_pass_hash": {Op: "set", Value: &cookie}}}); e != nil {
		t.Fatal(e)
	}
	if e := instancePrivateDir(filepath.Join(c.DataDir, "upgrade-snapshots")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(c.DataDir, "upgrade-snapshots/excluded.sqlite"), []byte("excluded"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := instancePrivateDir(filepath.Join(c.DataDir, "instance-recovery/old")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(c.DataDir, "instance-recovery/old/archive.zip"), []byte("excluded"), 0600); e != nil {
		t.Fatal(e)
	}
	v, e := s.CreateBackup(ctx, model.InstanceRequest{RequestKey: "anonymous-backup-1"})
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.CreateBackup(ctx, model.InstanceRequest{RequestKey: "anonymous-backup-1"})
	if e != nil || again.ID != v.ID {
		t.Fatalf("idempotency: %+v %v", again, e)
	}
	done := waitInstance(t, s, v.ID)
	if done.State != "completed" {
		t.Fatalf("backup: %+v", done)
	}
	state, e := db.HealthState(ctx)
	if e != nil || state.Phase != "idle" {
		t.Fatalf("barrier: %+v %v", state, e)
	}
	archive, _, e := s.Download(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	archive.Close()
	root, _ := s.root(v.ID)
	stage := filepath.Join(t.TempDir(), "validated")
	m, e := readInstancePackage(ctx, filepath.Join(root, "archive.zip"), stage, s.build, s.budget)
	if e != nil {
		t.Fatal(e)
	}
	if m.Inventory.Counts["accounts"] != 1 || m.Inventory.Version != db.Version || len(m.Files) != 4 {
		t.Fatalf("manifest: %+v", m)
	}
	if e = instanceRequired(m); e != nil {
		t.Fatal(e)
	}
	if ok, e := provider.ValidateBackupPair(filepath.Join(stage, "vault/key"), filepath.Join(stage, "vault/credentials")); !ok || e != nil {
		t.Fatalf("vault pair: %v %v", ok, e)
	}
	if b, e := os.ReadFile(filepath.Join(stage, key)); e != nil || !bytes.Equal(b, raw) {
		t.Fatalf("retained asset: %v", e)
	}
	if _, e = readInstancePackage(ctx, filepath.Join(root, "archive.zip"), filepath.Join(t.TempDir(), "rejected"), InstanceBuild{"anonymous-dev", "different-build"}, s.budget); !errors.Is(e, model.ErrInstanceBuild) {
		t.Fatalf("wrong build: %v", e)
	}
}

func TestInstancePackageRejectsHostileMembersAndBudget(t *testing.T) {
	cases := []struct {
		name, key string
		mode      os.FileMode
		duplicate bool
	}{
		{"traversal", "../outside", 0600, false}, {"absolute", "/outside", 0600, false}, {"backslash", "assets\\escape", 0600, false},
		{"symlink", "assets/link", os.ModeSymlink | 0777, false}, {"duplicate", "assets/a", 0600, true}, {"directory", "assets/", os.ModeDir | 0700, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.zip")
			f, e := os.Create(path)
			if e != nil {
				t.Fatal(e)
			}
			z := zip.NewWriter(f)
			h := &zip.FileHeader{Name: tc.key, Method: zip.Store}
			h.SetMode(tc.mode)
			w, e := z.CreateHeader(h)
			if e != nil {
				t.Fatal(e)
			}
			w.Write([]byte("bad"))
			if tc.duplicate {
				z.CreateHeader(h)
			}
			w, _ = z.Create("manifest.json")
			json.NewEncoder(w).Encode(InstanceManifest{})
			z.Close()
			f.Close()
			_, e = readInstancePackage(context.Background(), path, filepath.Join(t.TempDir(), "stage"), InstanceBuild{"dev", "build"}, DefaultInstanceBudget())
			if !errors.Is(e, model.ErrInstancePackage) {
				t.Fatalf("accepted %v", e)
			}
		})
	}
	m := InstanceManifest{Format: 1, Application: "nyamangaserver", Baseline: "foundation-v1", Build: InstanceBuild{"dev", "build"}, Secrets: true, Files: []model.InstanceFile{{Key: "app.sqlite", Size: 2, SHA256: string(bytes.Repeat([]byte("0"), 64))}}}
	b := DefaultInstanceBudget()
	b.ExpandedBytes = 1
	if e := validateInstanceMembers(m, b); !errors.Is(e, model.ErrInstancePackage) {
		t.Fatalf("budget: %v", e)
	}
}

func TestInstanceModelWeightsNeverEnterManagedBackupWalk(t *testing.T) {
	s, _, c := instanceFixture(t)
	s.c.WorkRecommendations.ModelsDir = filepath.Join(c.DataDir, "indexes/recommendations/models")
	weight := filepath.Join(s.c.WorkRecommendations.ModelsDir, "anonymous.onnx")
	if e := os.MkdirAll(filepath.Dir(weight), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(weight, []byte("anonymous weights must not be opened"), 0000); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreateBackup(context.Background(), model.InstanceRequest{RequestKey: "refuse-model-overlap"}); !errors.Is(e, model.ErrInstanceRecovery) {
		t.Fatalf("ambiguous model layout entered backup %v", e)
	}
	if info, e := os.Stat(weight); e != nil || info.Mode().Perm() != 0 || info.Size() != int64(len("anonymous weights must not be opened")) {
		t.Fatalf("managed model file changed: %v", e)
	}
	s.c.WorkRecommendations.ModelsDir = filepath.Join(t.TempDir(), "models")
	if e := instanceModelLayout(s.c); e != nil {
		t.Fatalf("separate read-only model root rejected %v", e)
	}
	protected := filepath.Join(c.DataDir, "assets/source/anonymous.cbz")
	if e := os.MkdirAll(filepath.Dir(protected), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(protected, []byte("anonymous source must remain unchanged"), 0000); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"source", "python", "worker"} {
		t.Run(kind, func(t *testing.T) {
			bad := c
			switch kind {
			case "source":
				bad.Roots = []config.Root{{Key: "bad", Name: "Anonymous", Path: filepath.Dir(protected)}}
			case "python":
				bad.WorkRecommendations.Python = filepath.Join(c.DataDir, "cache/venv/bin/python")
			case "worker":
				bad.WorkRecommendations.Worker = filepath.Join(c.DataDir, "assets/worker.py")
			}
			service := NewInstance(s.repo, bad, s.build, s.files)
			if _, e := service.CreateBackup(context.Background(), model.InstanceRequest{RequestKey: "protected-path-" + kind}); !errors.Is(e, model.ErrInstanceRecovery) {
				t.Fatalf("source/program entered backup: %v", e)
			}
			if e := instanceVaultTargetPreflight(bad, InstanceManifest{}); !errors.Is(e, model.ErrInstanceRecovery) {
				t.Fatalf("restore accepted protected layout: %v", e)
			}
			info, e := os.Stat(protected)
			if e != nil || info.Size() != int64(len("anonymous source must remain unchanged")) || info.Mode().Perm() != 0 {
				t.Fatalf("protected source altered: %v", e)
			}
		})
	}
}

func TestInstanceRestoreRefusesReadOnlySecretFileAndParentBeforeReplacement(t *testing.T) {
	_, _, c := instanceFixture(t)
	parent := filepath.Dir(c.ProviderKeyFile)
	if e := os.MkdirAll(parent, 0700); e != nil {
		t.Fatal(e)
	}
	key := []byte("01234567890123456789012345678901")
	if e := os.WriteFile(c.ProviderKeyFile, key, 0400); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(c.ProviderCredentialsFile, []byte("anonymous target ciphertext"), 0600); e != nil {
		t.Fatal(e)
	}
	m := InstanceManifest{Files: []model.InstanceFile{{Key: "vault/key"}, {Key: "vault/credentials"}}}
	if e := instanceVaultTargetPreflight(c, m); !errors.Is(e, os.ErrPermission) {
		t.Fatalf("read-only target secret accepted %v", e)
	}
	if old, e := os.ReadFile(c.ProviderKeyFile); e != nil || !bytes.Equal(old, key) {
		t.Fatalf("read-only secret changed %v", e)
	}
	if e := os.Chmod(c.ProviderKeyFile, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(parent, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(parent, 0700)
	if e := instanceVaultTargetPreflight(c, m); !errors.Is(e, os.ErrPermission) {
		t.Fatalf("read-only target secret directory accepted %v", e)
	}
}

func TestInstanceProtectionPausesAndRejectsUnrelatedRelease(t *testing.T) {
	s, db, _ := instanceFixture(t)
	ctx := context.Background()
	if _, e := db.Write.ExecContext(ctx, "INSERT INTO jobs(id,type,state,stage,generation,created_at,updated_at) VALUES('queued','maintenance','queued','queued',1,1,1),('paused','maintenance','paused','queued',3,1,1)"); e != nil {
		t.Fatal(e)
	}
	op, _, e := db.CreateInstanceOperation(ctx, "protection", "backup", "anonymous-protection")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.BeginInstanceProtection(ctx, op.ID, op.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Write.ExecContext(ctx, "INSERT INTO accounts VALUES(1,'account','opaque',1,1)"); e == nil {
		t.Fatal("barrier admitted business update")
	}
	if e = db.EndInstanceProtection(ctx, "other"); !errors.Is(e, model.ErrConflict) {
		t.Fatalf("wrong owner %v", e)
	}
	if _, e = db.BeginHealthIO(ctx); e == nil {
		t.Fatal("IO should be blocked")
	}
	if _, e = s.Cancel(ctx, op.ID, op.Revision); e != nil {
		t.Fatal(e)
	}
	var state string
	var generation int
	if e = db.Read.QueryRowContext(ctx, "SELECT state,generation FROM jobs WHERE id='queued'").Scan(&state, &generation); e != nil || state != "paused" || generation != 2 {
		t.Fatalf("queued: %s %d %v", state, generation, e)
	}
	if e = db.Read.QueryRowContext(ctx, "SELECT state,generation FROM jobs WHERE id='paused'").Scan(&state, &generation); e != nil || state != "paused" || generation != 3 {
		t.Fatalf("paused changed: %s %d %v", state, generation, e)
	}
}

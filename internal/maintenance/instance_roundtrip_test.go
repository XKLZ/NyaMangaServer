package maintenance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func instanceLogicalRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, e := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			t.Fatal(e)
		}
		names = append(names, n)
	}
	rows.Close()
	out := map[string]string{}
	for _, name := range names {
		if strings.HasPrefix(name, "instance_") || name == "sessions" || name == "device_sessions" || name == "events" || name == "health_state" {
			continue
		}
		query := fmt.Sprintf("SELECT * FROM %q", name)
		if name == "libraries" {
			query = "SELECT id,display_name,legacy_zip_encoding,scan_interval_minutes,next_scan_at,revision,last_complete_scan_id,last_error,disabled,trashed_at,scan_actor_id FROM libraries"
		}
		rows, e := db.Query(query)
		if e != nil {
			t.Fatal(e)
		}
		cols, e := rows.Columns()
		if e != nil {
			t.Fatal(e)
		}
		records := []string{}
		for rows.Next() {
			values := make([]any, len(cols))
			pointers := make([]any, len(cols))
			for i := range values {
				pointers[i] = &values[i]
			}
			if e = rows.Scan(pointers...); e != nil {
				t.Fatal(e)
			}
			raw, e := json.Marshal(values)
			if e != nil {
				t.Fatal(e)
			}
			records = append(records, string(raw))
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		rows.Close()
		sort.Strings(records)
		raw, _ := json.Marshal(records)
		out[name] = string(raw)
	}
	return out
}
func TestInstanceFullNonemptyBusinessRoundtripLogicalIdentity(t *testing.T) {
	s, db, c := instanceFixture(t)
	ctx := context.Background()
	token, e := auth.RandomToken(32)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = auth.New(db, token).Setup(ctx, token, "anonymous recovery password"); e != nil {
		t.Fatal(e)
	}
	source := t.TempDir()
	if e = testfixture.SeedInstanceRecovery(ctx, db.Write, c.DataDir, source, "fixture"); e != nil {
		t.Fatal(e)
	}
	sourceClient, e := db.ClientIdentity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	deviceToken, _, e := auth.New(db, "").LoginDevice(ctx, "fixture-viewer", "anonymous recovery password", "Fixture device", "fixture")
	if e != nil {
		t.Fatal(e)
	}

	vault := provider.NewVault(c.ProviderKeyFile, c.ProviderCredentialsFile)
	cookie := "anonymous-cookie"
	if _, e = vault.Credentials(provider.CredentialPatch{Expected: 1, Fields: map[string]provider.SecretChange{"ipb_pass_hash": {Op: "set", Value: &cookie}}}); e != nil {
		t.Fatal(e)
	}
	before := instanceLogicalRows(t, db.Read)
	nonempty := []string{"account_profiles", "account_library_grants", "reading_states", "reading_sessions", "reading_state_requests", "works", "editions", "files", "series", "series_members", "external_metadata", "edition_sources", "field_overrides", "edition_label_policy", "edition_version_labels", "tag_follows", "tag_images", "recommendation_materials", "import_candidates", "import_members", "work_proposals", "work_proposal_decisions", "series_match_feedback", "health_findings", "health_artifacts", "gallery_highlight_connections", "job_items", "scan_entries"}
	for _, name := range nonempty {
		if before[name] == "[]" || before[name] == "" {
			t.Fatalf("retention category was empty: %s", name)
		}
	}
	backup, e := s.CreateBackup(ctx, model.InstanceRequest{RequestKey: "full-business-roundtrip"})
	if e != nil {
		t.Fatal(e)
	}
	if v := waitInstance(t, s, backup.ID); v.State != "completed" {
		t.Fatalf("backup %+v", v)
	}
	root, _ := s.root(backup.ID)
	archive := filepath.Join(root, "archive.zip")
	targetRoot := t.TempDir()
	target := config.Config{DataDir: filepath.Join(targetRoot, "data"), ProviderKeyFile: filepath.Join(targetRoot, "secrets/key"), ProviderCredentialsFile: filepath.Join(targetRoot, "secrets/credentials")}
	if e = instancePrivateDir(filepath.Dir(target.ProviderKeyFile)); e != nil {
		t.Fatal(e)
	}
	in := InstanceRestoreInput{RequestKey: "full-business-offline", AcknowledgeStatOnly: true, Mappings: []model.InstanceRootMapping{{LibraryID: "fixture-library", TargetRootKey: nil}}}
	options := InstanceOfflineOptions{Inspect: store.ValidateInstanceSchema, PrepareDatabase: store.PrepareInstanceDatabase, CheckTarget: store.CheckInstanceOfflineTarget}
	if _, e = PrepareOfflineInstanceRestore(ctx, target, s.build, migrations.Files, archive, in, options); e != nil {
		t.Fatal(e)
	}
	in.Expected = 1
	if _, e = PrepareOfflineInstanceRestore(ctx, target, s.build, migrations.Files, archive, in, options); e != nil {
		t.Fatal(e)
	}
	if e = activateLocked(t, target, s.build, nil); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(ctx, target.DataDir, migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	after := instanceLogicalRows(t, restored.Read)
	var oldSessions int
	if e = restored.Read.QueryRow("SELECT count(*) FROM sessions").Scan(&oldSessions); e != nil || oldSessions != 0 {
		t.Fatal("restore retained sessions", oldSessions, e)
	}
	login := auth.New(restored, "")
	restoredClient, e := restored.ClientIdentity(ctx)
	if e != nil || restoredClient.InstanceID != sourceClient.InstanceID || restoredClient.CacheEpoch == sourceClient.CacheEpoch {
		t.Fatal("restored client identity", restoredClient, e)
	}
	if _, e = login.DeviceSession(ctx, deviceToken); !errors.Is(e, model.ErrDeviceSession) {
		t.Fatal("source device credential retained", e)
	}

	_, viewerSession, e := login.LoginUsername(ctx, "fixture-viewer", "anonymous recovery password")
	if e != nil || viewerSession.Account == nil || viewerSession.Account.MustChangePassword {
		t.Fatal("restored ordinary login", viewerSession, e)
	}
	_, forcedSession, e := login.LoginUsername(ctx, "fixture-editor", "anonymous recovery password")
	if e != nil || forcedSession.Account == nil || !forcedSession.Account.MustChangePassword {
		t.Fatal("restored forced password login", forcedSession, e)
	}
	reader := model.WithActor(ctx, *viewerSession.Account)
	reading, e := restored.ReadingState(reader, "fixture-edition-0")
	if e != nil || reading.ReadCount != 3 || !reading.Completed || reading.Revision != 4 {
		t.Fatal("restored reading", reading, e)
	}

	for name, rows := range before {
		if after[name] != rows {
			t.Errorf("logical data changed: %s\nbefore=%s\nafter=%s", name, rows, after[name])
		}
	}
	for _, key := range []string{"assets/tags/fixture/retained.png", "cache/covers/v1/fixture/retained.png", "cache/tag-images/v1/fixture/retained.png", "cache/import-previews/v1/fixture/retained.png", "cache/tag-translations/fixture.txt", "indexes/recommendations/features/phash/fixture.json", "indexes/recommendations/series/fixture/retained.json.gz", "health/repairs/fixture-health/file-0000.bin", "health/storage-original-verification.json"} {
		old, e := os.ReadFile(filepath.Join(c.DataDir, key))
		if e != nil {
			t.Fatal(e)
		}
		now, e := os.ReadFile(filepath.Join(target.DataDir, key))
		if e != nil || string(old) != string(now) {
			t.Errorf("material not retained: %s %v", key, e)
		}
	}
	sourceSettings, e := vault.Settings()
	if e != nil {
		t.Fatal(e)
	}
	newSettings, e := provider.NewVault(target.ProviderKeyFile, target.ProviderCredentialsFile).Settings()
	if e != nil || sourceSettings.Revision != newSettings.Revision || !newSettings.Configured["ipb_pass_hash"] {
		t.Fatalf("vault state %+v %v", newSettings, e)
	}
	state, e := restored.InstanceState(ctx)
	if e != nil || !state.BackgroundHold || state.WriteEpoch != 0 {
		t.Fatalf("restored did background writes %+v %v", state, e)
	}
	var restoreID string
	if e = restored.Read.QueryRow("SELECT id FROM instance_operations WHERE kind='restore' AND state='completed' LIMIT 1").Scan(&restoreID); e != nil {
		t.Fatal(e)
	}
	databasePath := filepath.Join(target.DataDir, "app.sqlite")
	if e = store.CheckInstanceRollbackIdentity(ctx, databasePath, restoreID); e != nil {
		t.Fatal("initial rollback checkpoint", e)
	}
	if _, e = restored.WriteReadingState(reader, "fixture-edition-0", model.ReadingWrite{Expected: 4, ContentRevision: 7, IdempotencyKey: "post-restore-reading", Action: "heartbeat"}); e != nil {
		t.Fatal(e)
	}
	if e = store.CheckInstanceRollbackIdentity(ctx, databasePath, restoreID); !errors.Is(e, model.ErrConflict) {
		t.Fatal("reading write allowed old rollback", e)
	}

}

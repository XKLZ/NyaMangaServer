package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/migrations"
)

func TestOfflineMaintenanceRequiresCurrentCompleteLedger(t *testing.T) {
	for _, variant := range []string{"current", "missing", "future", "checksum", "gap"} {
		t.Run(variant, func(t *testing.T) {
			db, dir := openTest(t)
			var statement string
			switch variant {
			case "missing":
				statement = "DELETE FROM schema_migrations WHERE version=(SELECT max(version) FROM schema_migrations)"
			case "future":
				statement = "INSERT INTO schema_migrations SELECT max(version)+1,'unknown',1 FROM schema_migrations"
			case "checksum":
				statement = "UPDATE schema_migrations SET checksum='changed' WHERE version=1"
			case "gap":
				statement = "UPDATE schema_migrations SET version=1000 WHERE version=1"
			}
			if statement != "" {
				if _, e := db.Write.Exec(statement); e != nil {
					t.Fatal(e)
				}
			}
			before := dumpRows(t, db, "SELECT * FROM schema_migrations ORDER BY version")
			_, e := MigrationOriginalAssets(context.Background(), dir, migrations.Files)
			if variant == "current" {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				if !errors.Is(e, model.ErrInput) {
					t.Fatal("unexpected ledger accepted or misclassified", e)
				}
				if _, e = ReconcileCleanup(context.Background(), dir, "anonymous-job", false, migrations.Files); !errors.Is(e, model.ErrInput) {
					t.Fatal("reconciliation accepted incompatible ledger", e)
				}
			}
			if !reflect.DeepEqual(before, dumpRows(t, db, "SELECT * FROM schema_migrations ORDER BY version")) {
				t.Fatal("offline inspection changed the migration ledger")
			}
		})
	}
}

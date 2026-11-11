package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"nyamangaserver.local/server/internal/model"
)

func TestInstanceHistoryExactRequestKeyBeforePagination(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	before, e := s.InstanceState(ctx)
	if e != nil {
		t.Fatal(e)
	}
	for _, key := range []string{"anonymous-absent", strings.Repeat("k", 8), strings.Repeat("k", 128), "' OR 1=1 --"} {
		items, e := s.InstanceOperations(ctx, "restore", 1, 1, key)
		if e != nil || len(items) != 0 || items == nil {
			t.Fatalf("missing key lookup: %+v %v", items, e)
		}
	}
	var count int
	if e = s.Read.QueryRowContext(ctx, "SELECT count(*) FROM instance_operations").Scan(&count); e != nil || count != 0 {
		t.Fatalf("GET created operations: %d %v", count, e)
	}
	after, e := s.InstanceState(ctx)
	if e != nil || before != after {
		t.Fatalf("GET changed state: %+v %v", after, e)
	}
	for _, row := range []struct{ id, kind, key string }{
		{"z-restore", "restore", "anonymous-upload-1"},
		{"a-restore", "restore", "anonymous-upload-10"},
		{"backup", "backup", "anonymous-backup-1"},
	} {
		if _, _, e = s.CreateInstanceOperation(ctx, row.id, row.kind, row.key); e != nil {
			t.Fatal(e)
		}
	}
	// Force the desired record behind another row in unfiltered page one.
	if _, e = s.Write.ExecContext(ctx, "UPDATE instance_operations SET created_at=1"); e != nil {
		t.Fatal(e)
	}
	items, e := s.InstanceOperations(ctx, "restore", 1, 1, "anonymous-upload-1")
	if e != nil || len(items) != 1 || items[0].ID != "z-restore" {
		t.Fatalf("filter was not exact/before limit: %+v %v", items, e)
	}
	for _, tc := range []struct {
		kind, key string
		page      int
	}{
		{"backup", "anonymous-upload-1", 1}, {"restore", "anonymous-backup-1", 1},
		{"restore", "anonymous-upload", 1}, {"restore", "ANONYMOUS-upload-1", 1},
		{"restore", "anonymous-upload-1", 2},
	} {
		items, e := s.InstanceOperations(ctx, tc.kind, tc.page, 1, tc.key)
		if e != nil || len(items) != 0 {
			t.Fatalf("kind/key/page isolation: %+v %v", items, e)
		}
	}
	for _, keys := range [][]string{{""}, {"short"}, {strings.Repeat("x", 129)}, {"anonymous-key-1", "anonymous-key-2"}} {
		if _, e = s.InstanceOperations(ctx, "restore", 1, 50, keys...); !errors.Is(e, model.ErrInput) {
			t.Fatalf("invalid key accepted: %v", e)
		}
	}
	items, e = s.InstanceOperations(ctx, "restore", 1, 50)
	if e != nil || len(items) != 2 {
		t.Fatalf("unfiltered history changed: %+v %v", items, e)
	}
}

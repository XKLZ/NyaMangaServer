package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/tagimages"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTagImagesCleanupReferencesCacheAndConcurrentPublication(t *testing.T) {
	s, db, cat, _, _ := fixture(t)
	ctx := context.Background()
	if _, e := db.Write.Exec("INSERT INTO tags VALUES('tag','character','Anonymous')"); e != nil {
		t.Fatal(e)
	}
	images := tagimages.New(db, s.c, cat.Pool)
	s.WithTagImages(images)
	var b bytes.Buffer
	png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 2, 3)))
	raw := b.Bytes()
	fg, e := images.Upload(ctx, "tag", "foreground", 0, "image/png", raw)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = images.Content(ctx, "tag", "foreground", 1); e != nil {
		t.Fatal(e)
	}
	asset, e := db.Asset(ctx, *fg.AssetID)
	if e != nil {
		t.Fatal(e)
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(s.c.DataDir, asset.Key), old, old)
	// Freeze an old orphan, then publish a reference before executing the plan.
	orphanKey := "assets/tags/ab/anonymous-orphan.png"
	orphan := filepath.Join(s.c.DataDir, orphanKey)
	os.MkdirAll(filepath.Dir(orphan), 0700)
	os.WriteFile(orphan, raw, 0600)
	os.Chtimes(orphan, old, old)
	plan, e := s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["tag_images","orphan_assets"]}`)})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(plan.Input.Options), orphanKey) || strings.Contains(string(plan.Input.Options), asset.Key) {
		t.Fatal("preview reference protection", string(plan.Input.Options))
	}
	if _, e = db.SetTagImage(ctx, "tag", "background", 0, model.TagImage{Width: 2, Height: 3, Format: "png", Asset: &model.Asset{ID: "became-referenced", Key: orphanKey, Digest: asset.Digest, Size: int64(len(raw))}}); e != nil {
		t.Fatal(e)
	}
	j, e := s.Execute(ctx, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	// The same storage lock guards active HTTP decode/read and maintenance removal.
	unlock := images.LockStorage()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() { close(started); done <- s.cleanupFrozen(ctx, *claimed, plan.Input.Options) }()
	<-started
	select {
	case e := <-done:
		t.Fatal("cleanup bypassed active lock", e)
	default:
	}
	unlock()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(orphan); e != nil {
		t.Fatal("reference added after preview was removed", e)
	}
	if _, e = os.Stat(filepath.Join(s.c.DataDir, asset.Key)); e != nil {
		t.Fatal("foreground asset removed", e)
	}
	if _, _, e = images.Content(ctx, "tag", "foreground", 1); e != nil {
		t.Fatal("cache did not rebuild", e)
	}
	state, _ := images.State(ctx, "tag")
	if state.Foreground.Revision != 1 || state.Background.Revision != 1 {
		t.Fatal(state)
	}
	storage, e := s.Storage(ctx)
	if e != nil {
		t.Fatal(e)
	}
	encoded, _ := json.Marshal(storage)
	if !bytes.Contains(encoded, []byte(`"kind":"tag_images"`)) {
		t.Fatal(storage)
	}
	if _, e = db.Write.Exec("UPDATE job_items SET state='completed' WHERE job_id=?", claimed.ID); e != nil {
		t.Fatal(e)
	}
	if e = db.FinishBackground(ctx, *claimed, ""); e != nil {
		t.Fatal(e)
	}
	// Removing a reference does not delete the original; only a later explicit,
	// fingerprint-checked orphan cleanup may remove it and prune its assets row.
	if _, e = images.Remove(ctx, "tag", "foreground", 1); e != nil {
		t.Fatal(e)
	}
	plan, e = s.Plan(ctx, model.MaintenanceInput{Operation: "cleanup", Expected: map[string]int64{}, Options: json.RawMessage(`{"categories":["orphan_assets"]}`)})
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Execute(ctx, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e = db.Claim(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.cleanupFrozen(ctx, *claimed, plan.Input.Options); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.c.DataDir, asset.Key)); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("orphan retained", e)
	}
	var n int
	db.Read.QueryRow("SELECT count(*) FROM assets WHERE id=?", asset.ID).Scan(&n)
	if n != 0 {
		t.Fatal("asset row retained")
	}
	if _, e = os.Stat(orphan); e != nil {
		t.Fatal("other live role lost", e)
	}
}

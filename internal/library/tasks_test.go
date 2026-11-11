package library

import (
	"context"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestScanPauseResumeAndSubsetRetry(t *testing.T) {
	s, l, root := setup(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.BeforePublish = func(context.Context, model.Job, model.Item) { once.Do(func() { close(entered) }); <-release }
	s.Start(ctx)
	j, e := s.StartScan(ctx, l.ID)
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	if e = s.Control(ctx, j.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	paused, e := s.Job(ctx, j.ID)
	if e != nil || paused.State != "paused" {
		t.Fatal(paused, e)
	}
	if e = s.Control(ctx, j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	queued, e := s.Job(ctx, j.ID)
	if e != nil || queued.State != "queued" || queued.Generation != paused.Generation+1 {
		t.Fatal("resumed before old IO drained", queued, e)
	}
	close(release)
	done := wait(t, s, j.ID)
	if done.State != "completed" || done.Done != 3 || done.Failed != 1 {
		t.Fatal(done)
	}
	// Repair only the failed anonymous source and retry that frozen subset.
	valid, e := os.ReadFile(filepath.Join(root, "合成1.cbz"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "损坏.zip"), valid, 0600); e != nil {
		t.Fatal(e)
	}
	retry, e := s.RetryScan(ctx, j.ID, nil)
	if e != nil {
		t.Fatal(e)
	}
	done = wait(t, s, retry.ID)
	if done.State != "completed" || done.Done != 1 || done.Failed != 0 || done.ParentID == nil || *done.ParentID != j.ID {
		t.Fatal(done)
	}
	books, total, e := s.Editions(ctx, l.ID, "available", 1, 100)
	if e != nil || total != 3 || len(books) != 3 {
		t.Fatal("subset retry marked other books missing", books, total, e)
	}
}

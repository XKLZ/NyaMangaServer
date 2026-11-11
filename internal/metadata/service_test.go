package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type localCatalog struct {
	hint string
	err  error
}

func (c *localCatalog) WebInfo(context.Context, *model.Edition) (string, error) { return c.hint, c.err }
func (c *localCatalog) Control(context.Context, string, string) error {
	return errors.New("unexpected scan")
}
func (c *localCatalog) RetryScan(context.Context, string, []int) (*model.Job, error) {
	return nil, errors.New("unexpected scan")
}

type controlledRemote struct {
	*testfixture.Remote
	downloads atomic.Int32
	search    atomic.Int32
	fetch     atomic.Int32
	onFetch   func(context.Context, string) (model.RemoteMetadata, error)
	data      []byte
	checksum  string
}

func (r *controlledRemote) Search(ctx context.Context, h string) ([]model.Candidate, error) {
	r.search.Add(1)
	return r.Remote.Search(ctx, h)
}
func (r *controlledRemote) Fetch(ctx context.Context, u string) (model.RemoteMetadata, error) {
	r.fetch.Add(1)
	if r.onFetch != nil {
		return r.onFetch(ctx, u)
	}
	return r.Remote.Fetch(ctx, u)
}
func (r *controlledRemote) LatestDictionary(ctx context.Context) (model.DictionaryRelease, error) {
	v, e := r.Remote.LatestDictionary(ctx)
	v.Digest = r.checksum
	return v, e
}
func (r *controlledRemote) DownloadDictionary(ctx context.Context, v model.DictionaryRelease) ([]byte, error) {
	r.downloads.Add(1)
	if r.data != nil {
		return r.data, nil
	}
	return r.Remote.DownloadDictionary(ctx, v)
}
func setup(t *testing.T) (*Service, *store.Store, *controlledRemote, *localCatalog) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, e := store.Open(ctx, filepath.Join(dir, "data"), migrations.Files)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	_, e = db.Write.Exec(`INSERT INTO libraries(id,root_key,display_name,legacy_zip_encoding,root_identity,revision) VALUES('lib','anonymous','Anonymous','utf8','test',1);`)
	if e != nil {
		t.Fatal(e)
	}
	if e = testfixture.SeedEdition(ctx, db.Write, "book", "lib", "sample.cbz", "Filename"); e != nil {
		t.Fatal(e)
	}
	remote := &controlledRemote{Remote: &testfixture.Remote{Vault: provider.NewVault(filepath.Join(dir, "key"), filepath.Join(dir, "credentials"))}}
	local := &localCatalog{}
	s := New(db, local, remote, filepath.Join(dir, "data"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := s.Shutdown(ctx); e != nil {
			t.Error(e)
		}
	})
	return s, db, remote, local
}
func waitJob(t *testing.T, db *store.Store, id, state string) *model.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, e := db.Job(context.Background(), id)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == state {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %s: %+v", state, j)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func TestSelectionCandidatesSavedURLAndManualOwnership(t *testing.T) {
	s, db, r, local := setup(t)
	ctx := context.Background()
	local.hint = "not a valid gallery"
	s.Start(ctx)
	j, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	items, _ := db.Items(ctx, j.ID, 1, 10, false)
	if items[0].Error != "multiple_matches" || len(items[0].Candidates) != 2 || r.fetch.Load() != 0 {
		t.Fatal(items)
	}
	next, e := s.Retry(ctx, j.ID, RetryInput{Ordinals: []int{1}, URLs: map[string]string{"book": items[0].Candidates[0].URL}})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, next.ID, "completed")
	if r.search.Load() != 1 || r.fetch.Load() != 1 {
		t.Fatal("manual URL did not win")
	}
	b, _ := db.Edition(ctx, "book")
	if b.Title != "匿名来源标题" {
		t.Fatal(b)
	}
	if e = s.ApplyOverrides(ctx, b.ID, b.Revision, map[string]model.Override{"title": {Op: "set", Value: json.RawMessage(`"人工标题"`)}}); e != nil {
		t.Fatal(e)
	}
	again, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, again.ID, "completed")
	b, _ = db.Edition(ctx, "book")
	details, _ := s.Details(ctx, b.ID)
	if b.Title != "人工标题" || details.Source.Method != "manual" || r.search.Load() != 1 {
		t.Fatal(b, details)
	}
}
func TestValidDirectFailureNeverFallsBackAndCacheCanBypass(t *testing.T) {
	s, db, r, local := setup(t)
	ctx := context.Background()
	local.hint = "https://e-hentai.org/g/1/0123456789/"
	r.onFetch = func(context.Context, string) (model.RemoteMetadata, error) {
		return model.RemoteMetadata{}, &model.RemoteError{Code: "provider_rejected"}
	}
	s.Start(ctx)
	j, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	if r.search.Load() != 0 {
		t.Fatal("direct URL fell back")
	}
	local.hint = ""
	if e = db.CacheHash(ctx, "eh", "0123456789012345678901234567890123456789", nil); e != nil {
		t.Fatal(e)
	}
	j, e = s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	if r.search.Load() != 0 {
		t.Fatal("cache ignored")
	}
	j, e = s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}, Force: true})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	if r.search.Load() != 1 {
		t.Fatal("force failed")
	}
}
func TestPauseResumeDrainsOldIOAndRejectsStaleResult(t *testing.T) {
	s, db, r, _ := setup(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	r.onFetch = func(ctx context.Context, u string) (model.RemoteMetadata, error) {
		if r.fetch.Load() == 1 {
			close(entered)
			<-release
		}
		return r.Remote.Fetch(context.Background(), u)
	}
	s.Start(ctx)
	j, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}, URLs: map[string]string{"book": "https://e-hentai.org/g/1/0123456789/"}})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("not running")
	}
	if e = s.Control(ctx, j.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	if e = s.Control(ctx, j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	if r.fetch.Load() != 1 {
		t.Fatal("old IO overlapped")
	}
	close(release)
	waitJob(t, db, j.ID, "completed")
	if r.fetch.Load() != 2 {
		t.Fatal("new worker missing")
	}
	b, _ := db.Edition(ctx, "book")
	if b.Revision != 2 {
		t.Fatal("old generation committed", b)
	}
}
func TestAuthenticationPausesAndRestartResumesCheckpoint(t *testing.T) {
	s, db, r, _ := setup(t)
	ctx := context.Background()
	r.onFetch = func(context.Context, string) (model.RemoteMetadata, error) {
		return model.RemoteMetadata{}, &model.RemoteError{Code: "provider_auth_required", ActionRequired: true}
	}
	s.Start(ctx)
	j, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}, URLs: map[string]string{"book": "https://e-hentai.org/g/1/0123456789/"}})
	if e != nil {
		t.Fatal(e)
	}
	paused := waitJob(t, db, j.ID, "paused")
	if paused.Error != "provider_auth_required" || r.fetch.Load() != 1 {
		t.Fatal(paused)
	}
	if e = s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	r.onFetch = nil
	s.Start(ctx)
	time.Sleep(25 * time.Millisecond)
	if r.fetch.Load() != 1 {
		t.Fatal("user paused job auto resumed")
	}
	if e = s.Control(ctx, j.ID, "resume"); e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
}
func TestDictionaryAtomicActivationAndManualTranslations(t *testing.T) {
	s, db, r, _ := setup(t)
	ctx := context.Background()
	if e := s.ApplyOverrides(ctx, "book", 1, map[string]model.Override{"tags": {Op: "set", Value: json.RawMessage(`[{"namespace":"artist","name":"anonymous"}]`)}}); e != nil {
		t.Fatal(e)
	}
	s.Start(ctx)
	j, e := s.StartTranslation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	tags, e := s.Tags(ctx, "", "", "zh-CN")
	if e != nil || len(tags) != 1 || tags[0].Translation != "匿名作者" {
		t.Fatal(tags, e)
	}
	manual := "人工翻译"
	if e = s.Translate(ctx, tags[0].ID, "zh-CN", 0, &manual); e != nil {
		t.Fatal(e)
	}
	r.data = []byte(`{"broken":true}`)
	j, e = s.StartTranslation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	v, e := s.TranslationVersion(ctx)
	if e != nil || v.ID != "anonymous-v1" {
		t.Fatal(v, e)
	}
	tags, _ = s.Tags(ctx, "", "", "zh-CN")
	if tags[0].Translation != "人工翻译" {
		t.Fatal(tags)
	}
	var count int
	db.Read.QueryRow("SELECT count(*) FROM tags").Scan(&count)
	if count != 1 {
		t.Fatal("unused dictionary terms materialized")
	}
	r.data = nil
	r.checksum = "wrong"
	j, e = s.StartTranslation(ctx)
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	items, _ := db.Items(ctx, j.ID, 1, 10, false)
	if items[0].Error != "dictionary_checksum_mismatch" {
		t.Fatal(items)
	}
}

func TestRunningCheckpointRestartAndBoundedRetry(t *testing.T) {
	s, db, r, _ := setup(t)
	ctx := context.Background()
	entered := make(chan struct{})
	r.onFetch = func(ctx context.Context, _ string) (model.RemoteMetadata, error) {
		close(entered)
		<-ctx.Done()
		return model.RemoteMetadata{}, ctx.Err()
	}
	s.Start(ctx)
	j, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}, URLs: map[string]string{"book": "https://e-hentai.org/g/1/0123456789/"}})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	if e = s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	before, _ := db.Job(ctx, j.ID)
	if before.State != "running" {
		t.Fatal(before)
	}
	r.onFetch = nil
	s.Start(ctx)
	after := waitJob(t, db, j.ID, "completed")
	if after.Generation <= before.Generation {
		t.Fatal("generation not renewed")
	}
	if e = s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	var attempts atomic.Int32
	r.onFetch = func(context.Context, string) (model.RemoteMetadata, error) {
		attempts.Add(1)
		return model.RemoteMetadata{}, &model.RemoteError{Code: "provider_unavailable", Retryable: true, Wait: time.Millisecond}
	}
	s.Start(ctx)
	j, e = s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}})
	if e != nil {
		t.Fatal(e)
	}
	waitJob(t, db, j.ID, "completed")
	items, _ := db.Items(ctx, j.ID, 1, 10, false)
	if attempts.Load() != 3 || items[0].Attempt != 3 || items[0].Error != "provider_unavailable" {
		t.Fatal(attempts.Load(), items)
	}
}

func TestCancelThenNewTaskWaitsForOldIOWithoutPublishing(t *testing.T) {
	s, db, r, _ := setup(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	r.onFetch = func(_ context.Context, u string) (model.RemoteMetadata, error) {
		if r.fetch.Load() == 1 {
			close(entered)
			<-release
		}
		return r.Remote.Fetch(context.Background(), u)
	}
	s.Start(ctx)
	first, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}, URLs: map[string]string{"book": "https://e-hentai.org/g/1/0123456789/"}})
	if e != nil {
		t.Fatal(e)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("not running")
	}
	if e = s.Control(ctx, first.ID, "cancel"); e != nil {
		t.Fatal(e)
	}
	next, e := s.StartMetadata(ctx, JobInput{BookIDs: []string{"book"}, URLs: map[string]string{"book": "https://e-hentai.org/g/1/0123456789/"}})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	if r.fetch.Load() != 1 {
		t.Fatal("new task overlapped old IO")
	}
	close(release)
	waitJob(t, db, next.ID, "completed")
	old, _ := db.Job(ctx, first.ID)
	book, _ := db.Edition(ctx, "book")
	if old.State != "cancelled" || book.Revision != 2 {
		t.Fatal("cancelled worker committed", old, book)
	}
}

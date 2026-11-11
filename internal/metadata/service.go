// Package metadata owns source selection, manual fields and persistent remote tasks.
package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/provider"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Repository interface {
	JobContext(context.Context, string) (context.Context, error)
	RequireAdministrator(context.Context) error
	TagSuggestions(context.Context, model.TagSuggestionInput) (model.TagSuggestions, error)
	DictionaryAvailable(context.Context) (bool, error)
	MaterializeActiveDictionary(context.Context) error
	CreateSourcePreview(context.Context, string, string, int64, int64, int64) (*model.Job, error)
	PublishSourcePreview(context.Context, model.Job, model.Item, model.RemoteMetadata) error
	SourcePreview(context.Context, string, string) (model.SourcePreview, error)
	ConfirmSourcePreview(context.Context, string, string, int64, int64, int64) error
	RemoveSource(context.Context, string, int64, int64, bool) error
	EditionWithMetadata(context.Context, string) (*model.Edition, model.MetadataDetails, error)
	Edition(context.Context, string) (*model.Edition, error)
	MetadataDetails(context.Context, string) (model.MetadataDetails, error)
	ApplyOverrides(context.Context, string, int64, map[string]model.Override) error
	Tags(context.Context, string, string, string) ([]model.Tag, error)
	TranslateTag(context.Context, string, string, int64, *string) error
	CreateMetadataJob(context.Context, string, string, []string, map[string]model.MetadataInput) (*model.Job, error)
	CreateTranslationJob(context.Context, string, string) (*model.Job, error)
	Job(context.Context, string) (*model.Job, error)
	Items(context.Context, string, int, int, bool) ([]model.Item, error)
	Claim(context.Context, string) (*model.Job, error)
	BackgroundJobs(context.Context) ([]model.Job, error)
	ControlJob(context.Context, string, string, string) error
	PublishMetadata(context.Context, model.Job, model.Item, *model.RemoteMetadata, string, []model.Candidate) error
	NoteAttempt(context.Context, model.Job, model.Item) error
	ItemCheckpoint(context.Context, model.Job, model.Item, string) error
	FinishBackground(context.Context, model.Job, string) error
	FailBackgroundItem(context.Context, model.Job, model.Item, string) error
	LookupHash(context.Context, string, string) ([]model.Candidate, bool, error)
	CacheHash(context.Context, string, string, []model.Candidate) error
	TranslationVersion(context.Context) (*model.TranslationVersion, error)
	PublishDictionary(context.Context, model.Job, model.Item, model.TranslationVersion, map[model.TagName]string) error
	MaterializeTranslations(context.Context, string, map[model.TagName]string) error
	Events(context.Context, int64, int) ([]model.Event, int64, bool, error)
}
type Catalog interface {
	WebInfo(context.Context, *model.Edition) (string, error)
	Control(context.Context, string, string) error
	RetryScan(context.Context, string, []int) (*model.Job, error)
}
type Remote interface {
	Settings() (provider.Settings, error)
	Configure(provider.SettingPatch) (provider.Settings, error)
	Credentials(provider.CredentialPatch) (provider.Settings, error)
	Test(context.Context) error
	Search(context.Context, string) ([]model.Candidate, error)
	Fetch(context.Context, string) (model.RemoteMetadata, error)
	LatestDictionary(context.Context) (model.DictionaryRelease, error)
	DownloadDictionary(context.Context, model.DictionaryRelease) ([]byte, error)
}
type JobInput struct {
	BookIDs []string          `json:"book_ids"`
	URLs    map[string]string `json:"gallery_urls,omitempty"`
	Force   bool              `json:"force_refresh,omitempty"`
}
type RetryInput struct {
	Ordinals []int             `json:"ordinals,omitempty"`
	URLs     map[string]string `json:"gallery_urls,omitempty"`
}
type Service struct {
	dictionaryMu sync.Mutex
	repo         Repository
	catalog      Catalog
	remote       Remote
	dir          string
	logger       *slog.Logger
	wake         chan struct{}
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	active       string
	stopActive   context.CancelFunc
}

func New(repo Repository, catalog Catalog, remote Remote, dir string, logger *slog.Logger) *Service {
	return &Service{repo: repo, catalog: catalog, remote: remote, dir: filepath.Join(dir, "cache", "tag-translations"), logger: logger, wake: make(chan struct{}, 1)}
}
func (s *Service) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Service) Settings(ctx context.Context) (provider.Settings, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return provider.Settings{}, e
	}
	return s.remote.Settings()
}
func (s *Service) Configure(ctx context.Context, in provider.SettingPatch) (provider.Settings, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return provider.Settings{}, e
	}
	return s.remote.Configure(in)
}
func (s *Service) Credentials(ctx context.Context, in provider.CredentialPatch) (provider.Settings, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return provider.Settings{}, e
	}
	return s.remote.Credentials(in)
}
func (s *Service) Test(ctx context.Context) error {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return e
	}
	return s.remote.Test(ctx)
}
func (s *Service) View(ctx context.Context, id string) (*model.Edition, model.MetadataDetails, error) {
	return s.repo.EditionWithMetadata(ctx, id)
}
func (s *Service) Details(ctx context.Context, id string) (model.MetadataDetails, error) {
	return s.repo.MetadataDetails(ctx, id)
}
func (s *Service) ApplyOverrides(ctx context.Context, id string, expected int64, fields map[string]model.Override) error {
	if expected < 1 {
		return model.ErrInput
	}
	if e := model.ValidateOverrides(fields); e != nil {
		return e
	}
	if e := s.repo.ApplyOverrides(ctx, id, expected, fields); e != nil {
		return e
	}
	s.refreshTranslations(ctx)
	return nil
}
func (s *Service) Tags(ctx context.Context, q, namespace, locale string) ([]model.Tag, error) {
	if len(q) > 512 || len(namespace) > 32 || locale != "zh-CN" {
		return nil, model.ErrInput
	}
	return s.repo.Tags(ctx, q, namespace, locale)
}
func (s *Service) Translate(ctx context.Context, id, locale string, expected int64, text *string) error {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return e
	}
	if locale != "zh-CN" || expected < 0 {
		return model.ErrInput
	}
	if text != nil && (!utf8.ValidString(*text) || strings.ContainsRune(*text, 0) || strings.TrimSpace(*text) == "" || utf8.RuneCountInString(*text) > 512 || len(*text) > 2048) {
		return model.ErrInput
	}
	return s.repo.TranslateTag(ctx, id, locale, expected, text)
}
func (s *Service) StartMetadata(ctx context.Context, in JobInput) (*model.Job, error) {
	return s.startMetadata(ctx, in, "")
}
func (s *Service) startMetadata(ctx context.Context, in JobInput, parent string) (*model.Job, error) {
	if len(in.BookIDs) < 1 || len(in.BookIDs) > 1000 || len(in.URLs) > len(in.BookIDs) {
		return nil, model.ErrInput
	}
	seen := map[string]bool{}
	inputs := map[string]model.MetadataInput{}
	for _, id := range in.BookIDs {
		if id == "" || seen[id] {
			return nil, model.ErrInput
		}
		seen[id] = true
		value := model.MetadataInput{Force: in.Force}
		if raw, ok := in.URLs[id]; ok {
			url, _, _, e := provider.Gallery(raw)
			if e != nil {
				return nil, model.ErrInput
			}
			value.URL = url
		}
		inputs[id] = value
	}
	for id := range in.URLs {
		if !seen[id] {
			return nil, model.ErrInput
		}
	}
	id, e := model.NewID()
	if e != nil {
		return nil, e
	}
	j, e := s.repo.CreateMetadataJob(ctx, id, parent, in.BookIDs, inputs)
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) StartTranslation(ctx context.Context) (*model.Job, error) {
	if e := model.AuthorizeAdministrator(ctx, s.repo); e != nil {
		return nil, e
	}
	id, e := model.NewID()
	if e != nil {
		return nil, e
	}
	j, e := s.repo.CreateTranslationJob(ctx, id, "")
	if e == nil {
		s.Wake()
	}
	return j, e
}
func (s *Service) TranslationVersion(ctx context.Context) (*model.TranslationVersion, error) {
	return s.repo.TranslationVersion(ctx)
}
func (s *Service) EventsForAccount(ctx context.Context, account string, after int64) ([]model.Event, int64, bool, error) {
	if repo, ok := s.repo.(interface {
		EventsForAccount(context.Context, string, int64, int) ([]model.Event, int64, bool, error)
	}); ok {
		return repo.EventsForAccount(ctx, account, after, 200)
	}
	return s.repo.Events(ctx, after, 200)
}
func (s *Service) Events(ctx context.Context, after int64) ([]model.Event, int64, bool, error) {
	return s.repo.Events(ctx, after, 200)
}
func (s *Service) Control(ctx context.Context, id, action string) error {
	j, e := s.repo.Job(ctx, id)
	if e != nil {
		return e
	}
	if j.Type != "scan" && j.Type != "metadata" && j.Type != "metadata_preview" && j.Type != "metadata_search" && j.Type != "tag_translation" {
		return model.ErrInput
	}
	if j.Type == "scan" {
		return s.catalog.Control(ctx, id, action)
	}
	if e = s.repo.ControlJob(ctx, id, action, ""); e != nil {
		return e
	}
	if action != "resume" {
		s.mu.Lock()
		if s.active == id && s.stopActive != nil {
			s.stopActive()
		}
		s.mu.Unlock()
	}
	s.Wake()
	return nil
}
func (s *Service) Retry(ctx context.Context, parent string, in RetryInput) (*model.Job, error) {
	if len(in.Ordinals) > 1000 {
		return nil, model.ErrInput
	}
	j, e := s.repo.Job(ctx, parent)
	if e != nil {
		return nil, e
	}
	if j.State != "completed" && j.State != "failed" && j.State != "cancelled" {
		return nil, model.ErrConflict
	}
	if j.Type == "maintenance" || j.Type == "cover_rebuild" || (j.Type == "work_recommendation" || j.Type == "series_recommendation") {
		return nil, model.ErrInput
	}
	if j.Type == "metadata_search" {
		return nil, model.ErrInput
	}
	if j.Type == "metadata_preview" {
		if len(in.URLs) > 0 {
			return nil, model.ErrInput
		}
		items, e := s.repo.Items(ctx, parent, 1, 100, false)
		if e != nil {
			return nil, e
		}
		return s.retryPreview(ctx, parent, items)
	}
	if j.Type == "scan" {
		if len(in.URLs) > 0 {
			return nil, model.ErrInput
		}
		return s.catalog.RetryScan(ctx, parent, in.Ordinals)
	}
	selected := map[int]bool{}
	for _, v := range in.Ordinals {
		if v < 1 || selected[v] {
			return nil, model.ErrInput
		}
		selected[v] = true
	}
	next := JobInput{URLs: in.URLs, Force: true}
	matched := 0
	for page := 1; ; page++ {
		items, e := s.repo.Items(ctx, parent, page, 100, false)
		if e != nil {
			return nil, e
		}
		for _, item := range items {
			choose := selected[item.Ordinal]
			if len(selected) == 0 {
				choose = item.State == "failed" || item.State == "deferred"
			}
			if !choose {
				continue
			}
			if item.State == "completed" {
				return nil, model.ErrInput
			}
			matched++
			if item.BookID != nil {
				next.BookIDs = append(next.BookIDs, *item.BookID)
			}
		}
		if len(items) < 100 {
			break
		}
	}
	if matched == 0 || (len(selected) > 0 && matched != len(selected)) {
		return nil, model.ErrInput
	}
	if j.Type == "tag_translation" {
		if len(in.URLs) > 0 {
			return nil, model.ErrInput
		}
		id, e := model.NewID()
		if e != nil {
			return nil, e
		}
		next, e := s.repo.CreateTranslationJob(ctx, id, parent)
		if e == nil {
			s.Wake()
		}
		return next, e
	}
	return s.startMetadata(ctx, next, parent)
}
func (s *Service) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx)
}
func (s *Service) Shutdown(ctx context.Context) error {
	if s.cancel == nil {
		return nil
	}
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if !model.AutomaticWorkAllowed(ctx, s.repo) {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			case <-s.wake:
			}
			continue
		}
		jobs, e := s.repo.BackgroundJobs(ctx)
		if e == nil && len(jobs) > 0 {
			s.execute(ctx, jobs[0].ID)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.wake:
		}
	}
}
func (s *Service) execute(parent context.Context, id string) {
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		release, e := r.BeginHealthIO(parent)
		if e != nil {
			return
		}
		defer release()
	}

	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	s.active = id
	s.stopActive = cancel
	s.mu.Unlock()
	defer func() { cancel(); s.mu.Lock(); s.active = ""; s.stopActive = nil; s.mu.Unlock() }()
	scopedContext, contextErr := s.repo.JobContext(ctx, id)
	if contextErr != nil {
		return
	}
	ctx = scopedContext
	j, e := s.repo.Claim(ctx, id)
	if e != nil {
		return
	}
	defer func() {
		if recover() != nil {
			s.logger.Error("metadata worker panic", "job_id", id)
			_ = s.repo.FinishBackground(context.Background(), *j, "worker_failed")
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		items, e := s.repo.Items(ctx, id, 1, 1, true)
		if e != nil {
			_ = s.repo.FinishBackground(ctx, *j, "storage_error")
			return
		}
		if len(items) == 0 {
			_ = s.repo.FinishBackground(ctx, *j, "")
			return
		}
		item := items[0]
		for {
			if ctx.Err() != nil {
				return
			}
			if item.Attempt >= 3 {
				if j.Type == "metadata" {
					e = s.repo.PublishMetadata(ctx, *j, item, nil, "retries_exhausted", nil)
				} else {
					e = s.repo.FailBackgroundItem(ctx, *j, item, "retries_exhausted")
				}
				break
			}
			if e = s.repo.NoteAttempt(ctx, *j, item); e != nil {
				return
			}
			item.Attempt++
			if j.Type == "metadata" {
				e = s.processMetadata(ctx, *j, item)
			} else if j.Type == "metadata_search" {
				e = s.processSourceSearch(ctx, *j, item)
			} else if j.Type == "metadata_preview" {
				e = s.processPreview(ctx, *j, item)
			} else {
				e = s.processTranslation(ctx, *j, &item)
			}
			if e == nil {
				break
			}
			if ctx.Err() != nil || errors.Is(e, model.ErrStaleJob) {
				return
			}
			var remote *model.RemoteError
			code := "operation_failed"
			if errors.As(e, &remote) {
				code = remote.Code
			} else if errors.Is(e, provider.ErrSecrets) {
				remote = &model.RemoteError{Code: "provider_secrets_unavailable", ActionRequired: true}
				code = remote.Code
			}
			if remote != nil && remote.ActionRequired {
				_ = s.repo.ControlJob(ctx, id, "pause", code)
				return
			}
			if remote != nil && remote.Retryable && item.Attempt < 3 {
				wait := remote.Wait
				if wait <= 0 {
					wait = time.Duration(1<<item.Attempt) * time.Second
				}
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				continue
			}
			if j.Type == "metadata" {
				e = s.repo.PublishMetadata(ctx, *j, item, nil, code, nil)
			} else {
				e = s.repo.FailBackgroundItem(ctx, *j, item, code)
			}
			break
		}
		if e != nil {
			if !errors.Is(e, model.ErrStaleJob) && ctx.Err() == nil {
				_ = s.repo.FinishBackground(ctx, *j, "storage_error")
			}
			return
		}
	}
}
func (s *Service) processMetadata(ctx context.Context, j model.Job, item model.Item) error {
	if scoped, ok := s.repo.(interface {
		ImportMetadataInputValid(context.Context, model.Job, model.Item) (bool, error)
	}); ok {
		valid, e := scoped.ImportMetadataInputValid(ctx, j, item)
		if e != nil {
			return e
		}
		if !valid {
			return s.repo.PublishMetadata(ctx, j, item, nil, "import_request_superseded", nil)
		}
	}

	if item.BookID == nil {
		return model.ErrInput
	}
	book, e := s.repo.Edition(ctx, *item.BookID)
	if e != nil {
		return e
	}
	if book.ContentRevision != item.ContentRevision {
		return s.repo.PublishMetadata(ctx, j, item, nil, "content_changed", nil)
	}
	var input model.MetadataInput
	if json.Unmarshal([]byte(item.Input), &input) != nil {
		return model.ErrInput
	}
	if input.LookupAlgorithm != "" && (input.LookupAlgorithm != book.RepresentativeAlgorithm || (input.LookupSHA1 == nil) != (book.SHA1 == nil) || input.LookupSHA1 != nil && book.SHA1 != nil && *input.LookupSHA1 != *book.SHA1) {
		return s.repo.PublishMetadata(ctx, j, item, nil, "lookup_input_changed", nil)
	}
	selected := input.URL
	method := "manual"
	warning := ""
	if selected == "" {
		hint, e := s.catalog.WebInfo(ctx, book)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e != nil {
			warning = "webinfo_invalid"
		}
		if hint != "" {
			selected, _, _, e = provider.Gallery(hint)
			if e != nil {
				warning = "webinfo_invalid"
			}
		}
		if selected != "" {
			method = "webinfo"
		}
	}
	var candidates []model.Candidate
	if selected == "" {
		if book.SHA1 == nil {
			return s.repo.PublishMetadata(ctx, j, item, nil, "no_sha1", nil)
		}
		settings, e := s.remote.Settings()
		if e != nil {
			return e
		}
		var found bool
		if !input.Force {
			candidates, found, e = s.repo.LookupHash(ctx, settings.Site, *book.SHA1)
			if e != nil {
				return e
			}
		}
		if !found {
			candidates, e = s.remote.Search(ctx, *book.SHA1)
			if e != nil {
				return e
			}
			if len(candidates) > 10 {
				return &model.RemoteError{Code: "invalid_response"}
			}
			for i := range candidates {
				url, _, _, e := provider.Gallery(candidates[i].URL)
				if e != nil {
					return &model.RemoteError{Code: "invalid_response"}
				}
				candidates[i].URL = url
			}
			if e = s.repo.CacheHash(ctx, settings.Site, *book.SHA1, candidates); e != nil {
				return e
			}
		}
		if len(candidates) == 0 {
			return s.repo.PublishMetadata(ctx, j, item, nil, "no_match", nil)
		}
		if len(candidates) > 1 {
			return s.repo.PublishMetadata(ctx, j, item, nil, "multiple_matches", candidates)
		}
		selected = candidates[0].URL
		method = "hash"
	}
	result, e := s.remote.Fetch(ctx, selected)
	if e != nil {
		return e
	}
	result.Method = method
	if e = s.repo.PublishMetadata(ctx, j, item, &result, warning, nil); e != nil {
		return e
	}
	s.refreshTranslations(ctx)
	return nil
}
func (s *Service) processTranslation(ctx context.Context, j model.Job, item *model.Item) error {
	s.dictionaryMu.Lock()
	defer s.dictionaryMu.Unlock()
	if r, ok := s.repo.(interface {
		EnhancementAllowed(context.Context, string) error
	}); ok {
		if e := r.EnhancementAllowed(ctx, model.EnhancementDictionary); e != nil {
			return e
		}
	}
	var checkpoint struct {
		model.DictionaryRelease
		CacheDigest string `json:"cache_sha256,omitempty"`
	}
	_ = json.Unmarshal([]byte(item.Input), &checkpoint)
	release := checkpoint.DictionaryRelease
	if release.Version == "" {
		var e error
		release, e = s.remote.LatestDictionary(ctx)
		if e != nil {
			return e
		}
		checkpoint.DictionaryRelease = release
		input, _ := json.Marshal(checkpoint)
		if e = s.repo.ItemCheckpoint(ctx, j, *item, string(input)); e != nil {
			return e
		}
		item.Input = string(input)
	}
	data, e := s.cachedDictionary(checkpoint.CacheDigest)
	cached := e == nil
	if !cached {
		data, e = s.remote.DownloadDictionary(ctx, release)
		if e != nil {
			return e
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if release.Digest != "" && release.Digest != digest {
		return &model.RemoteError{Code: "dictionary_checksum_mismatch"}
	}
	dictionary, e := provider.ParseDictionary(data)
	if e != nil {
		return e
	}
	if e = appdata.NoSymlinks(s.dir); e != nil {
		return errors.New("translation_cache_unavailable")
	}
	if e = os.MkdirAll(s.dir, 0700); e != nil {
		return e
	}
	key := digest + ".json"
	if cached {
		return s.repo.PublishDictionary(ctx, j, *item, model.TranslationVersion{ID: release.Version, Source: "https://github.com/EhTagTranslation/Database", Digest: digest, CacheKey: key}, dictionary)
	}
	path := filepath.Join(s.dir, key)
	if e = appdata.NoSymlinks(path); e != nil {
		return e
	}
	f, e := os.CreateTemp(s.dir, "pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	checkpoint.CacheDigest = digest
	checkpoint.DictionaryRelease = release
	input, _ := json.Marshal(checkpoint)
	if e = s.repo.ItemCheckpoint(ctx, j, *item, string(input)); e != nil {
		return e
	}
	item.Input = string(input)
	return s.repo.PublishDictionary(ctx, j, *item, model.TranslationVersion{ID: release.Version, Source: "https://github.com/EhTagTranslation/Database", Digest: digest, CacheKey: key}, dictionary)
}

// PrepareDictionary is offline only and runs before HTTP/workers. A missing or
// damaged cache degrades suggestions, never triggers a network request.
func (s *Service) PrepareDictionary(ctx context.Context) {
	if !model.AutomaticWorkAllowed(ctx, s.repo) {
		return
	}
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		release, e := r.BeginHealthIO(ctx)
		if e != nil {
			return
		}
		defer release()
	}
	s.ensureTranslations(ctx)
}
func (s *Service) refreshTranslations(ctx context.Context) {
	s.dictionaryMu.Lock()
	defer s.dictionaryMu.Unlock()
	s.refreshTranslationsUnlocked(ctx)
}
func (s *Service) refreshTranslationsUnlocked(ctx context.Context) {
	if ready, e := s.repo.DictionaryAvailable(ctx); e == nil && ready {
		if e = s.repo.MaterializeActiveDictionary(ctx); e != nil {
			s.logger.Warn("translation projection unavailable")
		}
	}
}
func (s *Service) ensureTranslations(ctx context.Context) {
	s.dictionaryMu.Lock()
	defer s.dictionaryMu.Unlock()
	if r, ok := s.repo.(interface{ DictionaryOfflineAllowed(context.Context) error }); ok {
		if e := r.DictionaryOfflineAllowed(ctx); e != nil {
			return
		}
	}
	if ready, e := s.repo.DictionaryAvailable(ctx); e == nil && ready {
		s.refreshTranslationsUnlocked(ctx)
		return
	}
	version, e := s.repo.TranslationVersion(ctx)
	if e != nil || version == nil {
		return
	}
	if len(version.Digest) != 64 || version.CacheKey != version.Digest+".json" {
		return
	}
	data, e := s.cachedDictionary(version.Digest)
	if e != nil {
		return
	}

	dictionary, e := provider.ParseDictionary(data)
	if e != nil {
		return
	}
	if e = s.repo.MaterializeTranslations(ctx, version.ID, dictionary); e != nil {
		s.logger.Warn("translation projection unavailable")
	}
}

func (s *Service) Drain(ctx context.Context) error {
	for {
		s.mu.Lock()
		id := s.active
		stop := s.stopActive
		s.mu.Unlock()
		if id == "" {
			return nil
		}
		j, e := s.repo.Job(ctx, id)
		if e == nil && j.State != "paused" && j.State != "cancelled" {
			return nil
		}
		if stop != nil {
			stop()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *Service) TagSuggestions(ctx context.Context, in model.TagSuggestionInput) (model.TagSuggestions, error) {
	in, e := model.ValidateTagSuggestions(in)
	if e != nil {
		return model.TagSuggestions{}, e
	}
	return s.repo.TagSuggestions(ctx, in)
}

func (s *Service) cachedDictionary(digest string) ([]byte, error) {
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return nil, errors.New("translation_cache_unavailable")
	}
	path := filepath.Join(s.dir, digest+".json")
	if e := appdata.NoSymlinks(path); e != nil {
		return nil, e
	}
	file, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	info, e := file.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, errors.New("translation_cache_unavailable")
	}
	data, e := io.ReadAll(io.LimitReader(file, (32<<20)+1))
	if e != nil {
		return nil, e
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != digest {
		return nil, errors.New("translation_cache_unavailable")
	}
	return data, nil
}

func (s *Service) DrainDictionary(ctx context.Context) error {
	for {
		s.mu.Lock()
		id, stop := s.active, s.stopActive
		s.mu.Unlock()
		busy := false
		if id != "" {
			j, e := s.repo.Job(ctx, id)
			if e != nil {
				return e
			}
			if j.Type == "tag_translation" {
				busy = true
				if stop != nil {
					stop()
				}
			}
		}
		if s.dictionaryMu.TryLock() {
			s.dictionaryMu.Unlock()
		} else {
			busy = true
		}
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (s *Service) RestoreDictionary(ctx context.Context) error {
	if r, ok := s.repo.(interface {
		BeginHealthIO(context.Context) (func(), error)
	}); ok {
		release, e := r.BeginHealthIO(ctx)
		if e != nil {
			return e
		}
		defer release()
	}

	s.ensureTranslations(ctx)
	v, e := s.repo.TranslationVersion(ctx)
	if e != nil || v == nil {
		return e
	}
	if r, ok := s.repo.(interface {
		DictionaryResourceState(context.Context) (string, error)
	}); ok {
		state, e := r.DictionaryResourceState(ctx)
		if e != nil {
			return e
		}
		if state == "ready" {
			return nil
		}
	}
	// While enabling, public reads are deliberately disabled. The installation
	// itself is checked by the store after offline materialization.
	if r, ok := s.repo.(interface {
		DictionaryInstalled(context.Context) (bool, error)
	}); ok {
		ready, e := r.DictionaryInstalled(ctx)
		if e != nil {
			return e
		}
		if ready {
			return s.repo.MaterializeActiveDictionary(ctx)
		}
	}
	// Enabling is a configuration change, not a claim that an installed index
	// is usable. Missing/corrupt cache remains resource_state=unavailable; the
	// explicitly enabled update endpoint can repair it on user request.
	return nil
}

// HealthDictionary reads and verifies installed offline input only. No provider call.
func (s *Service) HealthDictionary(ctx context.Context) (map[model.TagName]string, error) {
	v, e := s.repo.TranslationVersion(ctx)
	if e != nil {
		return nil, e
	}
	if v == nil {
		return nil, model.ErrNotFound
	}
	data, e := s.cachedDictionary(v.Digest)
	if e != nil {
		return nil, e
	}
	return provider.ParseDictionary(data)
}

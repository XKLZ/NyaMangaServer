package app

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/enhancements"
	"nyamangaserver.local/server/internal/galleryhighlight"
	"nyamangaserver.local/server/internal/httpapi"
	"nyamangaserver.local/server/internal/importflow"
	"nyamangaserver.local/server/internal/library"
	"nyamangaserver.local/server/internal/maintenance"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/organization"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/search"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/tagcatalog"
	"nyamangaserver.local/server/internal/tagimages"
	"nyamangaserver.local/server/internal/userstate"
	"nyamangaserver.local/server/internal/webassets"
	"nyamangaserver.local/server/migrations"
	"path/filepath"
)

func readSetupToken(filename string) (string, error) {
	f, e := os.Open(filename)
	if e != nil {
		return "", fmt.Errorf("uninitialized database requires readable setup_token_file: %w", e)
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return "", fmt.Errorf("setup credential must be a private regular file (0600 or 0400)")
	}
	b, e := io.ReadAll(io.LimitReader(f, 1025))
	f.Close()
	if e != nil {
		return "", e
	}
	if len(b) > 1024 {
		return "", fmt.Errorf("setup credential file too large")
	}
	setup := strings.TrimSpace(string(b))
	raw, e := base64.RawURLEncoding.DecodeString(setup)
	if e != nil || len(raw) < 32 {
		return "", fmt.Errorf("setup credential must encode at least 32 random bytes as base64url")
	}
	return setup, nil
}

func Run(version, commit string) error {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	filename := flags.String("config", os.Getenv("NYAMANGA_CONFIG"), "explicit JSON configuration file")
	check := flags.String("healthcheck", "", "check readiness URL and exit")
	if e := flags.Parse(os.Args[1:]); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *check != "" {
		client := http.Client{Timeout: 3 * time.Second}
		r, e := client.Get(*check)
		if e != nil {
			return e
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			return fmt.Errorf("readiness status %d", r.StatusCode)
		}
		return nil
	}
	c, e := config.Load(*filename)
	if e != nil {
		return e
	}
	syscall.Umask(0077)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return e
	}
	defer lock.Close()
	instanceBuild := maintenance.InstanceBuild{Version: version, Commit: commit}
	if e = maintenance.ActivateInstance(ctx, c, instanceBuild, maintenance.InstanceActivationOptions{Epoch: store.InstanceOfflineEpoch, FinishRollback: store.FinishInstanceRollback, CheckRollback: store.CheckInstanceRollbackIdentity, ValidatePrepared: func(ctx context.Context, path string, roots []model.InstanceRebase) error {
		inventory, e := store.ValidateInstanceSchema(ctx, path, migrations.Files)
		if e != nil {
			return e
		}
		return maintenance.ValidatePreparedInstanceRoots(ctx, c, inventory, roots)
	}}); e != nil {
		return fmt.Errorf("instance recovery stopped: %w", e)
	}
	if e = appdata.CheckReady(c.DataDir); e != nil {
		return e
	}
	db, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		return fmt.Errorf("open database: %w", e)
	}
	defer db.Close()
	if e = db.RecoverInstanceOperations(ctx); e != nil {
		return e
	}
	a, e := db.Account(ctx)
	if e != nil {
		return e
	}
	var setup string
	if a == nil || a.PasswordHash == "" {
		setup, e = readSetupToken(c.SetupTokenFile)
		if e != nil {
			return e
		}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	svc := auth.New(db, setup)
	catalog := library.New(db, c, logger)
	defer catalog.Close()
	remote := provider.New(provider.NewVault(c.ProviderKeyFile, c.ProviderCredentialsFile))
	meta := metadata.New(db, catalog, remote, c.DataDir, logger)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = meta.Shutdown(shutdown)
	}()
	meta.PrepareDictionary(ctx)
	worker := recommendation.NewWorker(recommendation.WorkerConfig{Python: c.WorkRecommendations.Python, Script: c.WorkRecommendations.Worker, Models: c.WorkRecommendations.ModelsDir, Cache: filepath.Join(c.DataDir, "indexes", "recommendations", "features")})
	recommend := recommendation.New(db, catalog, worker, c.DataDir, logger)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = recommend.Shutdown(shutdown)
	}()
	enhancementsService := enhancements.New(db, enhancementRuntime{recommend, meta, db})
	enhancementsService.Start(ctx)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = enhancementsService.Shutdown(shutdown)
	}()
	tagImages := tagimages.New(db, c, catalog.Pool)
	maint := maintenance.New(db, c, catalog, drainServices{meta, recommend}, logger).WithTagImages(tagImages)
	imports := importflow.New(db, catalog, logger, func() { catalog.Wake(); meta.Wake(); recommend.Wake() }).WithCovers(remote, c.DataDir)
	imports.Interrupt = func(ctx context.Context, j model.Job, action string) error {
		if j.Type == "work_recommendation" || j.Type == "series_recommendation" {
			return recommend.Control(ctx, j.ID, action)
		}
		return meta.Control(ctx, j.ID, action)
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = imports.Shutdown(shutdown)
	}()
	handler, health, instance := assembleHTTP(ctx, c, logger, svc, db, catalog, meta, maint, recommend, imports, tagImages, enhancementsService, httpapi.Build{Version: version, Commit: commit}, instanceBuild)
	instance.Start(ctx)
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = instance.Shutdown(shutdown)
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdown)
	}()
	server := &http.Server{Addr: c.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	catalog.Start(ctx)
	meta.Start(ctx)
	maint.Start(ctx)
	recommend.Start(ctx)
	imports.Start(ctx)
	health.Start(ctx)
	go func() { done <- server.ListenAndServe() }()
	logger.Info("server starting", "version", version, "commit", commit)
	select {
	case e = <-done:
		if e != http.ErrServerClosed {
			return e
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpError := server.Shutdown(shutdown)
		if httpError != nil {
			_ = server.Close()
		}
		jobError := errors.Join(imports.Shutdown(shutdown), catalog.Shutdown(shutdown), meta.Shutdown(shutdown), maint.Shutdown(shutdown), recommend.Shutdown(shutdown))
		if e = errors.Join(httpError, jobError); e != nil {
			return e
		}
		e = <-done
		if e != http.ErrServerClosed {
			return e
		}
	}
	logger.Info("server stopped")
	return nil
}

// Maintenance must wait for invalidated remote and local inference IO before cleanup.
type drainService interface{ Drain(context.Context) error }
type drainServices []drainService

func (services drainServices) Drain(ctx context.Context) error {
	var out error
	for _, service := range services {
		out = errors.Join(out, service.Drain(ctx))
	}
	return out
}

// assembleHTTP is shared by production startup and anonymous integration tests;
// starting workers and listeners remains the caller's responsibility.
func assembleHTTP(ctx context.Context, c config.Config, logger *slog.Logger, svc *auth.Service, db *store.Store, catalog *library.Service, meta *metadata.Service, maint *maintenance.Service, recommend *recommendation.Service, imports *importflow.Service, tagImages *tagimages.Service, enhancementsService *enhancements.Service, build httpapi.Build, instanceBuild maintenance.InstanceBuild) (*httpapi.Handler, *maintenance.HealthService, *maintenance.InstanceService) {
	handler := httpapi.New(c, svc, webassets.Files(), logger, build, catalog).WithEnhancements(enhancementsService, db).WithGalleryHighlight(galleryhighlight.New(db)).WithTagCatalog(tagcatalog.New(db)).WithTagImages(tagImages).WithMetadata(meta, ctx).WithOrganization(organization.New(db), search.New(db), userstate.New(db)).WithMaintenance(maint).WithRecommendations(recommend).WithImports(imports)
	health := maintenance.NewHealth(db, c, catalog.Pool)
	health.CoverBytes = catalog.HealthCoverBytes
	health.TagBytes = tagImages.HealthCacheBytes
	health.DictionaryBytes = meta.HealthDictionary
	handler.WithHealth(health)
	health.Drain = func(ctx context.Context) error {
		return errors.Join(handler.DrainHealthHTTP(ctx), maint.Drain(ctx), catalog.Drain(ctx), meta.Drain(ctx), meta.DrainDictionary(ctx), recommend.Drain(ctx))
	}
	instance := maintenance.NewInstance(db, c, instanceBuild, migrations.Files)
	instance.Inspect = store.ValidateInstanceSchema
	instance.PrepareDatabase = store.PrepareInstanceDatabase
	instance.Drain = func(ctx context.Context) error { return errors.Join(health.Drain(ctx), health.DrainChecks(ctx)) }
	handler.WithInstance(instance)
	return handler, health, instance
}

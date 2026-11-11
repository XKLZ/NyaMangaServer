package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/internal/userstate"
	"nyamangaserver.local/server/internal/webassets"
	"nyamangaserver.local/server/migrations"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Serve is deliberately available only from the anonymous fixture tool.
func Serve(filename string) error {
	c, e := config.Load(filename)
	if e != nil {
		return e
	}
	if c.Listen != "127.0.0.1:18081" && c.Listen != "127.0.0.1:18082" {
		return errors.New("fixture server requires an isolated acceptance port")
	}
	if len(c.Roots) < 1 || len(c.Roots) > 2 || filepath.Base(filepath.Dir(c.DataDir)) == "dev" {
		return errors.New("fixture server requires an isolated anonymous instance")
	}
	// Write refuses any foreign files, and confirms the exact anonymous bytes.
	for _, root := range c.Roots {
		if e = testfixture.Write(root.Path); e != nil {
			return e
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return e
	}
	defer lock.Close()
	instanceBuild := maintenance.InstanceBuild{Version: "anonymous-m3-test", Commit: "anonymous-fixture"}
	if e = maintenance.ActivateInstance(ctx, c, instanceBuild, maintenance.InstanceActivationOptions{Epoch: store.InstanceOfflineEpoch, CheckRollback: store.CheckInstanceRollbackIdentity, FinishRollback: store.FinishInstanceRollback, ValidatePrepared: func(ctx context.Context, path string, roots []model.InstanceRebase) error {
		inventory, e := store.ValidateInstanceSchema(ctx, path, migrations.Files)
		if e != nil {
			return e
		}
		return maintenance.ValidatePreparedInstanceRoots(ctx, c, inventory, roots)
	}}); e != nil {
		return e
	}
	db, e := store.Open(ctx, c.DataDir, migrations.Files)
	if e != nil {
		return e
	}
	defer db.Close()
	if e = db.RecoverInstanceOperations(ctx); e != nil {
		return e
	}
	token, e := os.ReadFile(c.SetupTokenFile)
	if e != nil {
		return e
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cat := library.New(db, c, logger)
	defer cat.Close()
	remote := &testfixture.Remote{Vault: provider.NewVault(c.ProviderKeyFile, c.ProviderCredentialsFile)}
	meta := metadata.New(db, cat, remote, c.DataDir, logger)
	meta.PrepareDictionary(ctx)
	delay := 300 * time.Millisecond
	// Only this anonymous executable supports a fast deterministic runner. The
	// application server never imports fixtures or reads this test setting.
	if os.Getenv("NYAMANGA_FIXTURE_FAST_RECOMMENDATIONS") == "1" {
		delay = time.Millisecond
	}
	recommend := recommendation.New(db, cat, &testfixture.RecommendationRunner{Delay: delay}, c.DataDir, logger)
	tagImages := tagimages.New(db, c, cat.Pool)
	maint := maintenance.New(db, c, cat, fixtureDrain{meta, recommend}, logger).WithTagImages(tagImages)
	enhance := enhancements.New(db, fixtureEnhancements{meta, recommend, db})
	enhance.Start(ctx)
	imports := importflow.New(db, cat, logger, func() { cat.Wake(); meta.Wake(); recommend.Wake() }).WithCovers(remote, c.DataDir)
	handler := httpapi.New(c, auth.New(db, strings.TrimSpace(string(token))), webassets.Files(), logger, httpapi.Build{Version: "anonymous-m3-test"}, cat).WithMetadata(meta, ctx).WithOrganization(organization.New(db), search.New(db), userstate.New(db)).WithMaintenance(maint).WithRecommendations(recommend).WithImports(imports)
	handler.WithEnhancements(enhance, db).WithTagCatalog(tagcatalog.New(db)).WithTagImages(tagImages).WithGalleryHighlight(galleryhighlight.New(db))
	health := maintenance.NewHealth(db, c, cat.Pool)
	health.CoverBytes = cat.HealthCoverBytes
	health.DictionaryBytes = meta.HealthDictionary
	health.TagBytes = tagImages.HealthCacheBytes
	handler.WithHealth(health)
	health.Drain = func(ctx context.Context) error {
		return errors.Join(handler.DrainHealthHTTP(ctx), maint.Drain(ctx), cat.Drain(ctx), meta.Drain(ctx), meta.DrainDictionary(ctx), recommend.Drain(ctx))
	}
	instance := maintenance.NewInstance(db, c, instanceBuild, migrations.Files)
	instance.Inspect = store.ValidateInstanceSchema
	instance.PrepareDatabase = store.PrepareInstanceDatabase
	instance.Drain = func(ctx context.Context) error { return errors.Join(health.Drain(ctx), health.DrainChecks(ctx)) }
	handler.WithInstance(instance)
	instance.Start(ctx)
	health.Start(ctx)
	server := &http.Server{Addr: c.Listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second}
	cat.Start(ctx)
	meta.Start(ctx)
	maint.Start(ctx)
	recommend.Start(ctx)
	imports.Start(ctx)
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case e = <-done:
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	closeErr := errors.Join(server.Shutdown(shutdown), instance.Shutdown(shutdown), health.Shutdown(shutdown), imports.Shutdown(shutdown), enhance.Shutdown(shutdown), cat.Shutdown(shutdown), meta.Shutdown(shutdown), recommend.Shutdown(shutdown), maint.Shutdown(shutdown))
	if e != nil && e != http.ErrServerClosed {
		return fmt.Errorf("fixture server: %w", e)
	}
	return closeErr
}

type fixtureEnhancements struct {
	meta      *metadata.Service
	recommend *recommendation.Service
	db        *store.Store
}

func (r fixtureEnhancements) DrainEnhancement(ctx context.Context, feature string) error {
	if feature == model.EnhancementDictionary {
		return r.meta.DrainDictionary(ctx)
	}
	return r.recommend.DrainFeature(ctx, feature)
}
func (r fixtureEnhancements) RestoreDictionary(ctx context.Context) error {
	return r.meta.RestoreDictionary(ctx)
}
func (r fixtureEnhancements) EnhancementResources(ctx context.Context) map[string]string {
	dictionary, e := r.db.DictionaryResourceState(ctx)
	if e != nil {
		dictionary = "unavailable"
	}
	return map[string]string{model.EnhancementWork: "unverified", model.EnhancementSeries: "unverified", model.EnhancementDictionary: dictionary}
}

type fixtureDrain struct {
	metadata       *metadata.Service
	recommendation *recommendation.Service
}

func (d fixtureDrain) Drain(ctx context.Context) error {
	return errors.Join(d.metadata.Drain(ctx), d.recommendation.Drain(ctx))
}

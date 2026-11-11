package app

import (
	"context"
	"nyamangaserver.local/server/internal/metadata"
	"nyamangaserver.local/server/internal/model"
	"nyamangaserver.local/server/internal/recommendation"
	"nyamangaserver.local/server/internal/store"
)

type enhancementRuntime struct {
	recommend *recommendation.Service
	metadata  *metadata.Service
	db        *store.Store
}

func (r enhancementRuntime) DrainEnhancement(ctx context.Context, feature string) error {
	if feature == model.EnhancementDictionary {
		return r.metadata.DrainDictionary(ctx)
	}
	return r.recommend.DrainFeature(ctx, feature)
}
func (r enhancementRuntime) RestoreDictionary(ctx context.Context) error {
	return r.metadata.RestoreDictionary(ctx)
}
func (r enhancementRuntime) EnhancementResources(ctx context.Context) map[string]string {
	capability := r.recommend.Capabilities(ctx)
	state, _ := capability["resource_state"].(string)
	if state == "" {
		state = "unverified"
	}
	dictionary, e := r.db.DictionaryResourceState(ctx)
	if e != nil {
		dictionary = "unavailable"
	}
	return map[string]string{model.EnhancementWork: state, model.EnhancementSeries: state, model.EnhancementDictionary: dictionary}
}

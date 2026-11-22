package server

import (
	"fmt"
	"log/slog"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/generation"
	"github.com/calypr/loom/internal/ingest/sourceload"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func wireLoad(serverConfig Config, logger *slog.Logger, connOpts arangostore.ConnectionOptions, lifecycleClient *arangostore.Client, storage storageComponents, dataframe dataframeComponents, discoveryCache *catalog.Cache, scopeResolver *authscope.ScopeResolver, releaseService *publicationcontract.ReleaseService) (*loadapi.Service, loadapi.SourceLoader, loadapi.SourceLoadLimits, error) {
	lifecycleStore, explorerStore := storage.lifecycle, storage.explorer
	configuredRecipeName := storage.recipeName
	bundleTarget, recipeEngine, publishedRegistry := dataframe.target, dataframe.engine, dataframe.registry
	ingestRunner := loadapi.IngestRunner{BaseOptions: generation.LoadOptions{
		ConnectionOptions: connOpts,
	}}
	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration:      ingestRunner.RunGeneration,
		GenerationActivator: lifecycleStore,
		DataframeReleases:   publishedRegistry,
		Logger:              logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		return nil, nil, loadapi.SourceLoadLimits{}, fmt.Errorf("create generation load service: %w", err)
	}
	sourceLoadLimits := loadapi.SourceLoadLimits{MaxFileBytes: serverConfig.Server.SourceLoadMaxFileBytes}
	var sourceLoader loadapi.SourceLoader
	if bundleTarget != nil && configuredRecipeName != "" {
		publicationAdapter := sourceload.NewSourcePublicationAdapter(sourceload.SourcePublicationAdapterConfig{
			RecipeEngine: recipeEngine, PublicationTarget: bundleTarget, RecipeName: configuredRecipeName,
			PublishedRegistry: publishedRegistry, Explorer: explorerStore, ReleaseService: releaseService,
			DiscoveryCache: discoveryCache, ScopeResolver: scopeResolver,
			BatchRows: serverConfig.Server.RecipeBatchRows, BatchBytes: serverConfig.Server.RecipeBatchBytes,
			QualityMaxRows: serverConfig.Server.RecipeQualityMaxRows, QualityMaxDistinctKeys: serverConfig.Server.RecipeQualityMaxDistinctKeys,
		})
		sourceLoader, err = sourceload.NewRepositorySourceLoader(sourceload.SourceLoadPublisherConfig{
			Client: lifecycleClient, DatasetStore: lifecycleStore,
			ValidateProject: publicationAdapter.ValidateProject, MaterializeAndSelect: publicationAdapter.MaterializeAndSelect,
		})
		if err != nil {
			return nil, nil, loadapi.SourceLoadLimits{}, fmt.Errorf("create per-file source loader: %w", err)
		}
	}
	return generationService, sourceLoader, sourceLoadLimits, nil
}

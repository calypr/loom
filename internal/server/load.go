package server

import (
	"context"
	"fmt"
	"log/slog"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog/discoverycache"
	"github.com/calypr/loom/internal/dataframe/publication"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/projectconfig"
	"github.com/calypr/loom/internal/ingest/generation"
	"github.com/calypr/loom/internal/ingest/sourceload"
	"github.com/calypr/loom/internal/releasecommit"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func wireLoad(serverConfig Config, logger *slog.Logger, connOpts arangostore.ConnectionOptions, lifecycleClient *arangostore.Client, storage storageComponents, dataframe dataframeComponents, discoveryCache *discoverycache.Cache, scopeResolver *authscope.ScopeResolver, releaseService *publicationcontract.ReleaseService) (*loadapi.Service, loadapi.SourceLoader, loadapi.SourceLoadLimits, error) {
	lifecycleStore := storage.lifecycle
	configuredRecipeName := storage.recipeName
	bundleTarget, recipeEngine, publishedRegistry := dataframe.target, dataframe.engine, dataframe.registry
	ingestRunner := loadapi.IngestRunner{BaseOptions: generation.LoadOptions{
		ConnectionOptions: connOpts,
	}}
	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration: ingestRunner.RunGeneration,
		Manifests:      lifecycleStore,
		ActivateBundle: func(ctx context.Context, manifest publicationcontract.Manifest, execution publication.BundleExecution) error {
			return releasecommit.ActivateBundle(ctx, lifecycleClient, *releaseService, manifest, execution)
		},
		DataframeReleases: publishedRegistry,
		Logger:            logger,
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
		publisher := projectconfig.NewSourcePublisher(projectconfig.SourcePublisherConfig{
			RecipeEngine: recipeEngine, PublicationTarget: bundleTarget, RecipeName: configuredRecipeName,
			PublishedRegistry: publishedRegistry, ListOwners: storage.explorerOwners.List,
			ReadSettings: storage.explorerViewBuilder.GetViewBuilderSettings, Commit: storage.explorerPublisher.PublishProjectConfigs, ReleaseService: releaseService,
			DiscoveryCache: discoveryCache, ScopeResolver: scopeResolver,
			BatchRows: serverConfig.Server.RecipeBatchRows, BatchBytes: serverConfig.Server.RecipeBatchBytes,
			QualityMaxRows: serverConfig.Server.RecipeQualityMaxRows, QualityMaxDistinctKeys: serverConfig.Server.RecipeQualityMaxDistinctKeys,
		})
		sourceLoader, err = sourceload.NewRepositorySourceLoader(sourceload.SourceLoadPublisherConfig{
			Client: lifecycleClient, DatasetStore: lifecycleStore, Inventory: storage.semantic,
			ValidateProject: publisher.ValidateProject, MaterializeAndSelect: publisher.MaterializeAndSelect,
		})
		if err != nil {
			return nil, nil, loadapi.SourceLoadLimits{}, fmt.Errorf("create per-file source loader: %w", err)
		}
	}
	return generationService, sourceLoader, sourceLoadLimits, nil
}

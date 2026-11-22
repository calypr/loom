package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	queryapi "github.com/calypr/loom/internal/api/dataframe/query"
	explorerapi "github.com/calypr/loom/internal/api/explorer"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	chartifactarango "github.com/calypr/loom/internal/dataframe/execution/chartifact/arango"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	bundlearango "github.com/calypr/loom/internal/dataframe/publication/arango"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

type dataframeReadRoutes struct {
	published *dataframeapi.Service
	query     *queryapi.Service
}

type dataframeComponents struct {
	service     *dataframeexecution.Service
	registry    *bundlearango.Registry
	clickhouse  *clickhousestore.Client
	reader      *published.Reader
	artifacts   *chartifact.Manager
	engine      *dataframeexecution.Engine
	target      publication.Target
	degradation error
}

func wireDataframe(ctx context.Context, serverConfig Config, logger *slog.Logger, lifecycleClient *arangostore.Client, storage storageComponents, discoveryCache *catalog.Cache, scopeResolver *authscope.ScopeResolver) (_ dataframeComponents, err error) {
	catalogStore, recipeRegistry, lifecycleStore := storage.catalog, storage.recipes, storage.lifecycle
	activeManifestResolver := publicationcontract.ActiveResolver(lifecycleStore)
	degradation := storage.degradation
	queries := executionarango.NewQueryAdapter(lifecycleClient, logger, explorerapi.PreserveConfiguredQueryError)
	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{QueryRows: queries.ReadRows})

	publishedRegistry, err := bundlearango.New(lifecycleClient)
	if err != nil {
		return dataframeComponents{}, fmt.Errorf("create published dataframe registry: %w", err)
	}
	publicationReady := true
	if err := lifecycleClient.Bootstrap(ctx, bundlearango.BootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap dataframe registry", err)
		publicationReady = false
	}
	var clickhouse *clickhousestore.Client
	var materializationReader *published.Reader
	var privateClickHouseArtifacts *chartifact.Manager
	if serverConfig.Server.ClickHouse.Enabled {
		clickhouse, err = clickhousestore.New(clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password})
		if err != nil {
			return dataframeComponents{}, fmt.Errorf("create ClickHouse client: %w", err)
		}
		defer func() {
			if err != nil {
				clickhouse.Close()
			}
		}()

		if err := clickhouse.EnsureDatabase(ctx); err != nil {
			degradation = recordDegradation(logger, degradation, "ClickHouse database", err)
			publicationReady = false
		}
		if err := lifecycleClient.Bootstrap(ctx, chartifactarango.BootstrapSpec()); err != nil {
			return dataframeComponents{}, fmt.Errorf("bootstrap private dataframe artifact catalog: %w", err)
		}
		privateArtifactCatalog, err := chartifactarango.NewCatalog(lifecycleClient)
		if err != nil {
			return dataframeComponents{}, fmt.Errorf("create private dataframe artifact catalog: %w", err)
		}
		privateClickHouseArtifacts, err = chartifact.New(chartifact.Config{
			Catalog: privateArtifactCatalog, ClickHouse: clickhouse,
			BatchRows: serverConfig.Server.RecipeBatchRows, BatchBytes: serverConfig.Server.RecipeBatchBytes,
		})
		if err != nil {
			return dataframeComponents{}, fmt.Errorf("create private ClickHouse artifact manager: %w", err)
		}
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: publishedRegistry, Logger: logger, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver, ActiveReleaseResolver: lifecycleStore}
	}
	recipeRevisions, err := recipearango.NewRevisionRegistry(lifecycleClient)
	if err != nil {
		return dataframeComponents{}, fmt.Errorf("create recipe revision registry: %w", err)
	}
	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	if clickhouse != nil {
		clickHouseQueryRows = clickhouse.QueryRowsArgsVisit
	}
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if materializationReader != nil {
		resolver := reciperuntime.NewCombineInputResolver(materializationReader, scopeResolver)
		resolveClickHouseInputs = resolver.Resolve
		withExecutionReadPins = materializationReader.WithExecutionReadPins
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:                   recipeRegistry,
		Revisions:                  recipeRevisions,
		ResolveBundle:              reciperuntime.RecipeSchemaResolver(catalogStore.DiscoverFields, discoveryCache),
		PreparePreviewIndex:        queries.PreviewIndexPreparation(),
		PreviewCoveringIndexes:     lifecycleClient.PreviewIndexes().ListPreviewCoveringIndexes,
		PreviewCollectionRevision:  lifecycleClient.CollectionRevision,
		PreviewExplainQuery:        queries.Explain,
		PreviewCollectionCount:     lifecycleClient.CollectionCount,
		ClickHouseQueryRows:        clickHouseQueryRows,
		PreviewQueryRows:           queries.PreviewQueryRows(),
		WithPreviewReadSnapshot:    queries.WithPreviewReadSnapshot,
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		QueryRows:                  queries.QueryRows(),
		ScopeDigest:                reciperuntime.ScopeDigest,
		RootPageRows:               serverConfig.Server.RecipeQueryPageRows,
	})
	if err != nil {
		return dataframeComponents{}, fmt.Errorf("create dataframe recipe engine: %w", err)
	}
	var bundleTarget publication.Target
	if serverConfig.Server.ClickHouse.Enabled && publicationReady {
		bundleStore, err := publicationclickhouse.NewBundleStore(clickhouse, publishedRegistry)
		if err != nil {
			return dataframeComponents{}, fmt.Errorf("create dataframe bundle store: %w", err)
		}
		bundleStore.SetActiveReleaseReader(lifecycleStore)
		if err := bundleStore.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
			degradation = recordDegradation(logger, degradation, "dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget = bundleStore
		}
	}
	return dataframeComponents{
		service: dataframes, registry: publishedRegistry, clickhouse: clickhouse, reader: materializationReader,
		artifacts: privateClickHouseArtifacts, engine: recipeEngine, target: bundleTarget, degradation: degradation,
	}, nil
}

func wireDataframeReads(logger *slog.Logger, lifecycleClient *arangostore.Client, storage storageComponents, dataframe dataframeComponents, discoveryCache *catalog.Cache, scopeResolver *authscope.ScopeResolver) dataframeReadRoutes {
	discoverFields := discoveryCache.DiscoverFields(storage.catalog.DiscoverFields)
	discoverReferences := discoveryCache.DiscoverReferences(storage.catalog.DiscoverReferences)
	activeManifestResolver := publicationcontract.ActiveResolver(storage.lifecycle)
	dataframes, materializationReader := dataframe.service, dataframe.reader
	queryConfig := queryapi.Config{
		DiscoverReferences:     discoverReferences,
		DiscoverFields:         discoverFields,
		Dataframes:             dataframes,
		ScopeResolver:          scopeResolver,
		ActiveManifestResolver: activeManifestResolver,
		Explain: func(ctx context.Context, compiled dataframeexecution.CompiledQuery) error {
			_, err := executionarango.ExplainCompiledQuery(ctx, lifecycleClient, compiled)
			return err
		},
	}
	dataframeReads := dataframeReadRoutes{
		query: queryapi.NewService(queryConfig),
		published: dataframeapi.NewService(dataframeapi.Config{
			Reader: materializationReader, ScopeResolver: scopeResolver, Logger: logger,
		}),
	}
	return dataframeReads
}

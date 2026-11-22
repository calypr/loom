package server

import (
	"context"
	"fmt"
	"log/slog"

	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	explorerapi "github.com/calypr/loom/internal/api/explorer"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/published"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
	"github.com/calypr/loom/internal/explorer/importedsource"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type explorerComponents struct {
	capability *capabilityresolver.Resolver
	service    *explorer.Service
	artifacts  *artifactfs.Store
}

func wireExplorer(ctx context.Context, serverConfig Config, lifecycleClient *arangostore.Client, storage storageComponents, scopeResolver *authscope.ScopeResolver) (explorerComponents, error) {
	explorerStore, catalogStore := storage.explorer, storage.catalog
	activeManifestResolver := publicationcontract.ActiveResolver(storage.lifecycle)
	if err := explorerStore.MigrateLegacyRepositoryConfigs(ctx); err != nil {
		return explorerComponents{}, fmt.Errorf("migrate legacy Explorer repository configs: %w", err)
	}
	capabilitySnapshots, err := explorerarango.NewCapabilitySnapshotStore(lifecycleClient)
	if err != nil {
		return explorerComponents{}, fmt.Errorf("create Explorer capability snapshot store: %w", err)
	}
	resolver, err := capabilityresolver.NewResolver(catalogStore, scopeResolver, activeManifestResolver, capabilitySnapshots)
	if err != nil {
		return explorerComponents{}, fmt.Errorf("create Explorer capability resolver: %w", err)
	}
	explorerService, err := explorer.NewService(explorerStore)
	if err != nil {
		return explorerComponents{}, fmt.Errorf("create Explorer service: %w", err)
	}
	artifactStore, err := artifactfs.New(serverConfig.Server.ArtifactDirectory)
	if err != nil {
		return explorerComponents{}, fmt.Errorf("create Explorer artifact store: %w", err)
	}
	return explorerComponents{capability: resolver, service: explorerService, artifacts: artifactStore}, nil
}

func wireLifecycle(serverConfig Config, logger *slog.Logger, storage storageComponents, dataframe dataframeComponents, explorerDeps explorerComponents, scopeResolver *authscope.ScopeResolver, releaseService *publicationcontract.ReleaseService, releaseRuntime *publicationcontract.ReleaseRuntime) (lifecycle.Config, error) {
	recipeRegistry, catalogStore, explorerStore := storage.recipes, storage.catalog, storage.explorer
	configuredRecipeName, configuredRecipeVersion := storage.recipeName, storage.recipeVersion
	recipeEngine, bundleTarget, publishedRegistry := dataframe.engine, dataframe.target, dataframe.registry
	materializationReader, degradation := dataframe.reader, dataframe.degradation
	capabilityResolver, explorerService, artifactStore := explorerDeps.capability, explorerDeps.service, explorerDeps.artifacts
	validateExplorerReleaseGeneration := releaseService.ValidateGeneration
	importedSources := importedsource.New(recipeRegistry, recipeEngine, capabilityResolver, configuredRecipeName, configuredRecipeVersion)
	var combineInputSchemas receiptapi.CombineInputSchemaResolver
	if materializationReader != nil {
		combineInputSchemas = receiptapi.NewCombineInputSchemaResolver(materializationReader, materializationReader.WithExecutionReadPins, scopeResolver)
	}
	receiptRuntime := receiptapi.NewRuntime(receiptapi.RuntimeConfig{
		Engine: recipeEngine, Explorers: explorerService, CapabilityResolver: capabilityResolver,
		Logger: logger, CombineInputSchemas: combineInputSchemas, ImportedSources: importedSources,
		ConstructionSourceStage: importedSources.SourceStage,
		Materializer: receiptapi.NewMaterializer(
			recipeEngine, bundleTarget, publishedRegistry, degradation, logger,
			serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes,
			serverConfig.Server.RecipeQualityMaxRows, serverConfig.Server.RecipeQualityMaxDistinctKeys,
		),
	})
	compileReceipt := receiptRuntime.CompileReceipt
	persistPublishedWorkspace, err := explorer.LocalWorkspaceWriter(serverConfig.Server.LocalWorkspaceWriteback, serverConfig.Server.LocalWorkspaceProject)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure local workspace writeback: %w", err)
	}
	populationMappingCursorCodec, err := lifecycle.NewHMACPopulationMappingCursorCodec(serverConfig.Server.PopulationMappingCursorSecret)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure population mapping cursor signing: %w", err)
	}
	var artifactPublishedReader ports.ArtifactPublishedReader
	if materializationReader != nil {
		artifactPublishedReader = materializationReader
		if serverConfig.Server.DevArtifactRowDelay > 0 {
			artifactPublishedReader = lifecycle.DelayArtifactRows(artifactPublishedReader, serverConfig.Server.DevArtifactRowDelay)
		}
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("load generated schema index for row choices: %w", err)
	}
	rowChoiceResolver, err := lifecycle.NewSchemaRowChoiceResolver(schemaIndex)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure row-choice schema resolver: %w", err)
	}
	explicitGroupResolver, err := lifecycle.NewRepositoryExplicitGroupRevisionResolver(explorerStore)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure explicit group revision resolver: %w", err)
	}
	var selectionSourceResolver lifecycle.SelectionSourceResolver
	if materializationReader != nil {
		selectionSourceResolver = published.SelectionSourceAdapter{Reader: materializationReader}
	}
	lifecycleConfig := lifecycle.Config{
		DefaultDataframeWorkspace:          importedSources.DefaultWorkspace,
		EditImportedSource:                 importedSources.Edit,
		CreateImportedSourceRule:           importedSources.CreateSourceRule,
		EditImportedSources:                importedSources.EditBatch,
		RemoveImportedField:                importedSources.RemoveField,
		RemoveImportedOutput:               importedSources.RemoveOutput,
		EditImportedRelationship:           importedSources.EditRelationship,
		AuthoringNetwork:                   importedSources.Network,
		SemanticInventory:                  catalogStore.PageSemanticInventory,
		SemanticCoverage:                   catalogStore.LookupSemanticCoverage,
		ResolveSemanticInventorySelections: catalogStore.ResolveSemanticInventorySelections,
		SelectionMembersCollection:         explorerarango.SelectionMembersCollection,
		InterpretationRepository:           explorerStore,
		PopulationMappingCursorCodec:       populationMappingCursorCodec,
		SelectionSourceResolver:            selectionSourceResolver,
		SelectionReferenceValidator:        explorerStore.ValidateSelectionReferences,
		RowChoiceResolver:                  rowChoiceResolver,
		RowChoicePlanner:                   rowChoiceResolver,
		ExplicitGroupResolver:              explicitGroupResolver,
		ExplicitGroupRepository:            explorerStore,
		ScanCategories:                     receiptRuntime.ScanCategories,
		ScanColumnPopulation:               receiptRuntime.ScanColumnPopulation,
		CompileReceipt:                     compileReceipt,
		ConstructionSourceStage:            receiptRuntime.ConstructionSourceStage,
		Capability: ports.CapabilityResolver{
			Current: func(ctx context.Context, project, _ string, generation string) (capability.Snapshot, error) {
				return capabilityResolver.Resolve(ctx, project, generation)
			},
			Token:          capabilityResolver.ResolveToken,
			ForCompilation: capabilityResolver.ResolveForCompilation,
			ForExecution:   capabilityResolver.ResolveForExecution,
			Catalog:        authoringv2.CatalogFromCapability,
		},
		ReceiptLookup:             receiptRuntime.ReceiptLookup,
		ArtifactStore:             artifactStore,
		PublishedReader:           artifactPublishedReader,
		ArtifactTTL:               serverConfig.Server.ArtifactTTL,
		ArtifactMaxRows:           serverConfig.Server.ArtifactMaxRows,
		ArtifactMaxBytes:          serverConfig.Server.ArtifactMaxBytes,
		PreviewReceipt:            receiptRuntime.PreviewReceipt,
		ValidateReceiptStream:     receiptRuntime.ValidateReceiptStream,
		PopulationMapping:         receiptRuntime.PopulationMapping,
		CellTrace:                 receiptRuntime.CellTrace,
		RowLineage:                receiptRuntime.RowLineage,
		MaterializeReceipt:        receiptRuntime.MaterializeReceipt,
		ValidateReleaseGeneration: validateExplorerReleaseGeneration,
		ActivateRelease:           releaseRuntime.Activate,
		PrepareRelease:            releaseRuntime.Prepare,
		PersistPublishedWorkspace: persistPublishedWorkspace,
	}
	return lifecycleConfig, nil
}

func wireExplorerHandlers(authorizer authscope.Authorizer, scopeResolver *authscope.ScopeResolver, dataframe dataframeComponents, explorerDeps explorerComponents, lifecycleConfig lifecycle.Config) *explorerapi.Handlers {
	materializationReader, publishedRegistry := dataframe.reader, dataframe.registry
	explorerService := explorerDeps.service
	readRecipeExecution := dataframeapi.NewRecipeExecutionAuthorizer(publishedRegistry, scopeResolver)
	var constructionInputsReader constructioninputs.ExactReader
	if materializationReader != nil {
		constructionInputsReader = materializationReader
	}
	return explorerapi.NewHandlers(explorerapi.Config{
		Authorizer: authorizer,
		AuthorizeRead: func(ctx context.Context, principal *authscope.Principal, project string) error {
			if scopeResolver == nil {
				return nil
			}
			return scopeResolver.AuthorizeReadProject(ctx, principal, project)
		},
		Explorers:          explorerService,
		Lifecycle:          lifecycleConfig,
		ConstructionInputs: constructioninputs.New(constructionInputsReader, publishedRegistry, lifecycleConfig.Capability, scopeResolver, explorerService, explorerService),
		AuthorizeExecution: readRecipeExecution,
	})
}

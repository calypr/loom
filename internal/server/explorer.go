package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/calypr/loom/internal/explorer/selection"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/api/dataframe/recipeexec"
	"github.com/calypr/loom/internal/explorer/arango/capabilitydb"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/population"

	explorerapi "github.com/calypr/loom/internal/api/explorer"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/published"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	explorerdb "github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/artifactfs"
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
	explorerStore, catalogStore := storage.explorerOwners, storage.catalog
	activeManifestResolver := publicationcontract.ActiveResolver(storage.lifecycle)
	if err := explorerarango.MigrateLegacyRepositoryConfigs(ctx, lifecycleClient, storage.explorerOwners, storage.explorerDrafts, storage.explorerRevisions, storage.explorerProjections); err != nil {
		return explorerComponents{}, fmt.Errorf("migrate legacy Explorer repository configs: %w", err)
	}
	capabilitySnapshots, err := capabilitydb.NewCapabilitySnapshotStore(lifecycleClient)
	if err != nil {
		return explorerComponents{}, fmt.Errorf("create Explorer capability snapshot store: %w", err)
	}
	resolver, err := capabilityresolver.NewResolver(catalogStore, scopeResolver, activeManifestResolver, capabilitySnapshots)
	if err != nil {
		return explorerComponents{}, fmt.Errorf("create Explorer capability resolver: %w", err)
	}
	explorerService, err := explorer.NewService(explorer.ServiceDependencies{
		Owners: explorerStore, Drafts: storage.explorerDrafts,
		Revisions: storage.explorerRevisions, ViewBuilder: storage.explorerViewBuilder,
		WorkspaceProjections: storage.explorerProjections,
	})
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
	recipeRegistry, semanticStore := storage.recipes, storage.semantic
	configuredRecipeName, configuredRecipeVersion := storage.recipeName, storage.recipeVersion
	recipeEngine, bundleTarget, publishedRegistry := dataframe.engine, dataframe.target, dataframe.registry
	materializationReader, degradation := dataframe.reader, dataframe.degradation
	capabilityResolver, artifactStore := explorerDeps.capability, explorerDeps.artifacts
	validateExplorerReleaseGeneration := releaseService.ValidateGeneration
	importedSources := importedsource.New(recipeRegistry, recipeEngine, capabilityResolver, configuredRecipeName, configuredRecipeVersion)
	var combineInputSchemas receiptapi.CombineInputSchemaResolver
	if materializationReader != nil {
		combineInputSchemas = receiptapi.NewCombineInputSchemaResolver(materializationReader, materializationReader.WithExecutionReadPins, scopeResolver)
	}
	receiptRuntime := receiptapi.NewRuntime(receiptapi.RuntimeConfig{
		Engine: recipeEngine, Receipts: storage.explorerReceipts, CapabilityResolver: capabilityResolver,
		Logger: logger, CombineInputSchemas: combineInputSchemas, ImportedSources: &importedSources,
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
	populationMappingCursorCodec, err := population.NewHMACPopulationMappingCursorCodec(serverConfig.Server.PopulationMappingCursorSecret)
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
	explicitGroupResolver, err := explicitgroup.NewRepositoryExplicitGroupRevisionResolver(storage.explorerGroups, storage.explorerSelections)
	if err != nil {
		return lifecycle.Config{}, fmt.Errorf("configure explicit group revision resolver: %w", err)
	}
	var selectionSourceResolver selection.SelectionSourceResolver
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
		SemanticInventory:                  semanticStore.PageSemanticInventory,
		SemanticCoverage:                   semanticStore.LookupSemanticCoverage,
		ResolveSemanticInventorySelections: semanticStore.ResolveSemanticInventorySelections,
		SelectionMembersCollection:         explorerdb.SelectionMembersCollection,
		InterpretationRepository:           storage.explorerInterpretation,
		PopulationMappingCursorCodec:       populationMappingCursorCodec,
		SelectionSourceResolver:            selectionSourceResolver,
		SelectionReferenceValidator:        storage.explorerSelections.ValidateSelectionReferences,
		ExplorerOwners:                     storage.explorerOwners,
		DraftStore:                         storage.explorerDrafts,
		DraftRevisions:                     storage.explorerDraftRevisions,
		Revisions:                          storage.explorerRevisions,
		RevisionWriter:                     storage.explorerRevisions,
		Receipts:                           storage.explorerReceipts,
		AuthoringPublisher:                 storage.explorerPublisher,
		RepositoryActivator:                storage.explorerActivator,
		SelectionStore:                     storage.explorerSelections,
		RowChoiceResolver:                  rowChoiceResolver,
		RowChoicePlanner:                   rowChoiceResolver,
		ExplicitGroupResolver:              explicitGroupResolver,
		ExplicitGroupRepository:            storage.explorerGroups,
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
			Catalog:        ws.CatalogFromCapability,
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
		PrepareRepositoryRelease: releaseRuntime.PrepareRepository,
		PrepareRelease:            releaseRuntime.Prepare,
		PersistPublishedWorkspace: persistPublishedWorkspace,
	}
	return lifecycleConfig, nil
}

func wireExplorerHandlers(authorizer authscope.Authorizer, scopeResolver *authscope.ScopeResolver, dataframe dataframeComponents, explorerDeps explorerComponents, storage storageComponents, lifecycleConfig lifecycle.Config) (*explorerapi.Handlers, error) {
	materializationReader, publishedRegistry := dataframe.reader, dataframe.registry
	explorerService := explorerDeps.service
	application, err := lifecycle.New(explorerService, lifecycleConfig)
	if err != nil {
		return nil, fmt.Errorf("create Explorer lifecycle application: %w", err)
	}
	readRecipeExecution := recipeexec.NewRecipeExecutionAuthorizer(publishedRegistry, scopeResolver)
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
		Application:        application,
		Revisions:          storage.explorerRevisions,
		ViewBuilder:        storage.explorerViewBuilder,
		ConstructionInputs: constructioninputs.New(constructionInputsReader, publishedRegistry, lifecycleConfig.Capability, scopeResolver, explorerService, storage.explorerRevisions),
		AuthorizeExecution: readRecipeExecution,
	}), nil
}

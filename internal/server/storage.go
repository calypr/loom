package server

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/catalog/fieldprofile"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"

	recipearango "github.com/calypr/loom/internal/dataframe/recipe/arango"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type storageComponents struct {
	explorerOwners         *workspacedb.WorkspaceStore
	explorerDrafts         explorer.DraftStore
	explorerDraftRevisions explorer.DraftRevisionReader
	explorerRevisions      *workspacedb.RevisionStore
	explorerSelections     *selectiondb.SelectionStore
	explorerGroups         *explorerarango.ExplicitGroupStore
	explorerInterpretation *explorerarango.InterpretationStore
	explorerReceipts       *publicationdb.ReceiptStore
	explorerPublisher      *publicationdb.PublicationStore
	explorerActivator      *publicationdb.PublicationStore
	explorerViewBuilder    *publicationdb.ViewBuilderStore
	explorerProjections    *explorerarango.WorkspaceProjectionStore
	recipes                *recipearango.Registry
	lifecycle              *publicationarango.Store
	catalog                *catalogarango.Store
	semantic               *semanticarango.Store
	degradation            error
	recipeName             string
	recipeVersion          string
}

func wireStorage(ctx context.Context, serverConfig Config, logger *slog.Logger, lifecycleClient *arangostore.Client) (storageComponents, error) {
	if err := lifecycleClient.Bootstrap(ctx, publicationarango.BootstrapSpec()); err != nil {
		return storageComponents{}, fmt.Errorf("bootstrap dataset lifecycle store: %w", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, arangostore.BootstrapSpec{
		Collections: []arangostore.CollectionSpec{
			{Name: fieldprofile.FieldCatalogCollection},
			semanticarango.SemanticCoverageCollectionSpec(),
		},
	}); err != nil {
		return storageComponents{}, fmt.Errorf("bootstrap authorization catalog: %w", err)
	}
	var degradation error
	if err := lifecycleClient.Bootstrap(ctx, recipearango.BootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, recipearango.RevisionBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe revision registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, explorerarango.BootstrapSpec()); err != nil {
		return storageComponents{}, fmt.Errorf("bootstrap Explorer store: %w", err)
	}
	explorerSelections := selectiondb.NewSelectionStore(lifecycleClient)
	explorerRevisions := workspacedb.NewRevisionStore(lifecycleClient)
	explorerOwners := workspacedb.NewWorkspaceStore(lifecycleClient)
	explorerDrafts := explorerarango.NewDraftStore(lifecycleClient)
	explorerGroups := explorerarango.NewExplicitGroupStore(lifecycleClient, explorerSelections)
	explorerInterpretation := explorerarango.NewInterpretationStore(lifecycleClient)
	explorerReceipts := publicationdb.NewReceiptStore(lifecycleClient)
	explorerPublisher := publicationdb.NewPublicationStore(lifecycleClient)
	explorerActivator := publicationdb.NewPublicationStore(lifecycleClient)
	explorerViewBuilder := publicationdb.NewViewBuilderStore(lifecycleClient)
	explorerProjections := explorerarango.NewWorkspaceProjectionStore(lifecycleClient)
	recipeRegistry, err := recipearango.New(lifecycleClient)
	if err != nil {
		return storageComponents{}, fmt.Errorf("create recipe registry: %w", err)
	}
	var configuredRecipeName, configuredRecipeVersion string
	if serverConfig.Server.ClickHouse.Enabled {
		data, err := os.ReadFile(serverConfig.Server.Dataframer.Recipe)
		if err != nil {
			return storageComponents{}, fmt.Errorf("read dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		defaultBundle, err := recipe.Parse(data)
		if err != nil {
			return storageComponents{}, fmt.Errorf("parse dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		configuredRecipeName = defaultBundle.Name
		configuredRecipeVersion = defaultBundle.TranslationVersion
		if _, err := (recipe.PersistentRegistry{Store: recipeRegistry}).RegisterVersion(ctx, defaultBundle); err != nil {
			degradation = recordDegradation(logger, degradation, "register default dataframe recipe", err)
		}
	}
	lifecycleStore, err := publicationarango.New(lifecycleClient)
	if err != nil {
		return storageComponents{}, fmt.Errorf("create dataset lifecycle store: %w", err)
	}

	catalogStore, err := catalogarango.New(lifecycleClient)
	if err != nil {
		return storageComponents{}, fmt.Errorf("create catalog store: %w", err)
	}
	semanticStore, err := semanticarango.New(lifecycleClient)
	if err != nil {
		return storageComponents{}, fmt.Errorf("create semantic store: %w", err)
	}
	return storageComponents{
		explorerOwners: explorerOwners, explorerDrafts: explorerDrafts, explorerDraftRevisions: explorerDrafts, explorerRevisions: explorerRevisions,
		explorerSelections: explorerSelections, explorerGroups: explorerGroups, explorerInterpretation: explorerInterpretation,
		explorerReceipts: explorerReceipts, explorerPublisher: explorerPublisher, explorerActivator: explorerActivator,
		explorerViewBuilder: explorerViewBuilder, explorerProjections: explorerProjections,
		recipes: recipeRegistry, lifecycle: lifecycleStore, catalog: catalogStore, semantic: semanticStore,
		degradation: degradation, recipeName: configuredRecipeName, recipeVersion: configuredRecipeVersion,
	}, nil
}

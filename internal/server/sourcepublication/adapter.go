package sourcepublication

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog/discoverycache"
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	"github.com/calypr/loom/internal/dataframe/recipeauth"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
	"github.com/calypr/loom/internal/explorer/projectconfig"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
)

type SourcePublicationAdapterConfig struct {
	RecipeEngine           *execution.Engine
	PublicationTarget      publication.Target
	RecipeName             string
	PublishedRegistry      publication.BundleCatalog
	Owners                 *workspacedb.WorkspaceStore
	ViewBuilder            *publicationdb.ViewBuilderStore
	Publisher              *publicationdb.PublicationStore
	ReleaseService         *dataset.ReleaseService
	DiscoveryCache         *discoverycache.Cache
	ScopeResolver          *authscope.ScopeResolver
	BatchRows              int
	BatchBytes             int
	QualityMaxRows         int64
	QualityMaxDistinctKeys int64
}

type SourcePublicationAdapter struct {
	config SourcePublicationAdapterConfig
}

func NewSourcePublicationAdapter(config SourcePublicationAdapterConfig) *SourcePublicationAdapter {
	return &SourcePublicationAdapter{config: config}
}

func (a *SourcePublicationAdapter) ValidateProject(ctx context.Context, project string) error {
	if a.config.ScopeResolver == nil {
		return nil
	}
	principal, _ := authscope.PrincipalFromContext(ctx)
	if err := a.config.ScopeResolver.AuthorizeReadProject(ctx, principal, project); err != nil {
		return recipeauth.AuthorizationError(err)
	}
	return nil
}

func (a *SourcePublicationAdapter) MaterializeAndSelect(
	ctx context.Context,
	project, generation, operationID, commit string,
	expectedRevision int64,
) (string, int64, error) {
	config := a.config
	bindings, err := recipeauth.NewAuthorization(config.ScopeResolver, selection.MembersCollection).AuthorizeRead(ctx, recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
	})
	if err != nil {
		return "", 0, err
	}
	var identity publication.BundleIdentity
	resolved, err := config.RecipeEngine.Materialize(ctx, config.RecipeName, bindings, func(publishCtx context.Context, full execution.Resolved) error {
		var publishErr error
		identity, publishErr = reciperuntime.PublishResolvedRecipe(publishCtx, reciperuntime.PublishRequest{
			Engine: config.RecipeEngine, Target: config.PublicationTarget,
			Name: config.RecipeName, Bindings: bindings, Resolved: full, ReceiptID: operationID,
			BatchRows: config.BatchRows, BatchBytes: config.BatchBytes,
			QualityMaxRows: config.QualityMaxRows, QualityMaxDistinctKeys: config.QualityMaxDistinctKeys,
		})
		return publishErr
	})
	if err != nil {
		return "", 0, err
	}
	executionRecord, err := config.PublishedRegistry.FindExecutionByKey(ctx, identity.Key())
	if err != nil {
		return "", 0, err
	}
	if !executionRecord.State.Successful() ||
		executionRecord.ReceiptID != operationID ||
		executionRecord.Project != project ||
		executionRecord.DatasetGeneration != generation {
		return "", 0, errors.New("configured recipe did not publish the candidate generation")
	}
	catalogSnapshot, err := projectconfig.PrepareViewBuilderCatalogSnapshot(resolved, executionRecord, commit)
	if err != nil {
		return "", 0, fmt.Errorf("prepare view builder catalog snapshot: %w", err)
	}
	settings, err := config.ViewBuilder.GetViewBuilderSettings(ctx, project)
	if err != nil {
		return "", 0, fmt.Errorf("load project view builder settings: %w", err)
	}
	catalogRevisionID, catalogAnchored := settings.Catalogs[config.RecipeName]
	if catalogAnchored &&
		(strings.TrimSpace(catalogRevisionID) == "" || catalogRevisionID != strings.TrimSpace(catalogRevisionID)) {
		return "", 0, fmt.Errorf("project view builder catalog anchor for recipe %q is invalid", config.RecipeName)
	}
	owners, err := config.Owners.List(ctx, projectid.Legacy(project))
	if err != nil {
		return "", 0, fmt.Errorf("load project-owned Explorer views: %w", err)
	}
	definitions, expectedConfigRevisionIDs, err := projectconfig.ProjectConfigsForSourcePublication(
		owners, config.RecipeName, catalogAnchored, catalogSnapshot,
	)
	if err != nil {
		return "", 0, fmt.Errorf("resolve project views for source publication: %w", err)
	}
	selectors := make([]dataset.DataframeSelector, 0, len(executionRecord.Outputs))
	for _, output := range executionRecord.Outputs {
		selectors = append(selectors, executionRecord.Selector(output.Name))
	}
	configs, err := projectconfig.PrepareProjectConfigsForPublication(definitions, resolved, executionRecord, commit)
	if err != nil {
		return "", 0, fmt.Errorf("prepare project configs: %w", err)
	}
	candidateReleaseService := *config.ReleaseService
	candidateReleaseService.Required = append(append([]dataset.DataframeSelector(nil), config.ReleaseService.Required...), selectors...)
	release, err := candidateReleaseService.Create(ctx, dataset.ActivationRequest{
		Project: project, Generation: generation, GitCommit: commit,
		ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return "", 0, err
	}
	active, err := config.Publisher.PublishProjectConfigs(ctx, explorer.ProjectConfigPublishRequest{
		RecipeName: config.RecipeName, Configs: configs, Release: release,
		ExpectedReleaseRevision: expectedRevision, ExpectedConfigRevisionIDs: expectedConfigRevisionIDs,
		CatalogSnapshot: &catalogSnapshot,
	})
	if err != nil {
		return "", 0, fmt.Errorf("activate project publication: %w", err)
	}
	config.DiscoveryCache.InvalidateProject(project)
	if config.ScopeResolver != nil {
		config.ScopeResolver.InvalidateProject(project)
	}
	return executionRecord.ID, active.Revision, nil
}

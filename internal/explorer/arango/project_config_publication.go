package arango

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	publicationdata "github.com/calypr/loom/internal/dataframe/publication"
	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	store "github.com/calypr/loom/internal/store/arango"
)

const projectConfigOwnersAQL = `FOR d IN @@c FILTER d.project == @project RETURN d`

const projectConfigExecutionAQL = `FOR d IN @@c FILTER d._key == @key RETURN d`

const projectConfigRevisionsAQL = `FOR d IN @@c FILTER d._key IN @keys RETURN d`

const insertProjectConfigRevisionAQL = `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`
const insertProjectConfigOwnerAQL = `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`

const updateProjectConfigOwnerAQL = `FOR d IN @@c
FILTER d._key == @key AND d._rev == @expectedRev
UPDATE d WITH @patch IN @@c OPTIONS { ignoreRevs: false, mergeObjects: false }
RETURN NEW`

const retireProjectConfigOwnerAQL = `FOR d IN @@c
FILTER d._key == @key AND d._rev == @expectedRev
  AND d.project == @project AND d.explorerId == @explorerId
  AND d.managementMode == @management
  AND d.projectConfig.recipeRef.name == @recipeName
  AND d.activeRevisionId == @revisionId
UPDATE d WITH { activeRevisionId: null, updatedAt: @updatedAt } IN @@c
  OPTIONS { ignoreRevs: false, mergeObjects: false }
RETURN NEW`

type projectConfigOwner struct {
	value explorer.Explorer
	doc   map[string]any
}

type projectConfigOwnerPlan struct {
	publication explorer.ProjectConfigPublication
	owner       *projectConfigOwner
	digest      string
	updatedAt   time.Time
	reactivate  bool
}

// PublishProjectConfigs stores project-owned definitions and their immutable
// execution-backed revisions, then switches the release and all config heads
// in one Arango transaction.
func (s *Store) PublishProjectConfigs(ctx context.Context, request explorer.ProjectConfigPublishRequest) (dataset.ActiveRelease, error) {
	if request.CatalogSnapshot != nil {
		normalized, err := canonicalProjectConfigRevision(*request.CatalogSnapshot)
		if err != nil {
			return dataset.ActiveRelease{}, err
		}
		request.CatalogSnapshot = &normalized
	}
	project, executionID, err := validateProjectConfigRequest(request)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	request.Configs, err = canonicalProjectConfigPublications(request.Configs)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	if s == nil || s.client == nil {
		return dataset.ActiveRelease{}, fmt.Errorf("publish project configs: Explorer Arango store is required")
	}

	var active dataset.ActiveRelease
	err = s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{publicationarango.BundleExecutionsCollection},
		Write: []string{datasetarango.LifecycleCollection, ExplorersCollection, RevisionsCollection, ViewBuilderSettingsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		execution, err := readProjectConfigExecution(txCtx, tx, executionID)
		if err != nil {
			return err
		}
		if err := validateProjectConfigExecution(request, execution); err != nil {
			return err
		}

		owners, err := readProjectConfigOwners(txCtx, tx, project)
		if err != nil {
			return err
		}
		var settings *viewBuilderSettingsRow
		if request.CatalogSnapshot != nil {
			if err := validateExpectedProjectConfigHeads(request, project, owners); err != nil {
				return err
			}
			settings, err = readViewBuilderSettings(txCtx, tx, project)
			if err != nil {
				return err
			}
		}
		plans, retired, revisionIDs, err := planProjectConfigOwners(request, project, owners)
		if err != nil {
			return err
		}
		for _, plan := range plans {
			revisionIDs = append(revisionIDs, plan.publication.Revision.ID)
		}
		if request.CatalogSnapshot != nil {
			revisionIDs = append(revisionIDs, request.CatalogSnapshot.ID)
			if settings != nil {
				revisionIDs = append(revisionIDs, settings.value.Catalogs[request.RecipeName])
			}
		}
		revisions, err := readProjectConfigRevisions(txCtx, tx, uniqueStrings(revisionIDs))
		if err != nil {
			return err
		}
		if err := validateProjectConfigRevisionGuards(plans, retired, revisions); err != nil {
			return err
		}
		if request.CatalogSnapshot != nil {
			if priorID := settingsCatalogID(settings, request.RecipeName); priorID != "" {
				if prior, ok := revisions[priorID]; !ok || prior.Project != project || prior.ExplorerID != "" || prior.Publication.ExecutionID == "" {
					return fmt.Errorf("stored catalog anchor for recipe %q is invalid", request.RecipeName)
				}
			}
			if err := validateCatalogSnapshot(request, execution); err != nil {
				return err
			}
			if existing, ok := revisions[request.CatalogSnapshot.ID]; ok && !sameProjectConfigRevision(existing, *request.CatalogSnapshot) {
				return fmt.Errorf("catalog snapshot %q conflicts with immutable content: %w", request.CatalogSnapshot.ID, explorer.ErrImmutableRevision)
			}
		}

		// All owner, prior-revision, immutable-key, and execution guards happen
		// before the first write. A lost release CAS therefore changes nothing.
		active, err = datasetarango.CompareAndSwapActivateRelease(txCtx, tx, request.Release, request.ExpectedReleaseRevision)
		if err != nil {
			return err
		}

		for _, plan := range plans {
			revision := plan.publication.Revision
			if err := insertProjectConfigRevision(txCtx, tx, revision, revisions[revision.ID]); err != nil {
				return err
			}
			if plan.reactivate {
				if err := updateActivationDocument(txCtx, tx, RevisionsCollection, revision.ID, map[string]any{"status": explorer.RevisionReady}); err != nil {
					return fmt.Errorf("prepare superseded project config revision %q for reactivation: %w", revision.ID, err)
				}
			}
			if err := writeProjectConfigOwner(txCtx, tx, project, plan); err != nil {
				return err
			}
			if existing := revisions[revision.ID]; existing.ID != "" && existing.Status == explorer.RevisionActive {
				if plan.owner != nil && plan.owner.value.ActiveRevisionID == revision.ID {
					continue // ACK-loss retry of the already-active exact revision.
				}
				return fmt.Errorf("project config revision %q is active without its owner pointer", revision.ID)
			}
			row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{
				"@explorers":  ExplorersCollection,
				"@revisions":  RevisionsCollection,
				"explorerKey": explorerKey(project, revision.ExplorerID),
				"revisionKey": revision.ID,
				"project":     project,
				"explorerId":  revision.ExplorerID,
				"management":  explorer.ManagementRepository,
			})
			if err != nil {
				return err
			}
			if !found {
				return explorer.ErrDraftConflict
			}
			state, err := activationStateFromRow(row)
			if err != nil {
				return err
			}
			if err := activateRevisionAndOwner(txCtx, tx, state, plan.updatedAt); err != nil {
				return err
			}
		}
		if request.CatalogSnapshot != nil {
			if err := insertProjectConfigRevision(txCtx, tx, *request.CatalogSnapshot, revisions[request.CatalogSnapshot.ID]); err != nil {
				return err
			}
			updatedSettings := settingsAfterCatalogPublish(settings, project, request.RecipeName, request.CatalogSnapshot.ID)
			if updatedSettings.Version != settingsVersion(settings) {
				if err := writeViewBuilderSettings(txCtx, tx, settings, updatedSettings); err != nil {
					return err
				}
			}
		} else {
			for _, owner := range retired {
				if owner.value.ActiveRevisionID == "" {
					continue
				}
				if err := supersedeProjectConfigRevision(txCtx, tx, owner.value.ActiveRevisionID); err != nil {
					return err
				}
				if err := retireProjectConfigOwner(txCtx, tx, project, request.RecipeName, owner, time.Now().UTC()); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("publish project configs for %s/%s: %w", project, request.Release.Generation, normalizeViewBuilderConflict(err))
	}
	return active, nil
}

// canonicalProjectConfigPublications makes request revisions use the exact
// JSON representation the store persists before immutable-content comparison.
// The copied slice keeps this boundary from mutating the caller's request.
func canonicalProjectConfigPublications(configs []explorer.ProjectConfigPublication) ([]explorer.ProjectConfigPublication, error) {
	canonical := append([]explorer.ProjectConfigPublication(nil), configs...)
	for index := range canonical {
		revision := canonical[index].Revision
		if revision.ID == "" {
			return nil, fmt.Errorf("project config revision %d has no ID", index)
		}
		doc, err := document(revision, revision.ID)
		if err != nil {
			return nil, fmt.Errorf("encode project config revision %q: %w", revision.ID, err)
		}
		normalized, err := decode[explorer.Revision](doc)
		if err != nil {
			return nil, fmt.Errorf("normalize project config revision %q: %w", revision.ID, err)
		}
		canonical[index].Revision = normalized
	}
	return canonical, nil
}

func canonicalProjectConfigRevision(revision explorer.Revision) (explorer.Revision, error) {
	if revision.ID == "" {
		return explorer.Revision{}, fmt.Errorf("catalog snapshot revision has no ID")
	}
	doc, err := document(revision, revision.ID)
	if err != nil {
		return explorer.Revision{}, fmt.Errorf("encode catalog snapshot revision %q: %w", revision.ID, err)
	}
	canonical, err := decode[explorer.Revision](doc)
	if err != nil {
		return explorer.Revision{}, fmt.Errorf("normalize catalog snapshot revision %q: %w", revision.ID, err)
	}
	return canonical, nil
}

func validateProjectConfigRequest(request explorer.ProjectConfigPublishRequest) (project, executionID string, err error) {
	project = strings.TrimSpace(request.Release.Project)
	if project == "" || projectid.Canonical(project) == "" || strings.TrimSpace(request.Release.Generation) == "" || strings.TrimSpace(request.Release.ID) == "" {
		return "", "", fmt.Errorf("project release identity and generation are required")
	}
	if request.ExpectedReleaseRevision < 0 {
		return "", "", fmt.Errorf("expected release revision must not be negative")
	}
	if request.RecipeName == "" || request.RecipeName != strings.TrimSpace(request.RecipeName) {
		return "", "", fmt.Errorf("project config recipe name is required")
	}
	if err := (dataset.Ref{Project: project, Generation: request.Release.Generation}).Validate(); err != nil {
		return "", "", fmt.Errorf("invalid project release identity: %w", err)
	}
	for index, binding := range request.Release.Publications {
		if err := binding.Selector.Validate(); err != nil || binding.ExecutionID == "" || binding.Generation == "" {
			return "", "", fmt.Errorf("release publication %d has an incomplete selector binding", index)
		}
	}
	for index, config := range request.Configs {
		if config.Definition.ID == "" || config.Definition.RecipeRef.Name != request.RecipeName {
			return "", "", fmt.Errorf("config %d must have an ID and reference recipe %q", index, request.RecipeName)
		}
		if config.Revision.ID == "" || config.Revision.Project != projectid.Legacy(project) || config.Revision.ExplorerID != config.Definition.ID {
			return "", "", fmt.Errorf("config %q revision project/owner identity is invalid", config.Definition.ID)
		}
		if config.Revision.Status != explorer.RevisionReady || config.Revision.SourceGeneration != request.Release.Generation || config.Revision.Publication.Generation != request.Release.Generation {
			return "", "", fmt.Errorf("config %q revision is not ready for release generation %q", config.Definition.ID, request.Release.Generation)
		}
		if config.Revision.Publication.ExecutionID == "" {
			return "", "", fmt.Errorf("config %q revision has no execution identity", config.Definition.ID)
		}
		if executionID == "" {
			executionID = config.Revision.Publication.ExecutionID
		} else if executionID != config.Revision.Publication.ExecutionID {
			return "", "", fmt.Errorf("configs for one recipe publication must share one execution")
		}
	}
	if len(request.Configs) == 0 {
		for _, binding := range request.Release.Publications {
			if binding.Selector.Recipe != request.RecipeName || binding.Generation != request.Release.Generation || binding.Stale {
				continue
			}
			if executionID == "" {
				executionID = binding.ExecutionID
			} else if executionID != binding.ExecutionID {
				return "", "", fmt.Errorf("release recipe %q outputs do not share one execution", request.RecipeName)
			}
		}
	}
	if executionID == "" {
		return "", "", fmt.Errorf("release has no current execution for recipe %q", request.RecipeName)
	}
	currentRecipeBindings := 0
	for _, binding := range request.Release.Publications {
		if binding.Selector.Recipe != request.RecipeName || binding.Generation != request.Release.Generation {
			continue // Preserve stale historical selectors for the same recipe.
		}
		if binding.Stale || binding.ExecutionID != executionID {
			return "", "", fmt.Errorf("release recipe %q contains a stale or mismatched current-generation binding", request.RecipeName)
		}
		currentRecipeBindings++
	}
	if currentRecipeBindings == 0 {
		return "", "", fmt.Errorf("release has no current output binding for recipe %q", request.RecipeName)
	}
	return projectid.Legacy(project), executionID, nil
}

func readProjectConfigExecution(ctx context.Context, tx store.RowQueryer, id string) (publicationdata.BundleExecution, error) {
	var execution *publicationdata.BundleExecution
	err := tx.QueryRows(ctx, projectConfigExecutionAQL, 1, map[string]any{
		"@c":  publicationarango.BundleExecutionsCollection,
		"key": id,
	}, func(row map[string]any) error {
		if execution != nil {
			return fmt.Errorf("execution lookup returned more than one row")
		}
		value, err := decode[publicationdata.BundleExecution](row)
		if err != nil {
			return err
		}
		value = value.CanonicalizeLegacy()
		execution = &value
		return nil
	})
	if err != nil {
		return publicationdata.BundleExecution{}, fmt.Errorf("read published project config execution: %w", err)
	}
	if execution == nil {
		return publicationdata.BundleExecution{}, fmt.Errorf("project config execution %q is missing", id)
	}
	return *execution, nil
}

func validateProjectConfigExecution(request explorer.ProjectConfigPublishRequest, execution publicationdata.BundleExecution) error {
	if execution.ID == "" || execution.ID != expectedProjectConfigExecutionID(request) || execution.State.Canonical() != publicationdata.BundlePublished {
		return fmt.Errorf("project config execution is missing, mismatched, or not published")
	}
	if projectid.Canonical(execution.Project) != projectid.Canonical(request.Release.Project) || execution.DatasetGeneration != request.Release.Generation || execution.Name != request.RecipeName {
		return fmt.Errorf("project config execution project, generation, or recipe does not match release")
	}
	if execution.RecipeDigest == "" || execution.SchemaDigest == "" || execution.TranslationVersion == "" {
		return fmt.Errorf("project config execution identity is incomplete")
	}
	for _, config := range request.Configs {
		revision := config.Revision
		// Recipe is the resolved snapshot and ResolvedSchemaDigest describes
		// compiler resolution. Compare the stored recipe digest and retained
		// final schema identity to the execution instead.
		if revision.RecipeDigest != execution.RecipeDigest || revision.Dataset.SchemaDigest != execution.SchemaDigest ||
			revision.Recipe.Name != execution.Name || revision.Recipe.TranslationVersion != execution.TranslationVersion {
			return fmt.Errorf("project config %q revision is not pinned to the published recipe", config.Definition.ID)
		}
		if revision.Publication.ExecutionID != execution.ID {
			return fmt.Errorf("project config %q revision references a different execution", config.Definition.ID)
		}
	}
	if request.CatalogSnapshot != nil {
		if err := validateCatalogSnapshot(request, execution); err != nil {
			return err
		}
	}
	for _, binding := range request.Release.Publications {
		if binding.Selector.Recipe == request.RecipeName && binding.Generation == execution.DatasetGeneration &&
			(binding.ExecutionID != execution.ID || binding.Stale) {
			return fmt.Errorf("release recipe binding does not match the published execution")
		}
	}
	return nil
}

func validateCatalogSnapshot(request explorer.ProjectConfigPublishRequest, execution publicationdata.BundleExecution) error {
	snapshot := request.CatalogSnapshot
	if snapshot == nil {
		return nil
	}
	if snapshot.ID == "" || !strings.HasPrefix(snapshot.ID, "catalog_") || snapshot.Project != projectid.Legacy(request.Release.Project) || snapshot.ExplorerID != "" {
		return fmt.Errorf("catalog snapshot identity is invalid")
	}
	if snapshot.Status != explorer.RevisionReady || snapshot.SourceGeneration != execution.DatasetGeneration ||
		snapshot.Publication.ExecutionID != execution.ID || snapshot.Publication.Generation != execution.DatasetGeneration ||
		snapshot.Recipe.Name != execution.Name || snapshot.RecipeDigest != execution.RecipeDigest ||
		snapshot.Dataset.SchemaDigest != execution.SchemaDigest || snapshot.Recipe.TranslationVersion != execution.TranslationVersion {
		return fmt.Errorf("catalog snapshot does not match the published recipe execution")
	}
	if len(snapshot.Config) != 0 || len(snapshot.AuthoringBundle) != 0 || snapshot.IntentDigest != "" ||
		len(snapshot.PublicOutputContract) == 0 || len(snapshot.EmittedColumns) == 0 || len(snapshot.Materializations) == 0 || len(snapshot.Dataset.Outputs) == 0 {
		return fmt.Errorf("catalog snapshot is incomplete or contains view-owned content")
	}
	return nil
}

func validateExpectedProjectConfigHeads(request explorer.ProjectConfigPublishRequest, project string, owners map[string]projectConfigOwner) error {
	if request.ExpectedConfigRevisionIDs == nil {
		return fmt.Errorf("source publication is missing expected project config heads: %w", explorer.ErrDraftConflict)
	}
	candidateRevisionIDs := make(map[string]string, len(request.Configs))
	for _, publication := range request.Configs {
		candidateRevisionIDs[publication.Definition.ID] = publication.Revision.ID
	}
	managedIDs := make(map[string]struct{})
	for id, owner := range owners {
		if owner.value.ProjectConfig == nil || owner.value.ProjectConfig.RecipeRef.Name != request.RecipeName {
			continue
		}
		expected, exists := request.ExpectedConfigRevisionIDs[id]
		if !exists || (expected != owner.value.ActiveRevisionID && candidateRevisionIDs[id] != owner.value.ActiveRevisionID) {
			return fmt.Errorf("project config %q changed during source publication: %w", id, explorer.ErrDraftConflict)
		}
		if err := validateProjectConfigOwner(owner.value, project, id, request.RecipeName); err != nil {
			return err
		}
		managedIDs[id] = struct{}{}
	}
	configIDs := make(map[string]struct{}, len(request.Configs))
	for _, publication := range request.Configs {
		id := publication.Definition.ID
		if _, duplicate := configIDs[id]; duplicate {
			return fmt.Errorf("duplicate project config %q in source publication", id)
		}
		configIDs[id] = struct{}{}
		if _, exists := request.ExpectedConfigRevisionIDs[id]; !exists {
			return fmt.Errorf("project config %q has no expected revision head: %w", id, explorer.ErrDraftConflict)
		}
		if owner, exists := owners[id]; exists {
			expected := request.ExpectedConfigRevisionIDs[id]
			if owner.value.ProjectConfig == nil || owner.value.ProjectConfig.RecipeRef.Name != request.RecipeName ||
				(expected != owner.value.ActiveRevisionID && publication.Revision.ID != owner.value.ActiveRevisionID) {
				return fmt.Errorf("project config %q owner changed during source publication: %w", id, explorer.ErrDraftConflict)
			}
		} else if request.ExpectedConfigRevisionIDs[id] != "" {
			return fmt.Errorf("project config %q disappeared during source publication: %w", id, explorer.ErrDraftConflict)
		}
	}
	if len(configIDs) != len(request.ExpectedConfigRevisionIDs) {
		return fmt.Errorf("source publication project config set changed: %w", explorer.ErrDraftConflict)
	}
	for id := range managedIDs {
		if _, found := configIDs[id]; !found {
			return fmt.Errorf("project config %q was omitted from source publication: %w", id, explorer.ErrDraftConflict)
		}
	}
	for id := range request.ExpectedConfigRevisionIDs {
		if _, found := configIDs[id]; !found {
			return fmt.Errorf("expected project config %q was omitted from source publication: %w", id, explorer.ErrDraftConflict)
		}
	}
	return nil
}

func settingsCatalogID(settings *viewBuilderSettingsRow, recipeName string) string {
	if settings == nil {
		return ""
	}
	return settings.value.Catalogs[recipeName]
}

func settingsVersion(settings *viewBuilderSettingsRow) int64 {
	if settings == nil {
		return 0
	}
	return settings.value.Version
}

func settingsAfterCatalogPublish(current *viewBuilderSettingsRow, project, recipeName, revisionID string) explorer.ViewBuilderSettings {
	settings := emptyViewBuilderSettings(project)
	if current != nil {
		settings = cloneViewBuilderSettings(current.value)
	}
	if settings.Catalogs == nil {
		settings.Catalogs = map[string]string{}
	}
	if settings.Catalogs[recipeName] != revisionID {
		settings.Catalogs[recipeName] = revisionID
		settings.Version++
		if settings.Version == 0 {
			settings.Version = 1
		}
	}
	return settings
}

func expectedProjectConfigExecutionID(request explorer.ProjectConfigPublishRequest) string {
	if request.CatalogSnapshot != nil {
		return request.CatalogSnapshot.Publication.ExecutionID
	}
	if len(request.Configs) > 0 {
		return request.Configs[0].Revision.Publication.ExecutionID
	}
	for _, binding := range request.Release.Publications {
		if binding.Selector.Recipe == request.RecipeName && binding.Generation == request.Release.Generation && !binding.Stale {
			return binding.ExecutionID
		}
	}
	return ""
}

func readProjectConfigOwners(ctx context.Context, tx store.RowQueryer, project string) (map[string]projectConfigOwner, error) {
	owners := make(map[string]projectConfigOwner)
	err := tx.QueryRows(ctx, projectConfigOwnersAQL, 256, map[string]any{
		"@c": ExplorersCollection, "project": project,
	}, func(row map[string]any) error {
		owner, err := decode[explorer.Explorer](row)
		if err != nil {
			return err
		}
		if owner.Project != project || owner.ExplorerID == "" || explorerKey(project, owner.ExplorerID) != stringValue(row["_key"]) {
			return fmt.Errorf("stored Explorer owner identity is invalid")
		}
		if _, exists := owners[owner.ExplorerID]; exists {
			return fmt.Errorf("duplicate Explorer owner ID %q", owner.ExplorerID)
		}
		owners[owner.ExplorerID] = projectConfigOwner{value: owner, doc: row}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read project config owners: %w", err)
	}
	return owners, nil
}

func planProjectConfigOwners(request explorer.ProjectConfigPublishRequest, project string, owners map[string]projectConfigOwner) ([]projectConfigOwnerPlan, []projectConfigOwner, []string, error) {
	seen := make(map[string]struct{}, len(request.Configs))
	plans := make([]projectConfigOwnerPlan, 0, len(request.Configs))
	revisionIDs := make([]string, 0, len(request.Configs))
	for _, publication := range request.Configs {
		definition := publication.Definition
		digest, err := definition.Digest()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("project config %q digest: %w", definition.ID, err)
		}
		if _, exists := seen[definition.ID]; exists {
			return nil, nil, nil, fmt.Errorf("duplicate project config ID %q", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		var existing *projectConfigOwner
		if owner, ok := owners[definition.ID]; ok {
			if err := validateProjectConfigOwner(owner.value, project, definition.ID, request.RecipeName); err != nil {
				return nil, nil, nil, err
			}
			existing = &owner
			if owner.value.ActiveRevisionID != "" {
				revisionIDs = append(revisionIDs, owner.value.ActiveRevisionID)
			}
		}
		updatedAt := publication.Revision.CreatedAt
		if updatedAt.IsZero() && publication.Revision.ReadyAt != nil {
			updatedAt = *publication.Revision.ReadyAt
		}
		if updatedAt.IsZero() {
			updatedAt = publication.Revision.Publication.UpdatedAt
		}
		if updatedAt.IsZero() {
			return nil, nil, nil, fmt.Errorf("project config %q has no stable revision timestamp", definition.ID)
		}
		plans = append(plans, projectConfigOwnerPlan{publication: publication, owner: existing, digest: digest, updatedAt: updatedAt.UTC()})
	}

	retired := make([]projectConfigOwner, 0)
	for id, owner := range owners {
		if owner.value.ProjectConfig == nil || owner.value.ProjectConfig.RecipeRef.Name != request.RecipeName {
			continue
		}
		if _, present := seen[id]; present {
			continue
		}
		if err := validateProjectConfigOwner(owner.value, project, id, request.RecipeName); err != nil {
			return nil, nil, nil, err
		}
		retired = append(retired, owner)
		if owner.value.ActiveRevisionID != "" {
			revisionIDs = append(revisionIDs, owner.value.ActiveRevisionID)
		}
	}
	return plans, retired, revisionIDs, nil
}

func validateProjectConfigOwner(owner explorer.Explorer, project, id, recipeName string) error {
	if owner.Project != project || owner.ExplorerID != id || owner.ManagementMode != explorer.ManagementRepository || owner.ProjectConfig == nil {
		return fmt.Errorf("project config owner %q collides with an unmanaged Explorer: %w", id, explorer.ErrDraftConflict)
	}
	if owner.ProjectConfig.ID != id || owner.ProjectConfig.RecipeRef.Name != recipeName {
		return fmt.Errorf("project config owner %q belongs to another recipe namespace: %w", id, explorer.ErrDraftConflict)
	}
	if len(owner.DraftConfig) != 0 && strings.TrimSpace(string(owner.DraftConfig)) != "null" || owner.LastAuthoringCommandID != "" || owner.PreviousDraftRevisionID != "" {
		return fmt.Errorf("project config owner %q contains Builder state: %w", id, explorer.ErrDraftConflict)
	}
	return nil
}

func readProjectConfigRevisions(ctx context.Context, tx store.RowQueryer, ids []string) (map[string]explorer.Revision, error) {
	revisions := make(map[string]explorer.Revision, len(ids))
	if len(ids) == 0 {
		return revisions, nil
	}
	err := tx.QueryRows(ctx, projectConfigRevisionsAQL, 256, map[string]any{
		"@c": RevisionsCollection, "keys": ids,
	}, func(row map[string]any) error {
		revision, err := decode[explorer.Revision](row)
		if err != nil {
			return err
		}
		if _, exists := revisions[revision.ID]; exists {
			return fmt.Errorf("duplicate project config revision %q", revision.ID)
		}
		revisions[revision.ID] = revision
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read project config revisions: %w", err)
	}
	return revisions, nil
}

func validateProjectConfigRevisionGuards(plans []projectConfigOwnerPlan, retired []projectConfigOwner, revisions map[string]explorer.Revision) error {
	for index := range plans {
		plan := &plans[index]
		candidate := plan.publication.Revision
		if existing, ok := revisions[candidate.ID]; ok {
			if !sameProjectConfigRevision(existing, candidate) {
				return fmt.Errorf("project config revision %q conflicts with immutable content: %w", candidate.ID, explorer.ErrImmutableRevision)
			}
			if existing.Status == explorer.RevisionActive {
				if plan.owner == nil || plan.owner.value.ActiveRevisionID != candidate.ID {
					return fmt.Errorf("project config revision %q is active without its owner pointer", candidate.ID)
				}
			} else if existing.Status == explorer.RevisionSuperseded {
				plan.reactivate = true
			} else if existing.Status != explorer.RevisionReady {
				return fmt.Errorf("project config revision %q is not ready for activation: %s", candidate.ID, existing.Status)
			}
		}
		if plan.owner == nil || plan.owner.value.ActiveRevisionID == "" {
			continue
		}
		prior, ok := revisions[plan.owner.value.ActiveRevisionID]
		if !ok || prior.Project != plan.owner.value.Project || prior.ExplorerID != plan.owner.value.ExplorerID || prior.Status != explorer.RevisionActive {
			return fmt.Errorf("project config owner %q has an invalid active revision", plan.owner.value.ExplorerID)
		}
	}
	for _, owner := range retired {
		if owner.value.ActiveRevisionID == "" {
			continue
		}
		prior, ok := revisions[owner.value.ActiveRevisionID]
		if !ok || prior.Project != owner.value.Project || prior.ExplorerID != owner.value.ExplorerID || prior.Status != explorer.RevisionActive {
			return fmt.Errorf("project config owner %q has an invalid active revision", owner.value.ExplorerID)
		}
	}
	return nil
}

func sameProjectConfigRevision(existing, candidate explorer.Revision) bool {
	return sameRevisionContent(existing, candidate) &&
		existing.Publication.Generation == candidate.Publication.Generation &&
		existing.Publication.ExecutionID == candidate.Publication.ExecutionID
}

func insertProjectConfigRevision(ctx context.Context, tx store.RowQueryer, revision, existing explorer.Revision) error {
	if existing.ID != "" {
		if !sameProjectConfigRevision(existing, revision) {
			return fmt.Errorf("project config revision %q conflicts with immutable content: %w", revision.ID, explorer.ErrImmutableRevision)
		}
		return nil
	}
	doc, err := document(revision, revision.ID)
	if err != nil {
		return err
	}
	if err := tx.QueryRows(ctx, insertProjectConfigRevisionAQL, 1, map[string]any{
		"@c": RevisionsCollection, "doc": doc,
	}, func(map[string]any) error { return nil }); err != nil {
		return fmt.Errorf("insert project config revision %q: %w", revision.ID, err)
	}
	stored, err := readProjectConfigRevisions(ctx, tx, []string{revision.ID})
	if err != nil {
		return err
	}
	actual, ok := stored[revision.ID]
	if !ok || !sameProjectConfigRevision(actual, revision) {
		return fmt.Errorf("project config revision %q was not stored with matching immutable content: %w", revision.ID, explorer.ErrImmutableRevision)
	}
	return nil
}

func writeProjectConfigOwner(ctx context.Context, tx store.RowQueryer, project string, plan projectConfigOwnerPlan) error {
	definition := plan.publication.Definition
	owner := explorer.Explorer{
		Project: project, ExplorerID: definition.ID, Title: definition.Title,
		ManagementMode: explorer.ManagementRepository, ProjectConfig: &definition,
		DraftVersion: 1, DraftDigest: plan.digest,
		UpdatedBy: plan.publication.Revision.CreatedBy, UpdatedAt: plan.updatedAt,
	}
	if plan.owner == nil {
		doc, err := document(owner, explorerKey(project, owner.ExplorerID))
		if err != nil {
			return err
		}
		if err := tx.QueryRows(ctx, insertProjectConfigOwnerAQL, 1, map[string]any{
			"@c": ExplorersCollection, "doc": doc,
		}, func(map[string]any) error { return nil }); err != nil {
			return fmt.Errorf("insert project config owner %q: %w", owner.ExplorerID, err)
		}
		return nil
	}
	previous := plan.owner.value
	if previous.Title == owner.Title && previous.DraftVersion == owner.DraftVersion && previous.DraftDigest == owner.DraftDigest &&
		previous.UpdatedBy == owner.UpdatedBy && previous.UpdatedAt.Equal(owner.UpdatedAt) && reflect.DeepEqual(previous.ProjectConfig, owner.ProjectConfig) {
		return nil
	}
	expectedRev := stringValue(plan.owner.doc["_rev"])
	if expectedRev == "" {
		return fmt.Errorf("project config owner %q has no Arango revision guard", owner.ExplorerID)
	}
	patch := map[string]any{
		"title": owner.Title, "projectConfig": owner.ProjectConfig,
		"managementMode": owner.ManagementMode, "draftConfig": nil,
		"draftVersion": owner.DraftVersion, "draftDigest": owner.DraftDigest,
		"draftSnapshotToken": nil, "draftSourceGeneration": nil,
		"draftAuthorizationScopeDigest": nil, "previousDraftRevisionId": nil,
		"lastAuthoringCommandId": nil, "lastAuthoringCommandDigest": nil,
		"lastAuthoringCommandResults": nil, "updatedBy": owner.UpdatedBy,
		"updatedAt": owner.UpdatedAt,
	}
	var updated bool
	err := tx.QueryRows(ctx, updateProjectConfigOwnerAQL, 1, map[string]any{
		"@c": ExplorersCollection, "key": explorerKey(project, owner.ExplorerID),
		"expectedRev": expectedRev, "patch": patch,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil {
		return fmt.Errorf("update project config owner %q: %w", owner.ExplorerID, err)
	}
	if !updated {
		return fmt.Errorf("project config owner %q changed during publish: %w", owner.ExplorerID, explorer.ErrDraftConflict)
	}
	return nil
}

func retireProjectConfigOwner(ctx context.Context, tx store.RowQueryer, project, recipeName string, owner projectConfigOwner, at time.Time) error {
	var updated bool
	err := tx.QueryRows(ctx, retireProjectConfigOwnerAQL, 1, map[string]any{
		"@c": ExplorersCollection, "key": explorerKey(project, owner.value.ExplorerID),
		"expectedRev": stringValue(owner.doc["_rev"]), "project": project,
		"explorerId": owner.value.ExplorerID, "management": explorer.ManagementRepository,
		"recipeName": recipeName, "revisionId": owner.value.ActiveRevisionID,
		"updatedAt": at,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil {
		return fmt.Errorf("retire project config owner %q: %w", owner.value.ExplorerID, err)
	}
	if !updated {
		return fmt.Errorf("project config owner %q changed during retirement: %w", owner.value.ExplorerID, explorer.ErrDraftConflict)
	}
	return nil
}

func supersedeProjectConfigRevision(ctx context.Context, tx store.RowQueryer, id string) error {
	return updateActivationDocument(ctx, tx, RevisionsCollection, id, map[string]any{"status": explorer.RevisionSuperseded})
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

var _ explorer.ProjectConfigPublisher = (*Store)(nil)

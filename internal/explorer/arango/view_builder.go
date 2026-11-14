package arango

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	viewBuilderSettingsReadAQL   = `FOR d IN @@c FILTER d._key == @key RETURN d`
	viewBuilderSettingsInsertAQL = `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`
	viewBuilderSettingsCASAQL    = `FOR d IN @@c
FILTER d._key == @key AND d._rev == @expectedRev
UPDATE d WITH @patch IN @@c OPTIONS { ignoreRevs: false, mergeObjects: false }
RETURN NEW`
	deleteViewBuilderOwnerAQL = `FOR d IN @@c
FILTER d._key == @key AND d._rev == @expectedRev
  AND d.project == @project AND d.explorerId == @explorerId
  AND d.managementMode == @management AND d.activeRevisionId == @revisionId
  AND d.projectConfig != null
REMOVE d IN @@c RETURN OLD`
)

type viewBuilderSettingsRow struct {
	value explorer.ViewBuilderSettings
	doc   map[string]any
}

func viewBuilderSettingsKey(project string) string {
	return "view_builder_" + key(project)
}

func emptyViewBuilderSettings(project string) explorer.ViewBuilderSettings {
	return explorer.ViewBuilderSettings{Project: project, Catalogs: map[string]string{}}
}

func (s *Store) GetViewBuilderSettings(ctx context.Context, project string) (explorer.ViewBuilderSettings, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return explorer.ViewBuilderSettings{}, fmt.Errorf("view builder project is required")
	}
	project = canonicalProjectConfigProject(project)
	row, err := readViewBuilderSettings(ctx, s.client, project)
	if err != nil {
		return explorer.ViewBuilderSettings{}, err
	}
	if row == nil {
		return emptyViewBuilderSettings(project), nil
	}
	return cloneViewBuilderSettings(row.value), nil
}

func normalizeViewBuilderConflict(err error) error {
	if err == nil || errors.Is(err, explorer.ErrDraftConflict) || !shared.IsConflict(err) {
		return err
	}
	return fmt.Errorf("view builder state changed concurrently: %w: %v", explorer.ErrDraftConflict, err)
}

func readViewBuilderSettings(ctx context.Context, tx store.RowQueryer, project string) (*viewBuilderSettingsRow, error) {
	var found *viewBuilderSettingsRow
	err := tx.QueryRows(ctx, viewBuilderSettingsReadAQL, 1, map[string]any{
		"@c": ViewBuilderSettingsCollection, "key": viewBuilderSettingsKey(project),
	}, func(doc map[string]any) error {
		if found != nil {
			return fmt.Errorf("duplicate view builder settings for project %q", project)
		}
		value, err := decode[explorer.ViewBuilderSettings](doc)
		if err != nil {
			return err
		}
		if value.Project != project || stringValue(doc["_key"]) != viewBuilderSettingsKey(project) || value.Version < 1 {
			return fmt.Errorf("stored view builder settings identity is invalid")
		}
		if value.Catalogs == nil {
			value.Catalogs = map[string]string{}
		}
		found = &viewBuilderSettingsRow{value: value, doc: doc}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read view builder settings for %q: %w", project, err)
	}
	return found, nil
}

func cloneViewBuilderSettings(value explorer.ViewBuilderSettings) explorer.ViewBuilderSettings {
	clone := value
	clone.Catalogs = make(map[string]string, len(value.Catalogs))
	for recipeName, revisionID := range value.Catalogs {
		clone.Catalogs[recipeName] = revisionID
	}
	return clone
}

func writeViewBuilderSettings(ctx context.Context, tx store.RowQueryer, current *viewBuilderSettingsRow, value explorer.ViewBuilderSettings) error {
	if value.Project == "" || value.Version < 1 {
		return fmt.Errorf("view builder settings identity is incomplete")
	}
	if value.Catalogs == nil {
		value.Catalogs = map[string]string{}
	}
	key := viewBuilderSettingsKey(value.Project)
	doc, err := document(value, key)
	if err != nil {
		return fmt.Errorf("encode view builder settings: %w", err)
	}
	doc["updatedAt"] = time.Now().UTC()
	if current == nil {
		var inserted bool
		err := tx.QueryRows(ctx, viewBuilderSettingsInsertAQL, 1, map[string]any{
			"@c": ViewBuilderSettingsCollection, "doc": doc,
		}, func(map[string]any) error { inserted = true; return nil })
		if err != nil {
			return fmt.Errorf("insert view builder settings: %w", err)
		}
		if !inserted {
			return explorer.ErrDraftConflict
		}
		return nil
	}
	expectedRev := stringValue(current.doc["_rev"])
	if expectedRev == "" {
		return fmt.Errorf("view builder settings have no Arango revision guard")
	}
	patch := map[string]any{
		"project": value.Project, "version": value.Version,
		"defaultConfigId": value.DefaultConfigID, "defaultFrameId": value.DefaultFrameID,
		"catalogs": value.Catalogs, "updatedAt": time.Now().UTC(),
	}
	var updated bool
	err = tx.QueryRows(ctx, viewBuilderSettingsCASAQL, 1, map[string]any{
		"@c": ViewBuilderSettingsCollection, "key": key, "expectedRev": expectedRev, "patch": patch,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil {
		return fmt.Errorf("update view builder settings: %w", err)
	}
	if !updated {
		return explorer.ErrDraftConflict
	}
	return nil
}

func canonicalProjectConfigProject(project string) string {
	// Project-config Explorer records use this normalization already; keeping
	// settings on the same canonical key lets both aggregates share one scope.
	return projectid.Legacy(project)
}

func (s *Store) SaveViewBuilderConfig(ctx context.Context, mutation explorer.ViewBuilderMutation) error {
	project := canonicalProjectConfigProject(strings.TrimSpace(mutation.Project))
	publication := mutation.Publication
	if project == "" || publication.Definition.ID == "" || publication.Revision.ID == "" {
		return fmt.Errorf("view builder config identity is required")
	}
	if publication.Definition.RecipeRef.Name == "" || publication.Revision.ExplorerID != publication.Definition.ID || publication.Revision.Project != project {
		return fmt.Errorf("view builder config revision identity is invalid")
	}
	digest, err := publication.Definition.Digest()
	if err != nil {
		return err
	}
	if publication.Revision.IntentDigest != digest || publication.Revision.Status != explorer.RevisionReady {
		return fmt.Errorf("view builder revision does not match its prepared definition")
	}
	canonical, err := canonicalProjectConfigPublications([]explorer.ProjectConfigPublication{publication})
	if err != nil {
		return err
	}
	publication = canonical[0]
	err = s.client.WithTransaction(ctx, store.TransactionCollections{
		Write: []string{ViewBuilderSettingsCollection, ExplorersCollection, RevisionsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		settings, err := readViewBuilderSettings(txCtx, tx, project)
		if err != nil {
			return err
		}
		if settings == nil || settings.value.Catalogs[publication.Definition.RecipeRef.Name] != mutation.CatalogRevisionID || mutation.CatalogRevisionID == "" {
			return explorer.ErrDraftConflict
		}
		catalogRevisions, err := readProjectConfigRevisions(txCtx, tx, []string{mutation.CatalogRevisionID})
		if err != nil {
			return err
		}
		catalog, ok := catalogRevisions[mutation.CatalogRevisionID]
		if !ok || catalog.ExplorerID != "" || catalog.Status != explorer.RevisionReady || !sameProjectRecipeSnapshot(catalog, publication.Revision) {
			return fmt.Errorf("view builder config is not pinned to the active catalog snapshot: %w", explorer.ErrDraftConflict)
		}
		owner, err := readProjectConfigOwner(txCtx, tx, project, publication.Definition.ID)
		if err != nil {
			return err
		}
		if err := validateViewBuilderOwnerHead(owner, project, publication.Definition, mutation.ExpectedRevisionID); err != nil {
			return err
		}
		if err := validateViewBuilderDefaultBinding(settings.value, publication.Definition); err != nil {
			return err
		}

		revisions, err := readProjectConfigRevisions(txCtx, tx, []string{publication.Revision.ID, mutation.ExpectedRevisionID})
		if err != nil {
			return err
		}
		reactivate := false
		noOp := false
		if existing, found := revisions[publication.Revision.ID]; found {
			if !sameProjectConfigRevision(existing, publication.Revision) {
				return fmt.Errorf("view builder revision %q conflicts with immutable content: %w", publication.Revision.ID, explorer.ErrImmutableRevision)
			}
			switch existing.Status {
			case explorer.RevisionReady:
			case explorer.RevisionSuperseded:
				reactivate = true
			case explorer.RevisionActive:
				if owner == nil || owner.value.ActiveRevisionID != publication.Revision.ID || mutation.ExpectedRevisionID != publication.Revision.ID {
					return explorer.ErrDraftConflict
				}
				currentDigest, err := owner.value.ProjectConfig.Digest()
				if err != nil || currentDigest != digest {
					return explorer.ErrDraftConflict
				}
				noOp = true
			default:
				return explorer.ErrDraftConflict
			}
		}
		if priorID := mutation.ExpectedRevisionID; priorID != "" {
			prior, found := revisions[priorID]
			if !found || prior.Status != explorer.RevisionActive || prior.ExplorerID != publication.Definition.ID || prior.Project != project {
				return explorer.ErrDraftConflict
			}
		}
		if noOp {
			return nil
		}

		if err := insertProjectConfigRevision(txCtx, tx, publication.Revision, revisions[publication.Revision.ID]); err != nil {
			return err
		}
		if reactivate {
			if err := updateActivationDocument(txCtx, tx, RevisionsCollection, publication.Revision.ID, map[string]any{"status": explorer.RevisionReady}); err != nil {
				return fmt.Errorf("prepare superseded view revision %q for reactivation: %w", publication.Revision.ID, err)
			}
		}
		if err := writeViewBuilderConfigOwner(txCtx, tx, project, publication, owner); err != nil {
			return err
		}
		row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{
			"@explorers": ExplorersCollection, "@revisions": RevisionsCollection,
			"explorerKey": explorerKey(project, publication.Definition.ID), "revisionKey": publication.Revision.ID,
			"project": project, "explorerId": publication.Definition.ID, "management": explorer.ManagementRepository,
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
		if err := activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC()); err != nil {
			return err
		}
		// Serialize with default changes without changing the settings version:
		// that version tracks only the catalog and default pointers.
		return writeViewBuilderSettings(txCtx, tx, settings, cloneViewBuilderSettings(settings.value))
	})
	return normalizeViewBuilderConflict(err)
}

func validateViewBuilderDefaultBinding(settings explorer.ViewBuilderSettings, definition explorer.ProjectConfig) error {
	if settings.DefaultConfigID != definition.ID {
		return nil
	}
	recipeName, output, ok := parseViewBuilderFrameID(settings.DefaultFrameID)
	if !ok || recipeName != definition.RecipeRef.Name || !projectConfigHasOutput(definition, output) {
		return fmt.Errorf("save the new default before removing or retargeting its selected frame: %w", explorer.ErrDraftConflict)
	}
	return nil
}

func readProjectConfigOwner(ctx context.Context, tx store.RowQueryer, project, id string) (*projectConfigOwner, error) {
	var owner *projectConfigOwner
	err := tx.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, 1, map[string]any{
		"@c": ExplorersCollection, "key": explorerKey(project, id), "project": project,
	}, func(row map[string]any) error {
		value, err := decode[explorer.Explorer](row)
		if err != nil {
			return err
		}
		owner = &projectConfigOwner{value: value, doc: row}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return owner, nil
}

func validateViewBuilderOwnerHead(owner *projectConfigOwner, project string, definition explorer.ProjectConfig, expectedRevisionID string) error {
	if owner == nil {
		if expectedRevisionID != "" {
			return explorer.ErrDraftConflict
		}
		return nil
	}
	if err := validateProjectConfigOwner(owner.value, project, definition.ID, definition.RecipeRef.Name); err != nil {
		return err
	}
	if owner.value.ActiveRevisionID != expectedRevisionID || expectedRevisionID == "" {
		return explorer.ErrDraftConflict
	}
	return nil
}

func writeViewBuilderConfigOwner(ctx context.Context, tx store.RowQueryer, project string, publication explorer.ProjectConfigPublication, previous *projectConfigOwner) error {
	definition := publication.Definition
	digest, err := definition.Digest()
	if err != nil {
		return err
	}
	revision := publication.Revision
	updatedAt := revision.CreatedAt
	if revision.ReadyAt != nil && revision.ReadyAt.After(updatedAt) {
		updatedAt = *revision.ReadyAt
	}
	if updatedAt.IsZero() {
		updatedAt = revision.Publication.UpdatedAt
	}
	if updatedAt.IsZero() {
		return fmt.Errorf("view builder revision %q has no stable timestamp", revision.ID)
	}
	owner := explorer.Explorer{
		Project: project, ExplorerID: definition.ID, Title: definition.Title,
		ManagementMode: explorer.ManagementRepository, ProjectConfig: &definition,
		DraftVersion: 1, DraftDigest: digest, UpdatedBy: revision.CreatedBy, UpdatedAt: updatedAt.UTC(),
	}
	if previous == nil {
		doc, err := document(owner, explorerKey(project, owner.ExplorerID))
		if err != nil {
			return err
		}
		var inserted bool
		if err := tx.QueryRows(ctx, insertProjectConfigOwnerAQL, 1, map[string]any{"@c": ExplorersCollection, "doc": doc}, func(map[string]any) error { inserted = true; return nil }); err != nil {
			return err
		}
		if !inserted {
			return explorer.ErrDraftConflict
		}
		return nil
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
	err = tx.QueryRows(ctx, updateProjectConfigOwnerAQL, 1, map[string]any{
		"@c": ExplorersCollection, "key": explorerKey(project, owner.ExplorerID),
		"expectedRev": stringValue(previous.doc["_rev"]), "patch": patch,
	}, func(map[string]any) error { updated = true; return nil })
	if err != nil {
		return err
	}
	if !updated {
		return explorer.ErrDraftConflict
	}
	return nil
}

func sameProjectRecipeSnapshot(snapshot, viewRevision explorer.Revision) bool {
	return snapshot.ID != "" && snapshot.Project == viewRevision.Project && snapshot.SourceGeneration == viewRevision.SourceGeneration &&
		snapshot.Recipe.Name == viewRevision.Recipe.Name && snapshot.Recipe.TranslationVersion == viewRevision.Recipe.TranslationVersion &&
		snapshot.Publication.Generation == viewRevision.Publication.Generation && snapshot.Publication.ExecutionID == viewRevision.Publication.ExecutionID &&
		snapshot.Dataset.SchemaDigest == viewRevision.Dataset.SchemaDigest && snapshot.RecipeDigest == viewRevision.RecipeDigest
}

func (s *Store) DeleteViewBuilderConfig(ctx context.Context, project, id, expectedRevisionID string) error {
	project = canonicalProjectConfigProject(strings.TrimSpace(project))
	if project == "" || id == "" || expectedRevisionID == "" {
		return explorer.ErrDraftConflict
	}
	err := s.client.WithTransaction(ctx, store.TransactionCollections{
		Write: []string{ViewBuilderSettingsCollection, ExplorersCollection, RevisionsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		settings, err := readViewBuilderSettings(txCtx, tx, project)
		if err != nil {
			return err
		}
		if settings != nil && settings.value.DefaultConfigID == id {
			return fmt.Errorf("cannot delete the default view builder config: %w", explorer.ErrDraftConflict)
		}
		owner, err := readProjectConfigOwner(txCtx, tx, project, id)
		if err != nil {
			return err
		}
		if owner == nil || owner.value.ManagementMode != explorer.ManagementRepository || owner.value.ProjectConfig == nil || owner.value.ActiveRevisionID != expectedRevisionID {
			return explorer.ErrDraftConflict
		}
		if err := supersedeProjectConfigRevision(txCtx, tx, expectedRevisionID); err != nil {
			return err
		}
		var removed bool
		err = tx.QueryRows(txCtx, deleteViewBuilderOwnerAQL, 1, map[string]any{
			"@c": ExplorersCollection, "key": explorerKey(project, id), "expectedRev": stringValue(owner.doc["_rev"]),
			"project": project, "explorerId": id, "management": explorer.ManagementRepository,
			"revisionId": expectedRevisionID,
		}, func(map[string]any) error { removed = true; return nil })
		if err != nil {
			return err
		}
		if !removed {
			return explorer.ErrDraftConflict
		}
		updatedSettings := emptyViewBuilderSettings(project)
		if settings != nil {
			updatedSettings = cloneViewBuilderSettings(settings.value)
		} else {
			updatedSettings.Version = 1
		}
		return writeViewBuilderSettings(txCtx, tx, settings, updatedSettings)
	})
	return normalizeViewBuilderConflict(err)
}

func (s *Store) SaveViewBuilderDefaults(ctx context.Context, project string, request explorer.ViewBuilderDefaultsRequest) error {
	project = canonicalProjectConfigProject(strings.TrimSpace(project))
	if project == "" || request.DefaultConfigID == "" || request.DefaultFrameID == "" {
		return fmt.Errorf("view builder default config and frame are required")
	}
	err := s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{ExplorersCollection},
		Write: []string{ViewBuilderSettingsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		settings, err := readViewBuilderSettings(txCtx, tx, project)
		if err != nil {
			return err
		}
		if settings == nil || settings.value.Version != request.ExpectedVersion {
			return explorer.ErrDraftConflict
		}
		owner, err := readProjectConfigOwner(txCtx, tx, project, request.DefaultConfigID)
		if err != nil {
			return err
		}
		if owner == nil || owner.value.ManagementMode != explorer.ManagementRepository || owner.value.ProjectConfig == nil || owner.value.ActiveRevisionID == "" {
			return explorer.ErrDraftConflict
		}
		recipeName := owner.value.ProjectConfig.RecipeRef.Name
		if settings.value.Catalogs[recipeName] == "" {
			return explorer.ErrDraftConflict
		}
		frameRecipe, output, ok := parseViewBuilderFrameID(request.DefaultFrameID)
		if !ok || frameRecipe != recipeName || !projectConfigHasOutput(*owner.value.ProjectConfig, output) {
			return fmt.Errorf("default frame must be an output in the selected saved config")
		}
		if settings.value.DefaultConfigID == request.DefaultConfigID && settings.value.DefaultFrameID == request.DefaultFrameID {
			return nil
		}
		updated := cloneViewBuilderSettings(settings.value)
		updated.Version++
		updated.DefaultConfigID = request.DefaultConfigID
		updated.DefaultFrameID = request.DefaultFrameID
		return writeViewBuilderSettings(txCtx, tx, settings, updated)
	})
	return normalizeViewBuilderConflict(err)
}

func projectConfigHasOutput(config explorer.ProjectConfig, output string) bool {
	for _, view := range config.Views {
		if view.Output == output {
			return true
		}
	}
	return false
}

func (s *Store) AdoptViewBuilderCatalogs(ctx context.Context, project string) error {
	project = canonicalProjectConfigProject(strings.TrimSpace(project))
	if project == "" {
		return fmt.Errorf("view builder project is required")
	}
	err := s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{ExplorersCollection},
		Write: []string{RevisionsCollection, ViewBuilderSettingsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		owners, err := readProjectConfigOwners(txCtx, tx, project)
		if err != nil {
			return err
		}
		settings, err := readViewBuilderSettings(txCtx, tx, project)
		if err != nil {
			return err
		}
		ids := make([]string, 0, len(owners))
		for _, owner := range owners {
			if owner.value.ProjectConfig != nil && owner.value.ActiveRevisionID != "" {
				ids = append(ids, owner.value.ActiveRevisionID)
			}
		}
		revisions, err := readProjectConfigRevisions(txCtx, tx, uniqueStrings(ids))
		if err != nil {
			return err
		}
		byRecipe := make(map[string]explorer.Revision)
		ownerIDs := make([]string, 0, len(owners))
		for id := range owners {
			ownerIDs = append(ownerIDs, id)
		}
		sort.Strings(ownerIDs)
		for _, id := range ownerIDs {
			owner := owners[id].value
			if owner.ProjectConfig == nil || owner.ActiveRevisionID == "" {
				continue
			}
			if owner.ManagementMode != explorer.ManagementRepository || owner.Project != project || owner.ProjectConfig.ID != id {
				return fmt.Errorf("cannot adopt unmanaged project config owner %q: %w", id, explorer.ErrDraftConflict)
			}
			revision, ok := revisions[owner.ActiveRevisionID]
			if !ok || revision.Project != project || revision.ExplorerID != id || revision.Status != explorer.RevisionActive || revision.Publication.ExecutionID == "" || revision.Recipe.Name != owner.ProjectConfig.RecipeRef.Name {
				return fmt.Errorf("active config %q has an invalid publication snapshot", id)
			}
			recipeName := owner.ProjectConfig.RecipeRef.Name
			if prior, exists := byRecipe[recipeName]; exists && (prior.Publication.ExecutionID != revision.Publication.ExecutionID || prior.SourceGeneration != revision.SourceGeneration) {
				return fmt.Errorf("active configs for recipe %q reference different executions", recipeName)
			}
			if _, exists := byRecipe[recipeName]; !exists {
				byRecipe[recipeName] = revision
			}
		}

		updated := emptyViewBuilderSettings(project)
		if settings != nil {
			updated = cloneViewBuilderSettings(settings.value)
		}
		changed := settings == nil
		catalogSnapshots := make(map[string]explorer.Revision, len(byRecipe))
		for recipeName, revision := range byRecipe {
			if updated.Catalogs[recipeName] == "" {
				snapshot := adoptedCatalogSnapshot(revision, recipeName)
				catalogSnapshots[recipeName] = snapshot
				updated.Catalogs[recipeName] = snapshot.ID
				changed = true
			}
		}
		if updated.DefaultConfigID == "" {
			for _, id := range ownerIDs {
				owner := owners[id].value
				if owner.ProjectConfig == nil || owner.ActiveRevisionID == "" || owner.ProjectConfig.RecipeRef.Name == "" {
					continue
				}
				if len(owner.ProjectConfig.Views) == 0 {
					continue
				}
				updated.DefaultConfigID = id
				updated.DefaultFrameID = explorer.ViewBuilderFrameID(owner.ProjectConfig.RecipeRef.Name, owner.ProjectConfig.Views[0].Output)
				changed = true
				break
			}
		}
		if !changed {
			return nil
		}
		for _, snapshot := range catalogSnapshots {
			stored, err := readProjectConfigRevisions(txCtx, tx, []string{snapshot.ID})
			if err != nil {
				return err
			}
			if err := insertProjectConfigRevision(txCtx, tx, snapshot, stored[snapshot.ID]); err != nil {
				return err
			}
		}
		updated.Version++
		return writeViewBuilderSettings(txCtx, tx, settings, updated)
	})
	return normalizeViewBuilderConflict(err)
}

func parseViewBuilderFrameID(frameID string) (string, string, bool) {
	parts := strings.Split(frameID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" ||
		parts[0] != strings.TrimSpace(parts[0]) || parts[1] != strings.TrimSpace(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func adoptedCatalogSnapshot(source explorer.Revision, recipeName string) explorer.Revision {
	id := "catalog_" + key(source.Project, recipeName, source.Publication.ExecutionID, source.RecipeDigest, source.Dataset.SchemaDigest, string(source.PublicOutputContract))
	snapshot := source
	snapshot.ID = id
	snapshot.ExplorerID = ""
	snapshot.Config = nil
	snapshot.AuthoringBundle = nil
	snapshot.IntentDigest = ""
	snapshot.CompilationReceiptID = ""
	snapshot.CreatedBy = ""
	snapshot.Diagnostics = []explorer.Diagnostic{}
	snapshot.Status = explorer.RevisionReady
	snapshot.ActivatedAt = nil
	snapshot.FailedAt = nil
	snapshot.ReadyAt = cloneTimePointer(source.ReadyAt)
	snapshot.Publication.State = string(explorer.RevisionReady)
	snapshot.Publication.RevisionID = id
	return snapshot
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

var _ explorer.ViewBuilderRepository = (*Store)(nil)

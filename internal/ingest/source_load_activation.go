package ingest

import (
	"context"
	"errors"
	"fmt"

	dataset "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// prepareLegacySourceLoadActivation removes only the legacy mutable-source
// marker after the candidate is staged. The source revision and lease checks
// share an Arango transaction with the lifecycle pointer update, so an
// in-flight legacy publisher cannot be silently reclassified.
func (p *RepositorySourceLoader) prepareLegacySourceLoadActivation(ctx context.Context, record sourceLoadRecord) error {
	if record.SourceMode != "mutable" {
		return nil
	}
	active, err := p.datasets.ReadActiveRelease(ctx, record.Project)
	if err == nil {
		if active.Release.Generation == record.Generation && active.Release.GitCommit == record.Plan.Commit {
			return nil
		}
		return &SourcePublishConflictError{Reason: "another project release superseded the initial source load"}
	}
	if !errors.Is(err, dataset.ErrNoActiveRelease) {
		return err
	}
	txClient, ok := any(p.client).(arangostore.TransactionalRowClient)
	if !ok {
		return fmt.Errorf("legacy source-tracking migration requires transactional Arango support")
	}
	stateCollection := "loom_source_file_states"
	stateExists, err := p.client.CollectionExists(ctx, stateCollection)
	if err != nil {
		return fmt.Errorf("inspect legacy source state before activation: %w", err)
	}
	collections := []string{datasetarango.LifecycleCollection}
	if stateExists {
		collections = append(collections, stateCollection)
	}
	updated := false
	err = txClient.WithTransaction(ctx, arangostore.TransactionCollections{Write: collections}, func(txCtx context.Context, tx arangostore.RowQueryer) error {
		query := legacySourceLoadActivationAQL
		binds := map[string]any{
			"@lifecycle":        datasetarango.LifecycleCollection,
			"active_key":        datasetarango.ActiveDocumentKey(record.Project),
			"project":           record.Project,
			"base_revision":     record.BaseRevision,
			"legacy_revision":   record.LegacySourceRevision,
			"legacy_tracked":    record.LegacySourceTracked,
			"legacy_repository": record.LegacyRepository,
		}
		if stateExists {
			binds["@states"] = stateCollection
			binds["state_key"] = legacySourceStateKey(record.Project)
			err := tx.QueryRows(txCtx, query, 1, binds, func(row map[string]any) error {
				updated, _ = row["updated"].(bool)
				return nil
			})
			return err
		}
		query = legacySourceLoadActivationWithoutStateAQL
		return tx.QueryRows(txCtx, query, 1, binds, func(row map[string]any) error {
			updated, _ = row["updated"].(bool)
			return nil
		})
	})
	if err != nil {
		return fmt.Errorf("prepare legacy source tracking for candidate release: %w", err)
	}
	if !updated {
		return &SourcePublishMigrationRequiredError{Reason: "legacy mutable source revision or active pointer changed after candidate copy"}
	}
	return nil
}

const legacySourceLoadActivationAQL = `
LET active = DOCUMENT(@@lifecycle, @active_key)
LET releasePointer = FIRST(
  FOR pointer IN @@lifecycle
    FILTER pointer.project == @project AND pointer.recordType == "active_project_release"
    LIMIT 1
    RETURN pointer
)
LET source = DOCUMENT(@@states, @state_key)
FILTER active != null AND active.project == @project AND active.recordType == "active_generation"
FILTER active.dataset == null AND active.manifestKey == null
FILTER releasePointer != null AND TO_NUMBER(NOT_NULL(releasePointer.revision, 0)) == @base_revision
FILTER NOT_NULL(releasePointer.releaseKey, "") == "" AND NOT_NULL(releasePointer.releaseId, "") == ""
FILTER source == null
  ? (@legacy_tracked == false AND @legacy_revision == 0 AND @legacy_repository == "")
  : (source.project == @project
    AND TO_NUMBER(NOT_NULL(source.sourceRevision, 0)) == @legacy_revision
    AND NOT_NULL(source.sourceTracked, false) == @legacy_tracked
    AND NOT_NULL(source.sourceRepository, "") == @legacy_repository
    AND NOT_NULL(source.pendingOperationId, "") == ""
    AND NOT_NULL(source.pendingPhase, "") == ""
    AND NOT_NULL(source.leaseOwnerId, "") == ""
    AND TO_NUMBER(NOT_NULL(source.leaseExpiresAt, 0)) < DATE_NOW())
FILTER active.sourceTracked != true OR @legacy_tracked == true
UPDATE active WITH {sourceTracked: false} IN @@lifecycle
RETURN {updated: true}`

const legacySourceLoadActivationWithoutStateAQL = `
LET active = DOCUMENT(@@lifecycle, @active_key)
LET releasePointer = FIRST(
  FOR pointer IN @@lifecycle
    FILTER pointer.project == @project AND pointer.recordType == "active_project_release"
    LIMIT 1
    RETURN pointer
)
FILTER active != null AND active.project == @project AND active.recordType == "active_generation"
FILTER active.dataset == null AND active.manifestKey == null
FILTER releasePointer != null AND TO_NUMBER(NOT_NULL(releasePointer.revision, 0)) == @base_revision
FILTER NOT_NULL(releasePointer.releaseKey, "") == "" AND NOT_NULL(releasePointer.releaseId, "") == ""
FILTER @legacy_tracked == false AND @legacy_revision == 0 AND @legacy_repository == ""
FILTER active.sourceTracked != true
UPDATE active WITH {sourceTracked: false} IN @@lifecycle
RETURN {updated: true}`

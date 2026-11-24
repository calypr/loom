package sourceload

import (
	"context"
	"errors"
	"fmt"

	dataset "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

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

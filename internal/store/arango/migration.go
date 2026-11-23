package arango

import (
	"context"
	"errors"
	"fmt"
	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/utils"
	"slices"
	"strings"
)

type previewMigrationCollection interface {
	Indexes(context.Context) ([]driver.IndexResponse, error)
	EnsurePersistentIndex(context.Context, []string, *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error)
	DeleteIndexByID(context.Context, string) error
}

func migratePreviewSupersededCollection(ctx context.Context, collectionName string, collection previewMigrationCollection, dryRun bool) ([]PreviewSupersededIndex, error) {
	indexes, err := collection.Indexes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list indexes in %q: %w", collectionName, err)
	}
	_, candidates, _ := auditPreviewIndexCollection(collectionName, indexes)
	if dryRun {
		return candidates, nil
	}
	for _, candidate := range candidates {
		if err := migratePreviewSupersededIndex(ctx, collection, candidate); err != nil {
			return candidates, fmt.Errorf("migrate %s/%s: %w", collectionName, candidate.OldName, err)
		}
	}
	return candidates, nil
}

func samePreviewIndexesExcept(before, after []driver.IndexResponse, excluded ...string) bool {
	skip := make(map[string]bool, len(excluded))
	for _, id := range excluded {
		skip[id] = true
	}
	remaining := make(map[string]driver.IndexResponse, len(after))
	for _, index := range after {
		if !skip[index.ID] {
			remaining[index.ID] = index
		}
	}
	for _, index := range before {
		if skip[index.ID] {
			continue
		}
		other, found := remaining[index.ID]
		if !found || index.Name != other.Name || index.Type != other.Type ||
			!slices.Equal(previewIndexFields(index), previewIndexFields(other)) ||
			!slices.Equal(previewIndexStoredValues(index), previewIndexStoredValues(other)) ||
			!samePreviewIndexFlag(index.Unique, other.Unique) || !samePreviewIndexFlag(index.Sparse, other.Sparse) {
			return false
		}
		delete(remaining, index.ID)
	}
	return len(remaining) == 0
}

func samePreviewIndexFlag(left, right *bool) bool {
	return (left == nil) == (right == nil) && (left == nil || *left == *right)
}

func migratePreviewSupersededIndex(ctx context.Context, collection previewMigrationCollection, candidate PreviewSupersededIndex) error {
	if strings.HasSuffix(candidate.Kind, ":replacement-name-conflict") {
		return errors.New("replacement index name exists with a different definition")
	}
	indexes, err := collection.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("read pre-migration inventory: %w", err)
	}
	initial := append([]driver.IndexResponse(nil), indexes...)
	old, oldExists := findIndexByID(indexes, candidate.OldID)
	if !oldExists {
		if replacement, ok := findIndexByName(indexes, candidate.NewName); ok &&
			previewCoveringIndexMatchesDefinition(replacement, candidate.NewFields, candidate.NewStoredValues) {
			return nil
		}
		return errors.New("exact old index ID is absent and exact replacement is not present")
	}
	if !previewCoveringIndexMatchesDefinition(old, candidate.OldFields, candidate.OldStoredValues) || old.Name != candidate.OldName {
		return errors.New("legacy index ID no longer has its audited exact definition")
	}
	newIndex, newExists := findIndexByName(indexes, candidate.NewName)
	if newExists && !previewCoveringIndexMatchesDefinition(newIndex, candidate.NewFields, candidate.NewStoredValues) {
		return errors.New("replacement index name exists with a different definition")
	}
	createdByMigration := false
	if !newExists {
		var created bool
		newIndex, created, err = collection.EnsurePersistentIndex(ctx, candidate.NewFields, &driver.CreatePersistentIndexOptions{
			Name: candidate.NewName, StoredValues: append([]string(nil), candidate.NewStoredValues...),
			Sparse: utils.NewType(false), Unique: utils.NewType(false),
		})
		if err != nil {
			return fmt.Errorf("create replacement before deleting legacy index: %w", err)
		}
		if newIndex.ID == "" || !previewCoveringIndexMatchesDefinition(newIndex, candidate.NewFields, candidate.NewStoredValues) || newIndex.Name != candidate.NewName {
			return errors.New("Arango did not return the exact replacement definition")
		}
		createdByMigration = created
	}
	beforeDelete, err := collection.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("reread before legacy deletion: %w", err)
	}
	oldNow, oldOK := findIndexByID(beforeDelete, candidate.OldID)
	newNow, newOK := findIndexByID(beforeDelete, newIndex.ID)
	if !oldOK || oldNow.Name != candidate.OldName ||
		!previewCoveringIndexMatchesDefinition(oldNow, candidate.OldFields, candidate.OldStoredValues) ||
		!newOK || newNow.Name != candidate.NewName ||
		!previewCoveringIndexMatchesDefinition(newNow, candidate.NewFields, candidate.NewStoredValues) ||
		!samePreviewIndexesExcept(initial, beforeDelete, candidate.OldID, newIndex.ID) {
		return errors.New("inventory changed before deletion; old ID, replacement, or unrelated definition no longer matches")
	}
	if deleteErr := collection.DeleteIndexByID(ctx, candidate.OldID); deleteErr != nil {
		observed, readErr := collection.Indexes(ctx)
		if readErr != nil {
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), fmt.Errorf("reread after uncertain delete: %w; verified candidate retained", readErr))
		}
		oldAfter, oldStillPresent := findIndexByID(observed, candidate.OldID)
		if !oldStillPresent {
			if replacement, exists := findIndexByID(observed, newIndex.ID); exists && replacement.Name == candidate.NewName && previewCoveringIndexMatchesDefinition(replacement, candidate.NewFields, candidate.NewStoredValues) {
				return fmt.Errorf("delete exact legacy index ID %q returned an error after deletion; verified replacement retained: %w", candidate.OldID, deleteErr)
			}
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), errors.New("old index is absent and verified replacement is missing; refusing cleanup"))
		}
		if oldAfter.Name != candidate.OldName || !previewCoveringIndexMatchesDefinition(oldAfter, candidate.OldFields, candidate.OldStoredValues) {
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), errors.New("old ID remains with a changed definition; replacement retained"))
		}
		if !createdByMigration {
			return fmt.Errorf("delete exact legacy index ID %q failed; pre-existing verified replacement retained: %w", candidate.OldID, deleteErr)
		}
		if err := collection.DeleteIndexByID(ctx, newIndex.ID); err != nil {
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), fmt.Errorf("roll back newly created replacement ID %q: %w", newIndex.ID, err))
		}
		rolledBack, rollbackReadErr := collection.Indexes(ctx)
		if rollbackReadErr != nil {
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), fmt.Errorf("verify replacement rollback: %w", rollbackReadErr))
		}
		oldRestored, oldStillPresent := findIndexByID(rolledBack, candidate.OldID)
		if !oldStillPresent || oldRestored.Name != candidate.OldName || !previewCoveringIndexMatchesDefinition(oldRestored, candidate.OldFields, candidate.OldStoredValues) {

			restored, _, restoreErr := collection.EnsurePersistentIndex(ctx, candidate.NewFields, &driver.CreatePersistentIndexOptions{
				Name: candidate.NewName, StoredValues: append([]string(nil), candidate.NewStoredValues...), Sparse: utils.NewType(false), Unique: utils.NewType(false),
			})
			if restoreErr == nil && restored.ID != "" && restored.Name == candidate.NewName && previewCoveringIndexMatchesDefinition(restored, candidate.NewFields, candidate.NewStoredValues) {
				return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), errors.New("old index disappeared during rollback; verified replacement restored"))
			}
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), errors.New("old index disappeared during rollback and replacement restoration failed"), restoreErr)
		}
		if _, candidatePresent := findIndexByID(rolledBack, newIndex.ID); candidatePresent {
			return errors.Join(fmt.Errorf("delete exact legacy index ID %q: %w", candidate.OldID, deleteErr), errors.New("newly created replacement remained after rollback"))
		}
		return fmt.Errorf("delete exact legacy index ID %q failed; rolled back newly created replacement: %w", candidate.OldID, deleteErr)
	}
	afterDelete, err := collection.Indexes(ctx)
	if err != nil {
		return fmt.Errorf("verify final inventory: %w", err)
	}
	if _, ok := findIndexByID(afterDelete, candidate.OldID); ok {
		return errors.New("legacy index remains after delete")
	}
	final, ok := findIndexByID(afterDelete, newIndex.ID)
	if !ok || final.Name != candidate.NewName || !previewCoveringIndexMatchesDefinition(final, candidate.NewFields, candidate.NewStoredValues) ||
		!samePreviewIndexesExcept(beforeDelete, afterDelete, candidate.OldID) {
		return errors.New("final inventory did not preserve the replacement and unrelated definitions")
	}
	return nil
}

func findIndexByID(indexes []driver.IndexResponse, id string) (driver.IndexResponse, bool) {
	for _, index := range indexes {
		if index.ID == id {
			return index, true
		}
	}
	return driver.IndexResponse{}, false
}

func findIndexByName(indexes []driver.IndexResponse, name string) (driver.IndexResponse, bool) {
	for _, index := range indexes {
		if index.Name == name {
			return index, true
		}
	}
	return driver.IndexResponse{}, false
}

package arango

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func (s *Store) SaveRelease(ctx context.Context, release dataset.ProjectRelease) (dataset.ProjectRelease, error) {
	document, err := lifecycleDocument(release)
	if err != nil {
		return dataset.ProjectRelease{}, err
	}
	document["_key"] = releaseDocumentKey(release.ID)
	document["recordType"] = releaseRecordType
	binds := lifecycleCollectionBindVars()
	binds["key"], binds["candidate"] = document["_key"], document
	binds["active_release_key"] = activeReleaseDocumentKey(release.Project)
	binds["active_release_placeholder"] = activeReleasePlaceholderDocument(release.Project)
	var saved *dataset.ProjectRelease
	err = s.client.QueryRows(ctx, saveReleaseAQL, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[dataset.ProjectRelease](row)
		if err != nil {
			return err
		}
		saved = &decoded
		return nil
	})
	if err != nil {
		return dataset.ProjectRelease{}, err
	}
	if saved == nil {
		return dataset.ProjectRelease{}, fmt.Errorf("release already exists with different content")
	}
	return *saved, nil
}

func (s *Store) ReadActiveRelease(ctx context.Context, project string) (dataset.ActiveRelease, error) {
	binds := lifecycleCollectionBindVars()
	binds["key"] = activeReleaseDocumentKey(project)
	var active *dataset.ActiveRelease
	err := s.client.QueryRows(ctx, readActiveReleaseAQL, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[dataset.ActiveRelease](row)
		if err != nil {
			return err
		}
		active = &decoded
		return nil
	})
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("read active release: %w", err)
	}
	if active == nil {
		return dataset.ActiveRelease{}, dataset.ErrNoActiveRelease
	}
	return *active, nil
}

func (s *Store) CompareAndSwapActivateRelease(ctx context.Context, release dataset.ProjectRelease, expectedRevision int64) (dataset.ActiveRelease, error) {
	return CompareAndSwapActivateRelease(ctx, s.client, release, expectedRevision)
}

// CompareAndSwapActivateRelease commits the active Explorer revision and
// matching dataset release in one Arango transaction.
func CompareAndSwapActivateRelease(ctx context.Context, client arangostore.RowQueryer, release dataset.ProjectRelease, expectedRevision int64) (dataset.ActiveRelease, error) {
	return compareAndSwapActivateRelease(ctx, client, release, expectedRevision, false)
}

// CompareAndSwapPublishRelease activates a release produced by an Explorer
// publication. The publication is only current when the active generation
// pointer still identifies its source manifest.
func CompareAndSwapPublishRelease(ctx context.Context, client arangostore.RowQueryer, release dataset.ProjectRelease, expectedRevision int64) (dataset.ActiveRelease, error) {
	return compareAndSwapActivateRelease(ctx, client, release, expectedRevision, true)
}

func compareAndSwapActivateRelease(ctx context.Context, client arangostore.RowQueryer, release dataset.ProjectRelease, expectedRevision int64, requireCurrentGeneration bool) (dataset.ActiveRelease, error) {
	releaseDocument, err := lifecycleDocument(release)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	releaseDocument["_key"] = releaseDocumentKey(release.ID)
	releaseDocument["recordType"] = releaseRecordType
	binds := lifecycleCollectionBindVars()
	binds["project"] = release.Project
	binds["release_key"] = releaseDocument["_key"]
	binds["release_id"] = release.ID
	binds["generation"] = release.Generation
	binds["manifest_key"] = manifestDocumentKey(dataset.Ref{Project: release.Project, Generation: release.Generation})
	binds["active_key"] = activeDocumentKey(release.Project)
	binds["active_release_key"] = activeReleaseDocumentKey(release.Project)
	binds["expected_revision"] = expectedRevision
	binds["next_revision"] = expectedRevision + 1
	binds["require_current_generation"] = requireCurrentGeneration
	binds["staged_state"] = string(dataset.StateStaged)
	binds["ready_state"] = string(dataset.StateReady)
	var active *dataset.ActiveRelease
	err = client.QueryRows(ctx, activateReleaseAQL, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[dataset.ActiveRelease](row)
		if err != nil {
			return err
		}
		active = &decoded
		return nil
	})
	if err != nil {
		// Concurrent release CAS operations can both observe the same revision
		// before Arango serializes their pointer writes. Arango reports the loser
		// as error 1200 (write-write conflict), so preserve the repository's
		// typed CAS contract for callers instead of leaking a backend failure.
		if shared.IsArangoErrorWithErrorNum(err, shared.ErrArangoConflict) {
			return dataset.ActiveRelease{}, fmt.Errorf("%w: concurrent active-pointer update", dataset.ErrReleaseActivationConflict)
		}
		return dataset.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	if active == nil {
		return dataset.ActiveRelease{}, dataset.ErrReleaseActivationConflict
	}
	return *active, nil
}

func lifecycleDocument(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	return document, nil
}

func decodeValue[T any](value any) (T, error) {
	var decoded T
	encoded, err := json.Marshal(value)
	if err != nil {
		return decoded, err
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

func releaseDocumentKey(id string) string            { return documentKey("release", id) }
func activeReleaseDocumentKey(project string) string { return documentKey("active_release", project) }

const saveReleaseAQL = `
LET existing = DOCUMENT(@@lifecycle_collection, @key)
LET activeRelease = DOCUMENT(@@lifecycle_collection, @active_release_key)
// F-B15: re-saving a content-addressed release is a no-op ONLY when the
// content matches; createdAt is the first-creation timestamp and is excluded
// from the equality (a re-save keeps the ORIGINAL document untouched, so the
// timestamp cannot drift). Any other field difference stays incompatible.
LET compatible = existing != null AND existing.recordType == "project_release"
  AND UNSET(existing, "_key", "_id", "_rev", "recordType", "createdAt") == UNSET(@candidate, "_key", "recordType", "createdAt")
FILTER existing == null OR compatible
LET documents = APPEND(existing == null ? [@candidate] : [], activeRelease == null ? [@active_release_placeholder] : [])
LET inserted = (FOR document IN documents INSERT document INTO @@lifecycle_collection RETURN NEW)
LET saved = existing == null ? FIRST(FOR document IN inserted FILTER document.recordType == "project_release" RETURN document) : existing
RETURN UNSET(saved, "_key", "_id", "_rev", "recordType")
`

const readActiveReleaseAQL = `
LET pointer = DOCUMENT(@@lifecycle_collection, @key)
FILTER pointer != null AND pointer.recordType == "active_project_release"
LET release = DOCUMENT(@@lifecycle_collection, pointer.releaseKey)
FILTER release != null AND release.recordType == "project_release"
RETURN {release: UNSET(release, "_key", "_id", "_rev", "recordType"), revision: pointer.revision}
`

const activateReleaseAQL = `
LET manifest = DOCUMENT(@@lifecycle_collection, @manifest_key)
LET generationPointer = DOCUMENT(@@lifecycle_collection, @active_key)
LET releasePointer = DOCUMENT(@@lifecycle_collection, @active_release_key)
LET storedRelease = DOCUMENT(@@lifecycle_collection, @release_key)
LET currentRevision = releasePointer == null ? 0 : releasePointer.revision
LET alreadyActive = releasePointer != null AND releasePointer.releaseId == @release_id
LET selectedRevision = alreadyActive ? currentRevision : @next_revision
FILTER storedRelease != null AND storedRelease.recordType == "project_release"
FILTER storedRelease.project == @project AND storedRelease.id == @release_id AND storedRelease.generation == @generation
FILTER manifest != null AND manifest.recordType == "manifest"
FILTER manifest.dataset.project == @project AND manifest.dataset.generation == @generation
FILTER manifest.state IN [@staged_state, @ready_state]
FILTER generationPointer != null AND generationPointer.recordType == "active_generation"
FILTER generationPointer.sourceTracked != true
FILTER NOT @require_current_generation OR (
  generationPointer.dataset.project == @project
  AND generationPointer.dataset.generation == @generation
  AND generationPointer.manifestKey == @manifest_key
)
FILTER releasePointer != null AND releasePointer.recordType == "active_project_release"
FILTER currentRevision == @expected_revision OR alreadyActive
// F-B15: with content-addressed release IDs an identical re-activation hits
// alreadyActive and performs ZERO writes (no pointer re-patch, no revision
// bump, not even an _rev churn), returning the current active release.
LET updates = alreadyActive ? [] : [
  {document: releasePointer, patch: {releaseKey: @release_key, releaseId: @release_id, revision: selectedRevision}},
  {document: generationPointer, patch: {dataset: manifest.dataset, manifestKey: manifest._key, releaseId: @release_id, releaseRevision: selectedRevision}}
]
LET patched = (FOR item IN updates
  UPDATE item.document WITH item.patch IN @@lifecycle_collection OPTIONS {ignoreRevs: false, mergeObjects: false}
  RETURN 1)
FILTER LENGTH(patched) == LENGTH(updates)
RETURN {release: UNSET(storedRelease, "_key", "_id", "_rev", "recordType"), revision: selectedRevision}
`

var _ dataset.ReleaseRepository = (*Store)(nil)

package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

func (s *Store) CreateOrResumeSnapshot(ctx context.Context, candidate dataset.SnapshotGeneration) (dataset.SnapshotGeneration, error) {
	if err := candidate.Validate(); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	document, err := lifecycleDocument(candidate)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	document["_key"] = snapshotDocumentKey(candidate.Dataset)
	document["recordType"] = snapshotRecordType
	document["project"] = candidate.Dataset.Project
	binds := lifecycleBindVars(candidate.Dataset.Project)
	binds["key"], binds["candidate"] = document["_key"], document
	rows, err := s.snapshotRows(ctx, createOrResumeSnapshotAQL, binds)
	if err != nil {
		return dataset.SnapshotGeneration{}, fmt.Errorf("create or resume snapshot: %w", err)
	}
	if len(rows) == 0 {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotConflict
	}
	return rows[0], nil
}

func (s *Store) ReadSnapshot(ctx context.Context, ref dataset.Ref) (dataset.SnapshotGeneration, error) {
	if err := ref.Validate(); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	binds := lifecycleBindVars(ref.Project)
	binds["key"], binds["generation"] = snapshotDocumentKey(ref), ref.Generation
	rows, err := s.snapshotRows(ctx, readSnapshotAQL, binds)
	if err != nil {
		return dataset.SnapshotGeneration{}, fmt.Errorf("read snapshot: %w", err)
	}
	if len(rows) == 0 {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotNotFound
	}
	return rows[0], nil
}

func (s *Store) RecordSnapshotUpload(ctx context.Context, ref dataset.Ref, upload dataset.ResourceUpload) (dataset.SnapshotGeneration, error) {
	binds := lifecycleBindVars(ref.Project)
	binds["key"], binds["generation"], binds["upload"] = snapshotDocumentKey(ref), ref.Generation, upload
	binds["loading_state"] = string(dataset.StateLoading)
	rows, err := s.snapshotRows(ctx, recordSnapshotUploadAQL, binds)
	if err != nil {
		return dataset.SnapshotGeneration{}, fmt.Errorf("record snapshot upload: %w", err)
	}
	if len(rows) == 0 {
		existing, readErr := s.ReadSnapshot(ctx, ref)
		if readErr != nil {
			return dataset.SnapshotGeneration{}, readErr
		}
		if prior, ok := existing.Upload(upload.ResourceType); ok && prior.SHA256 != upload.SHA256 {
			return dataset.SnapshotGeneration{}, dataset.ErrChecksumConflict
		}
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotConflict
	}
	return rows[0], nil
}

func (s *Store) TransitionSnapshot(ctx context.Context, ref dataset.Ref, expected, next dataset.State, now time.Time) (dataset.SnapshotGeneration, error) {
	binds := lifecycleBindVars(ref.Project)
	binds["key"], binds["generation"] = snapshotDocumentKey(ref), ref.Generation
	binds["expected"], binds["next"], binds["updated_at"] = string(expected), string(next), now.UTC()
	rows, err := s.snapshotRows(ctx, transitionSnapshotAQL, binds)
	if err != nil {
		return dataset.SnapshotGeneration{}, fmt.Errorf("transition snapshot: %w", err)
	}
	if len(rows) == 0 {
		existing, readErr := s.ReadSnapshot(ctx, ref)
		if readErr == nil && existing.State == next {
			return existing, nil
		}
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotConflict
	}
	return rows[0], nil
}

func (s *Store) ListSnapshotProjects(ctx context.Context) ([]string, error) {
	return s.projectRows(ctx, listSnapshotProjectsAQL, snapshotRecordType)
}

func (s *Store) SaveRelease(ctx context.Context, release dataset.ProjectRelease) (dataset.ProjectRelease, error) {
	document, err := lifecycleDocument(release)
	if err != nil {
		return dataset.ProjectRelease{}, err
	}
	document["_key"] = releaseDocumentKey(release.ID)
	document["recordType"] = releaseRecordType
	binds := lifecycleBindVars(release.Project)
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
		return dataset.ProjectRelease{}, dataset.ErrSnapshotConflict
	}
	return *saved, nil
}

func (s *Store) ReadRelease(ctx context.Context, project, releaseID string) (dataset.ProjectRelease, error) {
	binds := lifecycleBindVars(project)
	binds["key"], binds["release_id"] = releaseDocumentKey(releaseID), releaseID
	var release *dataset.ProjectRelease
	err := s.client.QueryRows(ctx, readReleaseAQL, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[dataset.ProjectRelease](row)
		if err != nil {
			return err
		}
		release = &decoded
		return nil
	})
	if err != nil {
		return dataset.ProjectRelease{}, err
	}
	if release == nil {
		return dataset.ProjectRelease{}, dataset.ErrReleaseNotFound
	}
	return *release, nil
}

func (s *Store) ReadActiveRelease(ctx context.Context, project string) (dataset.ActiveRelease, error) {
	binds := lifecycleBindVars(project)
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
	releaseDocument, err := lifecycleDocument(release)
	if err != nil {
		return dataset.ActiveRelease{}, err
	}
	releaseDocument["_key"] = releaseDocumentKey(release.ID)
	releaseDocument["recordType"] = releaseRecordType
	binds := lifecycleBindVars(release.Project)
	binds["release"] = releaseDocument
	binds["release_key"] = releaseDocument["_key"]
	binds["release_id"] = release.ID
	binds["generation"] = release.Generation
	binds["manifest_key"] = manifestDocumentKey(dataset.Ref{Project: release.Project, Generation: release.Generation})
	binds["active_key"] = activeDocumentKey(release.Project)
	binds["active_release_key"] = activeReleaseDocumentKey(release.Project)
	binds["expected_revision"] = expectedRevision
	binds["next_revision"] = expectedRevision + 1
	binds["staged_state"] = string(dataset.StateStaged)
	binds["ready_state"] = string(dataset.StateReady)
	var active *dataset.ActiveRelease
	err = s.client.QueryRows(ctx, activateReleaseAQL, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[dataset.ActiveRelease](row)
		if err != nil {
			return err
		}
		active = &decoded
		return nil
	})
	if err != nil {
		return dataset.ActiveRelease{}, fmt.Errorf("activate release: %w", err)
	}
	if active == nil {
		return dataset.ActiveRelease{}, dataset.ErrReleaseActivationConflict
	}
	return *active, nil
}

func (s *Store) ListReleaseProjects(ctx context.Context) ([]string, error) {
	return s.projectRows(ctx, listReleaseProjectsAQL, activeReleaseRecordType)
}

func (s *Store) ListRetentionGenerations(ctx context.Context) ([]dataset.RetentionGeneration, error) {
	results := make([]dataset.RetentionGeneration, 0)
	binds := map[string]any{
		"@lifecycle_collection":      LifecycleCollection,
		"@executions_collection":     "loom_dataframe_bundle_executions",
		"snapshot_record_type":       snapshotRecordType,
		"active_record_type":         activeRecordType,
		"active_release_record_type": activeReleaseRecordType,
		"release_record_type":        releaseRecordType,
		"in_flight_states":           []string{"QUEUED", "RUNNING", "VALIDATING", "PENDING", "PREFLIGHT", "LOADING"},
	}
	err := s.client.QueryRows(ctx, listRetentionGenerationsAQL, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[struct {
			Dataset     dataset.Ref   `json:"dataset"`
			State       dataset.State `json:"state"`
			UpdatedAt   time.Time     `json:"updatedAt"`
			Active      bool          `json:"active"`
			LastGood    bool          `json:"lastGood"`
			InFlight    bool          `json:"inFlight"`
			Recoverable bool          `json:"recoverable"`
		}](row)
		if err != nil {
			return err
		}
		results = append(results, dataset.RetentionGeneration{
			Dataset: decoded.Dataset, State: decoded.State, UpdatedAt: decoded.UpdatedAt,
			Active: decoded.Active, LastGood: decoded.LastGood, InFlight: decoded.InFlight, Recoverable: decoded.Recoverable,
		})
		return nil
	})
	return results, err
}

func (s *Store) DeleteGeneration(ctx context.Context, ref dataset.Ref) error {
	snapshot, err := s.ReadSnapshot(ctx, ref)
	if err != nil {
		return err
	}
	if snapshot.State != dataset.StateFailed {
		return fmt.Errorf("refuse to delete recoverable generation %s/%s in state %s", ref.Project, ref.Generation, snapshot.State)
	}
	targets := make([]string, 0)
	if _, manifestErr := s.ReadManifest(ctx, ref); manifestErr == nil {
		targets = append(targets, snapshot.ExpectedResourceTypes...)
		targets = append(targets, "fhir_edge", "fhir_field_catalog", "fhir_relationship_catalog")
	} else if !errors.Is(manifestErr, ErrManifestNotFound) {
		return manifestErr
	}
	for _, target := range targets {
		binds := guardedDeletionBindVars(ref)
		binds["@target_collection"] = target
		if err := s.client.QueryRows(ctx, guardedDeleteGenerationDataAQL, metadataBatchSize, binds, func(map[string]any) error { return nil }); err != nil {
			return err
		}
	}
	binds := guardedDeletionBindVars(ref)
	binds["snapshot_key"] = snapshotDocumentKey(ref)
	binds["manifest_key"] = manifestDocumentKey(ref)
	if err := s.client.QueryRows(ctx, guardedDeleteGenerationMetadataAQL, metadataBatchSize, binds, func(map[string]any) error { return nil }); err != nil {
		return err
	}
	return nil
}

func guardedDeletionBindVars(ref dataset.Ref) map[string]any {
	return map[string]any{
		"@lifecycle_collection":  LifecycleCollection,
		"@executions_collection": "loom_dataframe_bundle_executions",
		"project":                ref.Project, "generation": ref.Generation,
		"active_key":         activeDocumentKey(ref.Project),
		"active_release_key": activeReleaseDocumentKey(ref.Project),
		"failed_state":       string(dataset.StateFailed),
		"in_flight_states":   []string{"QUEUED", "RUNNING", "VALIDATING", "PENDING", "PREFLIGHT", "LOADING"},
	}
}

func (s *Store) snapshotRows(ctx context.Context, query string, binds map[string]any) ([]dataset.SnapshotGeneration, error) {
	rows := make([]dataset.SnapshotGeneration, 0, 1)
	err := s.client.QueryRows(ctx, query, metadataBatchSize, binds, func(row map[string]any) error {
		decoded, err := decodeValue[dataset.SnapshotGeneration](row)
		if err != nil {
			return err
		}
		rows = append(rows, decoded)
		return nil
	})
	return rows, err
}

func (s *Store) projectRows(ctx context.Context, query, recordType string) ([]string, error) {
	projects := make([]string, 0)
	err := s.client.QueryRows(ctx, query, metadataBatchSize, map[string]any{"@lifecycle_collection": LifecycleCollection, "record_type": recordType}, func(row map[string]any) error {
		project, _ := row["project"].(string)
		if project != "" {
			projects = append(projects, project)
		}
		return nil
	})
	return projects, err
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

func snapshotDocumentKey(ref dataset.Ref) string {
	return documentKey("snapshot", ref.Project, ref.Generation)
}
func releaseDocumentKey(id string) string            { return documentKey("release", id) }
func activeReleaseDocumentKey(project string) string { return documentKey("active_release", project) }

const createOrResumeSnapshotAQL = `
LET existing = DOCUMENT(@@lifecycle_collection, @key)
LET compatible = existing != null
  AND existing.recordType == "snapshot_generation"
  AND existing.dataset == @candidate.dataset
  AND existing.gitCommit == @candidate.gitCommit
  AND existing.expectedResourceTypes == @candidate.expectedResourceTypes
  AND existing.authResourcePath == @candidate.authResourcePath
FILTER existing == null OR compatible
LET stored = existing == null ? FIRST(INSERT @candidate INTO @@lifecycle_collection RETURN NEW) : existing
RETURN UNSET(stored, "_key", "_id", "_rev", "recordType", "project")
`

const readSnapshotAQL = `
FOR snapshot IN @@lifecycle_collection
  FILTER snapshot._key == @key AND snapshot.recordType == "snapshot_generation"
  FILTER snapshot.dataset.project == @project AND snapshot.dataset.generation == @generation
  LIMIT 1
  RETURN UNSET(snapshot, "_key", "_id", "_rev", "recordType", "project")
`

const recordSnapshotUploadAQL = `
LET snapshot = DOCUMENT(@@lifecycle_collection, @key)
LET prior = snapshot == null ? null : FIRST(FOR upload IN snapshot.uploads FILTER upload.resourceType == @upload.resourceType RETURN upload)
LET idempotent = prior != null AND prior.sha256 == @upload.sha256 AND prior.size == @upload.size
FILTER snapshot != null AND snapshot.recordType == "snapshot_generation"
FILTER snapshot.dataset.project == @project AND snapshot.dataset.generation == @generation
FILTER @upload.resourceType IN snapshot.expectedResourceTypes
FILTER idempotent OR (snapshot.state == @loading_state AND prior == null)
LET stored = idempotent ? snapshot : FIRST(UPDATE snapshot WITH {uploads: APPEND(snapshot.uploads, @upload), updatedAt: @upload.uploadedAt} IN @@lifecycle_collection RETURN NEW)
RETURN UNSET(stored, "_key", "_id", "_rev", "recordType", "project")
`

const transitionSnapshotAQL = `
FOR snapshot IN @@lifecycle_collection
  FILTER snapshot._key == @key AND snapshot.recordType == "snapshot_generation"
  FILTER snapshot.dataset.project == @project AND snapshot.dataset.generation == @generation
  FILTER snapshot.state == @expected
  UPDATE snapshot WITH {state: @next, updatedAt: @updated_at, abortedAt: @next == "FAILED" ? @updated_at : null} IN @@lifecycle_collection
  RETURN UNSET(NEW, "_key", "_id", "_rev", "recordType", "project")
`

const listSnapshotProjectsAQL = `FOR doc IN @@lifecycle_collection FILTER doc.recordType == @record_type OR doc.recordType == "manifest" COLLECT project = doc.dataset.project SORT project RETURN {project}`
const listReleaseProjectsAQL = `FOR doc IN @@lifecycle_collection FILTER doc.recordType == @record_type OR doc.recordType == "project_release" COLLECT project = doc.project SORT project RETURN {project}`

const saveReleaseAQL = `
LET existing = DOCUMENT(@@lifecycle_collection, @key)
LET activeRelease = DOCUMENT(@@lifecycle_collection, @active_release_key)
LET compatible = existing != null AND existing.recordType == "project_release"
  AND UNSET(existing, "_key", "_id", "_rev", "recordType") == UNSET(@candidate, "_key", "recordType")
FILTER existing == null OR compatible
LET documents = APPEND(existing == null ? [@candidate] : [], activeRelease == null ? [@active_release_placeholder] : [])
LET inserted = (FOR document IN documents INSERT document INTO @@lifecycle_collection RETURN NEW)
LET saved = existing == null ? FIRST(FOR document IN inserted FILTER document.recordType == "project_release" RETURN document) : existing
RETURN UNSET(saved, "_key", "_id", "_rev", "recordType")
`

const readReleaseAQL = `
FOR release IN @@lifecycle_collection
  FILTER release._key == @key AND release.recordType == "project_release"
  FILTER release.project == @project AND release.id == @release_id
  LIMIT 1
  RETURN UNSET(release, "_key", "_id", "_rev", "recordType")
`

const listRetentionGenerationsAQL = `
FOR snapshot IN @@lifecycle_collection
  FILTER snapshot.recordType == @snapshot_record_type
  LET activeGeneration = FIRST(FOR pointer IN @@lifecycle_collection FILTER pointer.recordType == @active_record_type AND pointer.project == snapshot.dataset.project RETURN pointer.dataset.generation)
  LET activeReleasePointer = FIRST(FOR pointer IN @@lifecycle_collection FILTER pointer.recordType == @active_release_record_type AND pointer.project == snapshot.dataset.project RETURN pointer)
  LET activeRelease = activeReleasePointer == null ? null : DOCUMENT(@@lifecycle_collection, activeReleasePointer.releaseKey)
  LET lastGood = activeRelease != null AND (activeRelease.generation == snapshot.dataset.generation OR snapshot.dataset.generation IN activeRelease.publications[*].generation)
  LET inFlight = FIRST(FOR execution IN @@executions_collection
    LET project = execution.project != null ? execution.project : execution.Project
    LET generation = execution.datasetGeneration != null ? execution.datasetGeneration : execution.DatasetGeneration
    FILTER project == snapshot.dataset.project AND generation == snapshot.dataset.generation
    FILTER execution.state IN @in_flight_states LIMIT 1 RETURN true)
  RETURN {
    dataset: snapshot.dataset, state: snapshot.state, updatedAt: snapshot.updatedAt,
    active: activeGeneration == snapshot.dataset.generation,
    lastGood, inFlight: inFlight == true,
    recoverable: snapshot.state IN ["LOADING", "STAGED", "READY"]
  }
`

const guardedDeleteGenerationDataAQL = `
LET snapshot = FIRST(FOR doc IN @@lifecycle_collection FILTER doc.recordType == "snapshot_generation" AND doc.dataset.project == @project AND doc.dataset.generation == @generation RETURN doc)
LET active = DOCUMENT(@@lifecycle_collection, @active_key)
LET releasePointer = DOCUMENT(@@lifecycle_collection, @active_release_key)
LET release = releasePointer == null ? null : DOCUMENT(@@lifecycle_collection, releasePointer.releaseKey)
LET protectedByRelease = release != null AND (release.generation == @generation OR @generation IN release.publications[*].generation)
LET inFlight = FIRST(FOR execution IN @@executions_collection
  LET project = execution.project != null ? execution.project : execution.Project
  LET generation = execution.datasetGeneration != null ? execution.datasetGeneration : execution.DatasetGeneration
  FILTER project == @project AND generation == @generation AND execution.state IN @in_flight_states LIMIT 1 RETURN true)
FILTER snapshot != null AND snapshot.state == @failed_state
FILTER active == null OR active.dataset.generation != @generation
FILTER !protectedByRelease AND inFlight == null
FOR document IN @@target_collection
  FILTER document.project == @project AND document.dataset_generation == @generation
  REMOVE document IN @@target_collection
  COLLECT WITH COUNT INTO removed
  RETURN {removed}
`

const guardedDeleteGenerationMetadataAQL = `
LET snapshot = DOCUMENT(@@lifecycle_collection, @snapshot_key)
LET manifest = DOCUMENT(@@lifecycle_collection, @manifest_key)
LET active = DOCUMENT(@@lifecycle_collection, @active_key)
LET releasePointer = DOCUMENT(@@lifecycle_collection, @active_release_key)
LET release = releasePointer == null ? null : DOCUMENT(@@lifecycle_collection, releasePointer.releaseKey)
LET protectedByRelease = release != null AND (release.generation == @generation OR @generation IN release.publications[*].generation)
LET inFlight = FIRST(FOR execution IN @@executions_collection
  LET project = execution.project != null ? execution.project : execution.Project
  LET generation = execution.datasetGeneration != null ? execution.datasetGeneration : execution.DatasetGeneration
  FILTER project == @project AND generation == @generation AND execution.state IN @in_flight_states LIMIT 1 RETURN true)
FILTER snapshot != null AND snapshot.state == @failed_state
FILTER active == null OR active.dataset.generation != @generation
FILTER !protectedByRelease AND inFlight == null
LET documents = APPEND([snapshot], manifest != null AND manifest.state == "FAILED" ? [manifest] : [])
FOR document IN documents
  REMOVE document IN @@lifecycle_collection OPTIONS {ignoreErrors: true}
  COLLECT WITH COUNT INTO removed
  RETURN {removed}
`

const readActiveReleaseAQL = `
LET pointer = DOCUMENT(@@lifecycle_collection, @key)
FILTER pointer != null AND pointer.recordType == "active_project_release"
LET release = DOCUMENT(@@lifecycle_collection, pointer.releaseKey)
FILTER release != null AND release.recordType == "project_release"
RETURN {release: UNSET(release, "_key", "_id", "_rev", "recordType"), revision: pointer.revision}
`

// Both visibility pointers are updated in the same AQL transaction. A release
// therefore cannot expose new graph data without the matching dataframe set,
// or expose dataframe metadata while graph reads still use the prior generation.
const activateReleaseAQL = `
LET manifest = DOCUMENT(@@lifecycle_collection, @manifest_key)
LET generationPointer = DOCUMENT(@@lifecycle_collection, @active_key)
LET releasePointer = DOCUMENT(@@lifecycle_collection, @active_release_key)
LET storedRelease = DOCUMENT(@@lifecycle_collection, @release_key)
LET currentRevision = releasePointer == null ? 0 : releasePointer.revision
LET alreadyActive = releasePointer != null AND releasePointer.releaseId == @release_id AND currentRevision == @expected_revision + 1
LET selectedRevision = alreadyActive ? currentRevision : @next_revision
FILTER storedRelease != null AND storedRelease.recordType == "project_release"
FILTER storedRelease.project == @project AND storedRelease.id == @release_id AND storedRelease.generation == @generation
FILTER manifest != null AND manifest.recordType == "manifest"
FILTER manifest.dataset.project == @project AND manifest.dataset.generation == @generation
FILTER manifest.state IN [@staged_state, @ready_state]
FILTER generationPointer != null AND generationPointer.recordType == "active_generation"
FILTER releasePointer != null AND releasePointer.recordType == "active_project_release"
FILTER currentRevision == @expected_revision OR alreadyActive
LET updates = [
  {document: releasePointer, patch: {releaseKey: @release_key, releaseId: @release_id, revision: selectedRevision}},
  {document: generationPointer, patch: {dataset: manifest.dataset, manifestKey: manifest._key, releaseId: @release_id, releaseRevision: selectedRevision}}
]
FOR item IN updates
  UPDATE item.document WITH item.patch IN @@lifecycle_collection OPTIONS {ignoreRevs: false, mergeObjects: false}
  FILTER item.document.recordType == "active_project_release"
  RETURN {release: UNSET(storedRelease, "_key", "_id", "_rev", "recordType"), revision: selectedRevision}
`

var _ dataset.SnapshotRepository = (*Store)(nil)
var _ dataset.ReleaseRepository = (*Store)(nil)
var _ dataset.RetentionRepository = (*Store)(nil)

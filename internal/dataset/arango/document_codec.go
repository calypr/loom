package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataset"
)

func (s *Store) validate() error {
	if s == nil || s.client == nil {
		return ErrNilQueryClient
	}
	if s.batchSize <= 0 {
		return ErrInvalidCursorBatchSize
	}
	return nil
}

func (s *Store) manifestRows(ctx context.Context, query string, bindVars map[string]any) ([]dataset.Manifest, error) {
	rows := make([]dataset.Manifest, 0, 1)
	var unexpected error
	err := s.client.QueryRows(ctx, query, s.batchSize, bindVars, func(row map[string]any) error {
		manifest, err := manifestFromValue(row)
		if err != nil {
			unexpected = err
			return err
		}
		rows = append(rows, manifest)
		if len(rows) > 1 {
			unexpected = fmt.Errorf("%w: query returned multiple manifests", ErrUnexpectedStoreResult)
			return unexpected
		}
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return nil, unexpected
		}
		return nil, err
	}
	return rows, nil
}

func lifecycleBindVars(project string) map[string]any {
	return map[string]any{
		"@lifecycle_collection": LifecycleCollection,
		"project":               project,
		"manifest_record_type":  manifestRecordType,
		"active_record_type":    activeRecordType,
	}
}

func validateProject(project string) error {
	_, err := dataset.NewDatasetRef(project, "datasetstore-project-validation")
	return err
}

func manifestDocument(manifest dataset.Manifest) (map[string]any, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode dataset manifest document: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode dataset manifest document: %w", err)
	}
	document["_key"] = manifestDocumentKey(manifest.Dataset)
	document["recordType"] = manifestRecordType
	return document, nil
}

func activePlaceholderDocument(project string) map[string]any {
	return map[string]any{
		"_key":       activeDocumentKey(project),
		"recordType": activeRecordType,
		"project":    project,
	}
}

func schemaIdentityBindValue(identity dataset.SchemaIdentitySnapshot) (map[string]any, error) {
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, fmt.Errorf("encode schema identity bind value: %w", err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode schema identity bind value: %w", err)
	}
	return value, nil
}

func manifestFromValue(value any) (dataset.Manifest, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return dataset.Manifest{}, fmt.Errorf("%w: encode manifest row: %v", ErrUnexpectedStoreResult, err)
	}
	var manifest dataset.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return dataset.Manifest{}, fmt.Errorf("%w: decode manifest row: %v", ErrUnexpectedStoreResult, err)
	}
	return manifest, nil
}

func activeResolutionFromRow(row map[string]any) (activeResolution, error) {
	active, err := activeFromValue(row["active"])
	if err != nil {
		return activeResolution{}, err
	}
	manifest, err := manifestFromValue(row["manifest"])
	if err != nil {
		return activeResolution{}, err
	}
	return activeResolution{active: active, manifest: manifest}, nil
}

func activeFromValue(value any) (dataset.ActiveGeneration, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return dataset.ActiveGeneration{}, fmt.Errorf("%w: encode active generation row: %v", ErrUnexpectedStoreResult, err)
	}
	var active dataset.ActiveGeneration
	if err := json.Unmarshal(data, &active); err != nil {
		return dataset.ActiveGeneration{}, fmt.Errorf("%w: decode active generation row: %v", ErrUnexpectedStoreResult, err)
	}
	return active, nil
}

func datasetRefFromValue(value any) (dataset.DatasetRef, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return dataset.DatasetRef{}, fmt.Errorf("%w: encode dataset reference row: %v", ErrUnexpectedStoreResult, err)
	}
	var ref dataset.DatasetRef
	if err := json.Unmarshal(data, &ref); err != nil {
		return dataset.DatasetRef{}, fmt.Errorf("%w: decode dataset reference row: %v", ErrUnexpectedStoreResult, err)
	}
	return ref, nil
}

func manifestIdentityEqual(left, right dataset.Manifest) bool {
	return left.Dataset.Equal(right.Dataset) &&
		left.State == right.State &&
		left.SchemaIdentity.Equal(right.SchemaIdentity) &&
		left.AnalysisVersion == right.AnalysisVersion
}

func manifestDocumentKey(ref dataset.DatasetRef) string {
	return documentKey("manifest", ref.Project, ref.Generation)
}

func activeDocumentKey(project string) string {
	return documentKey("active", project)
}

func documentKey(kind string, values ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(documentKeyDomain))
	for _, value := range append([]string{kind}, values...) {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return kind + "_" + hex.EncodeToString(hash.Sum(nil))
}

// The collection is always bound through @@lifecycle_collection. Project,
// generation, states, schema metadata, and document payloads are scalar bind
// values; no user-controlled identifier is interpolated into AQL text.
const createManifestAQL = `
LET existing = FIRST(
  FOR manifest IN @@lifecycle_collection
    FILTER manifest._key == @manifest_key
    FILTER manifest.recordType == @manifest_record_type
    RETURN manifest
)
FILTER existing == null
LET active = FIRST(
  FOR pointer IN @@lifecycle_collection
    FILTER pointer._key == @active_key
    FILTER pointer.recordType == @active_record_type
    FILTER pointer.project == @project
    RETURN pointer
)
LET documents = active == null ? [@manifest, @active_placeholder] : [@manifest]
FOR document IN documents
  INSERT document INTO @@lifecycle_collection
  RETURN {
    recordType: NEW.recordType,
    manifest: NEW.recordType == @manifest_record_type ? {
      dataset: NEW.dataset,
      state: NEW.state,
      schemaIdentity: NEW.schemaIdentity,
      analysisVersion: NEW.analysisVersion
    } : null
  }
`

const readManifestAQL = `
FOR manifest IN @@lifecycle_collection
  FILTER manifest._key == @manifest_key
  FILTER manifest.recordType == @manifest_record_type
  FILTER manifest.dataset.project == @project
  FILTER manifest.dataset.generation == @generation
  LIMIT 2
  RETURN {
    dataset: manifest.dataset,
    state: manifest.state,
    schemaIdentity: manifest.schemaIdentity,
    analysisVersion: manifest.analysisVersion
  }
`

const transitionManifestAQL = `
FOR manifest IN @@lifecycle_collection
  FILTER manifest._key == @manifest_key
  FILTER manifest.recordType == @manifest_record_type
  FILTER manifest.dataset.project == @project
  FILTER manifest.dataset.generation == @generation
  FILTER manifest.state == @expected_state
  FILTER manifest.schemaIdentity == @schema_identity
  FILTER manifest.analysisVersion == @analysis_version
  UPDATE manifest WITH { state: @next_state } IN @@lifecycle_collection
  RETURN {
    dataset: NEW.dataset,
    state: NEW.state,
    schemaIdentity: NEW.schemaIdentity,
    analysisVersion: NEW.analysisVersion
  }
`

const readActiveAQL = `
FOR active IN @@lifecycle_collection
  FILTER active._key == @active_key
  FILTER active.recordType == @active_record_type
  FILTER active.project == @project
  FILTER active.manifestKey != null
  FOR manifest IN @@lifecycle_collection
    FILTER manifest._key == active.manifestKey
    FILTER manifest.recordType == @manifest_record_type
    FILTER manifest.dataset == active.dataset
    FILTER manifest.state == @ready_state
    LIMIT 2
    RETURN {
      active: { dataset: active.dataset },
      manifest: {
        dataset: manifest.dataset,
        state: manifest.state,
        schemaIdentity: manifest.schemaIdentity,
        analysisVersion: manifest.analysisVersion
      }
    }
`

// activateAQL has exactly one UPDATE statement. The candidate guard updates
// READY to READY only to put its revision into the AQL write set; it is a CAS
// guard, not a lifecycle transition. With ignoreRevs:false, a concurrent
// change to the candidate, prior active manifest, or active pointer aborts the
// whole single-server transaction instead of selecting stale state.
const activateAQL = `
LET candidate = FIRST(
  FOR manifest IN @@lifecycle_collection
    FILTER manifest._key == @candidate_key
    FILTER manifest.recordType == @manifest_record_type
    FILTER manifest.dataset.project == @project
    FILTER manifest.dataset.generation == @generation
    FILTER manifest.state == @ready_state
    FILTER manifest.schemaIdentity == @schema_identity
    FILTER manifest.analysisVersion == @analysis_version
    RETURN manifest
)
LET active = FIRST(
  FOR pointer IN @@lifecycle_collection
    FILTER pointer._key == @active_key
    FILTER pointer.recordType == @active_record_type
    FILTER pointer.project == @project
    RETURN pointer
)
FILTER candidate != null
FILTER active != null
LET previous = FIRST(
  FOR manifest IN @@lifecycle_collection
    FILTER active.manifestKey != null
    FILTER manifest._key == active.manifestKey
    FILTER manifest.recordType == @manifest_record_type
    FILTER manifest.dataset == active.dataset
    FILTER manifest.state == @ready_state
    RETURN manifest
)
FILTER (
  (active.dataset == null AND active.manifestKey == null) OR
  (
    active.dataset != null AND
    active.dataset.project == @project AND
    active.manifestKey != null AND
    previous != null
  )
)
LET updates = APPEND(
  [{ document: candidate, patch: { state: @ready_state }, role: @candidate_guard_role }],
  APPEND(
    previous != null AND previous._key != candidate._key
      ? [{ document: previous, patch: { state: @superseded_state }, role: @superseded_role }]
      : [],
    [{
      document: active,
      patch: { dataset: candidate.dataset, manifestKey: candidate._key },
      role: @active_record_type
    }]
  )
)
FOR update IN updates
  UPDATE update.document WITH update.patch IN @@lifecycle_collection
    OPTIONS { ignoreRevs: false, mergeObjects: false }
  RETURN {
    role: update.role,
    dataset: NEW.dataset,
    previous: update.role == @superseded_role ? OLD.dataset : null
  }
`

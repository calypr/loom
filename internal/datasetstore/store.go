package datasetstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	defaultCursorBatchSize = 32
	documentKeyDomain      = "loom.datasetstore.v1"
)

var (
	// ErrNilQueryClient reports an unusable Store dependency.
	ErrNilQueryClient = errors.New("dataset store query client is required")
	// ErrInvalidCursorBatchSize reports a non-positive cursor batch size.
	ErrInvalidCursorBatchSize = errors.New("dataset store cursor batch size must be positive")
	// ErrManifestAlreadyExists reports an attempt to create a second immutable
	// manifest for the same project and generation.
	ErrManifestAlreadyExists = errors.New("dataset manifest already exists")
	// ErrManifestNotFound reports a missing manifest reference.
	ErrManifestNotFound = errors.New("dataset manifest was not found")
	// ErrManifestTransitionConflict reports that the persisted manifest no
	// longer exactly matches the caller's expected immutable version and state.
	ErrManifestTransitionConflict = errors.New("dataset manifest transition conflict")
	// ErrActiveGenerationNotFound reports a project with no active READY
	// generation. It also protects callers from treating a corrupt active
	// pointer as a usable generation.
	ErrActiveGenerationNotFound = errors.New("active dataset generation was not found")
	// ErrActivationConflict reports a candidate that was not persisted READY,
	// a missing active pointer record, or an invalid prior active pointer.
	ErrActivationConflict = errors.New("dataset activation conflict")
	// ErrUnexpectedStoreResult reports malformed, duplicate, or inconsistent
	// rows returned by the persistence backend.
	ErrUnexpectedStoreResult = errors.New("unexpected dataset store result")
)

// QueryRowsClient is the minimal Arango capability required by Store. The
// concrete *arango.Client satisfies it, while tests and future transaction
// adapters can supply a small fake without opening a network connection.
type QueryRowsClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
}

var _ QueryRowsClient = (*arangostore.Client)(nil)
var _ dataset.ActiveManifestResolver = (*Store)(nil)

// Store persists dataset manifests and active-generation pointers. Its zero
// value is intentionally unusable; construct it with New or NewWithBatchSize.
type Store struct {
	client    QueryRowsClient
	batchSize int
}

// New constructs a Store with a small metadata-oriented cursor batch size.
func New(client QueryRowsClient) (*Store, error) {
	return NewWithBatchSize(client, defaultCursorBatchSize)
}

// NewWithBatchSize constructs a Store with an explicit positive cursor batch
// size. It performs no I/O and does not bootstrap collections.
func NewWithBatchSize(client QueryRowsClient, batchSize int) (*Store, error) {
	if client == nil {
		return nil, ErrNilQueryClient
	}
	if batchSize <= 0 {
		return nil, ErrInvalidCursorBatchSize
	}
	return &Store{client: client, batchSize: batchSize}, nil
}

// CreateManifest persists a new immutable PREFLIGHT manifest. It uses a
// deterministic, opaque document key for the project/generation reference and
// creates an empty active-generation pointer for a project the first time it
// is seen. Neither document contains authorization scope data.
func (s *Store) CreateManifest(ctx context.Context, manifest dataset.Manifest) (dataset.Manifest, error) {
	if err := manifest.Validate(); err != nil {
		return dataset.Manifest{}, fmt.Errorf("create dataset manifest: %w", err)
	}
	if manifest.State != dataset.ManifestStatePreflight {
		return dataset.Manifest{}, fmt.Errorf("create dataset manifest: %w: state must be %s, got %s", dataset.ErrInvalidTransition, dataset.ManifestStatePreflight, manifest.State)
	}
	if err := s.validate(); err != nil {
		return dataset.Manifest{}, err
	}

	document, err := manifestDocument(manifest)
	if err != nil {
		return dataset.Manifest{}, err
	}
	bindVars := lifecycleBindVars(manifest.Dataset.Project)
	bindVars["manifest"] = document
	bindVars["active_placeholder"] = activePlaceholderDocument(manifest.Dataset.Project)
	bindVars["manifest_key"] = manifestDocumentKey(manifest.Dataset)
	bindVars["active_key"] = activeDocumentKey(manifest.Dataset.Project)

	var created *dataset.Manifest
	var unexpected error
	err = s.client.QueryRows(ctx, createManifestAQL, s.batchSize, bindVars, func(row map[string]any) error {
		recordType, _ := row["recordType"].(string)
		switch recordType {
		case manifestRecordType:
			if created != nil {
				unexpected = fmt.Errorf("%w: create returned more than one manifest", ErrUnexpectedStoreResult)
				return unexpected
			}
			decoded, err := manifestFromValue(row["manifest"])
			if err != nil {
				unexpected = err
				return err
			}
			if !decoded.Dataset.Equal(manifest.Dataset) || decoded.State != dataset.ManifestStatePreflight || !decoded.SchemaIdentity.Equal(manifest.SchemaIdentity) || decoded.AnalysisVersion != manifest.AnalysisVersion {
				unexpected = fmt.Errorf("%w: created manifest does not match request", ErrUnexpectedStoreResult)
				return unexpected
			}
			copy := decoded.Clone()
			created = &copy
		case activeRecordType:
			// The first manifest for a project also inserts its empty active
			// pointer. There is deliberately no active dataset to decode yet.
		default:
			unexpected = fmt.Errorf("%w: create returned record type %q", ErrUnexpectedStoreResult, recordType)
			return unexpected
		}
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return dataset.Manifest{}, unexpected
		}
		return dataset.Manifest{}, fmt.Errorf("create dataset manifest: %w", err)
	}
	if created == nil {
		return dataset.Manifest{}, fmt.Errorf("%w: %s/%s", ErrManifestAlreadyExists, manifest.Dataset.Project, manifest.Dataset.Generation)
	}
	return created.Clone(), nil
}

// ReadManifest returns exactly one persisted manifest named by ref. It does
// not infer a generation or silently select a different one.
func (s *Store) ReadManifest(ctx context.Context, ref dataset.DatasetRef) (dataset.Manifest, error) {
	if err := ref.Validate(); err != nil {
		return dataset.Manifest{}, fmt.Errorf("read dataset manifest: %w", err)
	}
	if err := s.validate(); err != nil {
		return dataset.Manifest{}, err
	}
	bindVars := lifecycleBindVars(ref.Project)
	bindVars["manifest_key"] = manifestDocumentKey(ref)
	bindVars["generation"] = ref.Generation

	rows, err := s.manifestRows(ctx, readManifestAQL, bindVars)
	if err != nil {
		return dataset.Manifest{}, fmt.Errorf("read dataset manifest: %w", err)
	}
	if len(rows) == 0 {
		return dataset.Manifest{}, fmt.Errorf("%w: %s/%s", ErrManifestNotFound, ref.Project, ref.Generation)
	}
	if len(rows) != 1 {
		return dataset.Manifest{}, fmt.Errorf("%w: read returned %d manifests for %s/%s", ErrUnexpectedStoreResult, len(rows), ref.Project, ref.Generation)
	}
	manifest := rows[0]
	if !manifest.Dataset.Equal(ref) {
		return dataset.Manifest{}, fmt.Errorf("%w: read manifest reference does not match request", ErrUnexpectedStoreResult)
	}
	return manifest.Clone(), nil
}

// TransitionManifest applies one allowed lifecycle transition to the exact
// immutable manifest value supplied by the caller. The AQL filter includes
// schema and analysis metadata as well as the current state, so a stale or
// substituted manifest cannot mutate a different persisted generation.
func (s *Store) TransitionManifest(ctx context.Context, manifest dataset.Manifest, next dataset.ManifestState) (dataset.Manifest, error) {
	nextManifest, err := manifest.Transition(next)
	if err != nil {
		return dataset.Manifest{}, fmt.Errorf("transition dataset manifest: %w", err)
	}
	if err := s.validate(); err != nil {
		return dataset.Manifest{}, err
	}

	schemaIdentity, err := schemaIdentityBindValue(manifest.SchemaIdentity)
	if err != nil {
		return dataset.Manifest{}, err
	}
	bindVars := lifecycleBindVars(manifest.Dataset.Project)
	bindVars["manifest_key"] = manifestDocumentKey(manifest.Dataset)
	bindVars["generation"] = manifest.Dataset.Generation
	bindVars["expected_state"] = string(manifest.State)
	bindVars["next_state"] = string(next)
	bindVars["schema_identity"] = schemaIdentity
	bindVars["analysis_version"] = string(manifest.AnalysisVersion)

	rows, err := s.manifestRows(ctx, transitionManifestAQL, bindVars)
	if err != nil {
		return dataset.Manifest{}, fmt.Errorf("transition dataset manifest: %w", err)
	}
	if len(rows) == 0 {
		return dataset.Manifest{}, fmt.Errorf("%w: %s/%s was not %s with the expected immutable metadata", ErrManifestTransitionConflict, manifest.Dataset.Project, manifest.Dataset.Generation, manifest.State)
	}
	if len(rows) != 1 {
		return dataset.Manifest{}, fmt.Errorf("%w: transition returned %d manifests", ErrUnexpectedStoreResult, len(rows))
	}
	persisted := rows[0]
	if !manifestIdentityEqual(persisted, nextManifest) {
		return dataset.Manifest{}, fmt.Errorf("%w: transition result does not match requested state", ErrUnexpectedStoreResult)
	}
	return persisted.Clone(), nil
}

// ReadActive returns the active generation only when its pointer resolves to
// the exact persisted READY manifest. It is a convenience wrapper around
// ResolveActiveManifest; callers that also need immutable generation metadata
// should use that method to avoid a second read and an active-switch race.
func (s *Store) ReadActive(ctx context.Context, project string) (dataset.ActiveGeneration, error) {
	resolution, err := s.resolveActive(ctx, project)
	if err != nil {
		return dataset.ActiveGeneration{}, err
	}
	return resolution.active, nil
}

// ResolveActiveManifest returns the immutable READY manifest named by a
// project's active pointer using one AQL join. It is the race-free read entry
// point for future discovery, dataframe, cache, and export adapters that need
// both the active selection and its schema or analysis metadata.
func (s *Store) ResolveActiveManifest(ctx context.Context, project string) (dataset.Manifest, error) {
	resolution, err := s.resolveActive(ctx, project)
	if err != nil {
		return dataset.Manifest{}, err
	}
	return resolution.manifest.Clone(), nil
}

type activeResolution struct {
	active   dataset.ActiveGeneration
	manifest dataset.Manifest
}

func (s *Store) resolveActive(ctx context.Context, project string) (activeResolution, error) {
	if err := validateProject(project); err != nil {
		return activeResolution{}, fmt.Errorf("resolve active dataset generation: %w", err)
	}
	if err := s.validate(); err != nil {
		return activeResolution{}, err
	}
	bindVars := lifecycleBindVars(project)
	bindVars["active_key"] = activeDocumentKey(project)
	bindVars["ready_state"] = string(dataset.ManifestStateReady)

	var resolutions []activeResolution
	var unexpected error
	err := s.client.QueryRows(ctx, readActiveAQL, s.batchSize, bindVars, func(row map[string]any) error {
		decoded, err := activeResolutionFromRow(row)
		if err != nil {
			unexpected = err
			return err
		}
		if decoded.active.Dataset.Project != project || !decoded.active.Dataset.Equal(decoded.manifest.Dataset) || decoded.manifest.State != dataset.ManifestStateReady {
			unexpected = fmt.Errorf("%w: active pointer and READY manifest are inconsistent", ErrUnexpectedStoreResult)
			return unexpected
		}
		resolutions = append(resolutions, decoded)
		if len(resolutions) > 1 {
			unexpected = fmt.Errorf("%w: read active returned multiple rows", ErrUnexpectedStoreResult)
			return unexpected
		}
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return activeResolution{}, unexpected
		}
		return activeResolution{}, fmt.Errorf("resolve active dataset generation: %w", err)
	}
	if len(resolutions) == 0 {
		return activeResolution{}, fmt.Errorf("%w: %s", ErrActiveGenerationNotFound, project)
	}
	return resolutions[0], nil
}

// Activate atomically selects a persisted READY candidate as a project's
// active generation. If a different READY generation was already active, the
// same single AQL UPDATE statement changes it to SUPERSEDED. The returned
// plan records the switch that was actually performed. The statement includes
// a state-preserving candidate update with revision checking so a concurrently
// superseded candidate cannot be selected from a stale read snapshot.
func (s *Store) Activate(ctx context.Context, candidate dataset.Manifest) (dataset.ActivationPlan, error) {
	active, err := dataset.ActiveGenerationFor(candidate)
	if err != nil {
		return dataset.ActivationPlan{}, fmt.Errorf("activate dataset generation: %w", err)
	}
	if err := s.validate(); err != nil {
		return dataset.ActivationPlan{}, err
	}
	schemaIdentity, err := schemaIdentityBindValue(candidate.SchemaIdentity)
	if err != nil {
		return dataset.ActivationPlan{}, err
	}

	bindVars := lifecycleBindVars(candidate.Dataset.Project)
	bindVars["candidate_key"] = manifestDocumentKey(candidate.Dataset)
	bindVars["active_key"] = activeDocumentKey(candidate.Dataset.Project)
	bindVars["generation"] = candidate.Dataset.Generation
	bindVars["schema_identity"] = schemaIdentity
	bindVars["analysis_version"] = string(candidate.AnalysisVersion)
	bindVars["ready_state"] = string(dataset.ManifestStateReady)
	bindVars["superseded_state"] = string(dataset.ManifestStateSuperseded)
	bindVars["superseded_role"] = "superseded_manifest"
	bindVars["candidate_guard_role"] = "candidate_guard"

	var result dataset.ActivationPlan
	var candidateGuardSeen bool
	var activeSeen bool
	var previousSeen bool
	var unexpected error
	err = s.client.QueryRows(ctx, activateAQL, s.batchSize, bindVars, func(row map[string]any) error {
		role, _ := row["role"].(string)
		switch role {
		case "candidate_guard":
			if candidateGuardSeen {
				unexpected = fmt.Errorf("%w: activation returned more than one candidate guard", ErrUnexpectedStoreResult)
				return unexpected
			}
			guarded, err := datasetRefFromValue(row["dataset"])
			if err != nil {
				unexpected = err
				return err
			}
			if !guarded.Equal(candidate.Dataset) {
				unexpected = fmt.Errorf("%w: candidate guard updated a different dataset", ErrUnexpectedStoreResult)
				return unexpected
			}
			candidateGuardSeen = true
		case activeRecordType:
			if activeSeen {
				unexpected = fmt.Errorf("%w: activation returned more than one active pointer", ErrUnexpectedStoreResult)
				return unexpected
			}
			decoded, err := activeFromValue(map[string]any{"dataset": row["dataset"]})
			if err != nil {
				unexpected = err
				return err
			}
			if !decoded.Dataset.Equal(active.Dataset) {
				unexpected = fmt.Errorf("%w: activation selected a different dataset", ErrUnexpectedStoreResult)
				return unexpected
			}
			result.Active = decoded
			activeSeen = true
		case "superseded_manifest":
			if previousSeen {
				unexpected = fmt.Errorf("%w: activation returned more than one superseded manifest", ErrUnexpectedStoreResult)
				return unexpected
			}
			previous, err := datasetRefFromValue(row["previous"])
			if err != nil {
				unexpected = err
				return err
			}
			if previous.Project != candidate.Dataset.Project || previous.Equal(candidate.Dataset) {
				unexpected = fmt.Errorf("%w: invalid superseded dataset reference", ErrUnexpectedStoreResult)
				return unexpected
			}
			result.Previous = &previous
			previousSeen = true
		default:
			unexpected = fmt.Errorf("%w: activation returned role %q", ErrUnexpectedStoreResult, role)
			return unexpected
		}
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return dataset.ActivationPlan{}, unexpected
		}
		return dataset.ActivationPlan{}, fmt.Errorf("activate dataset generation: %w", err)
	}
	if !candidateGuardSeen || !activeSeen {
		return dataset.ActivationPlan{}, fmt.Errorf("%w: candidate %s/%s was not a persisted READY manifest with a valid active pointer", ErrActivationConflict, candidate.Dataset.Project, candidate.Dataset.Generation)
	}
	if err := result.Validate(); err != nil {
		return dataset.ActivationPlan{}, fmt.Errorf("%w: activation result: %v", ErrUnexpectedStoreResult, err)
	}
	return result, nil
}

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
